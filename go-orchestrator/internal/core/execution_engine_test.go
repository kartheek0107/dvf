package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
)

type mockStore struct {
	mu      sync.Mutex
	runs    map[string]TestRunStatus
	errors  map[string]string
	results map[string][]*TestResult
}

func newMockStore() *mockStore {
	return &mockStore{
		runs:    make(map[string]TestRunStatus),
		errors:  make(map[string]string),
		results: make(map[string][]*TestResult),
	}
}

func (m *mockStore) UpdateTestRunStatus(ctx context.Context, id string, status TestRunStatus, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[id] = status
	if errMsg != "" {
		m.errors[id] = errMsg
	}
	return nil
}

func (m *mockStore) GetTestResults(ctx context.Context, testRunID string) ([]*TestResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.results[testRunID], nil
}

func (m *mockStore) SaveTestResult(ctx context.Context, result *TestResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[result.TestRunID] = append(m.results[result.TestRunID], result)
	return nil
}

type mockVMManager struct{}

func (m *mockVMManager) CreateVM(ctx context.Context, vmCfg interface{}) (*VMInstance, error) {
	return &VMInstance{
		ID:             "vm-test",
		Status:         VMStatusReady,
		AllocatedCPUs:  2,
		AllocatedMemMB: 512,
	}, nil
}

func (m *mockVMManager) StartVM(ctx context.Context, vmID string) error {
	return nil
}

func (m *mockVMManager) StopVM(ctx context.Context, vmID string) error {
	return nil
}

func (m *mockVMManager) DestroyVM(ctx context.Context, vmID string) error {
	return nil
}

type mockAgentCoordinator struct {
	mu           sync.Mutex
	commandCount int
	agentStatus  string
	outputJSON   string
}

func (m *mockAgentCoordinator) RegisterVM(vmID string)   {}
func (m *mockAgentCoordinator) UnregisterVM(vmID string) {}

func (m *mockAgentCoordinator) WaitForAgent(ctx context.Context, vmID string) error {
	return nil
}

func (m *mockAgentCoordinator) SendCommand(ctx context.Context, vmID string, cmd *AgentCommand) (*AgentResult, error) {
	m.mu.Lock()
	m.commandCount++
	m.mu.Unlock()

	if cmd.Type == "verify_device" {
		return &AgentResult{
			CommandID: cmd.ID,
			Status:    "passed",
			Output:    "device verified",
		}, nil
	}

	if cmd.Type == "load_driver" {
		return &AgentResult{
			CommandID: cmd.ID,
			Status:    "passed",
			Output:    "driver loaded",
		}, nil
	}

	status := m.agentStatus
	if status == "" {
		status = "passed"
	}

	outputJSON := m.outputJSON
	if outputJSON == "" {
		outputJSON = `{
			"results": [
				{
					"test": "read_write_test",
					"status": "passed",
					"duration_ms": 12.5,
					"message": "success",
					"metrics": {"throughput_mbps": 450.5}
				}
			]
		}`
	}

	return &AgentResult{
		CommandID:  cmd.ID,
		Status:     status,
		Output:     outputJSON,
		DurationMs: 15,
	}, nil
}

func TestExecutionEngine_WorkflowExecution(t *testing.T) {
	store := newMockStore()
	logger, _ := zap.NewDevelopment()

	// Setup simple config
	cfg := &config.GlobalConfig{
		VMDefaults: config.VMDefaultsConfig{
			MaxConcurrentVMs:   2,
			TestTimeoutSeconds: 30,
		},
		QEMU: config.QEMUConfig{
			DefaultCPUs:     1,
			DefaultMemoryMB: 256,
		},
	}

	// Setup device registry
	registry := &config.DeviceRegistry{
		Devices: []config.DeviceEntry{
			{
				ID:             "gpgpu",
				TargetModes:    []string{"qemu"},
				DeviceNode:     "/dev/gpgpu",
				DriverModule:   "gpgpu_drv",
				QEMUDeviceName: "gp_gpu",
			},
		},
	}

	allocator := NewResourceAllocator(4, 1024)
	scheduler := NewScheduler(2)

	engine := NewExecutionEngine(
		scheduler,
		store,
		registry,
		&mockVMManager{},
		&mockAgentCoordinator{},
		allocator,
		cfg,
		logger,
		nil,
		nil,
	)

	engine.Start()
	defer engine.Stop()

	// Submit a test run using the smoke suite
	run := &TestRun{
		ID:          "run-smoke-test",
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
		Status:      TestRunStatusPending,
		Priority:    1,
	}

	engine.SubmitTestRun(run)

	// Wait up to 5 seconds for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		status := store.runs[run.ID]
		store.mu.Unlock()

		if status == TestRunStatusPassed || status == TestRunStatusFailed || status == TestRunStatusErrored {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	store.mu.Lock()
	finalStatus := store.runs[run.ID]
	errMsg := store.errors[run.ID]
	results := store.results[run.ID]
	store.mu.Unlock()

	if finalStatus != TestRunStatusPassed {
		t.Fatalf("expected test run status PASSED, got %s (error: %s)", finalStatus, errMsg)
	}

	if len(results) == 0 {
		t.Fatal("expected test results to be saved, got none")
	}

	// Verify step result values
	firstRes := results[0]
	if firstRes.TestName != "read_write_test" {
		t.Errorf("expected test name 'read_write_test', got %q", firstRes.TestName)
	}
	if firstRes.Status != TestRunStatusPassed {
		t.Errorf("expected step status PASSED, got %s", firstRes.Status)
	}
}

func TestExecutionEngine_ResourceAllocatorGating(t *testing.T) {
	store := newMockStore()
	logger, _ := zap.NewDevelopment()

	cfg := &config.GlobalConfig{
		VMDefaults: config.VMDefaultsConfig{
			MaxConcurrentVMs:   2,
			TestTimeoutSeconds: 30,
		},
		QEMU: config.QEMUConfig{
			DefaultCPUs:     2,
			DefaultMemoryMB: 512,
		},
	}

	registry := &config.DeviceRegistry{
		Devices: []config.DeviceEntry{
			{
				ID:             "gpgpu",
				TargetModes:    []string{"qemu"},
				DeviceNode:     "/dev/gpgpu",
				DriverModule:   "gpgpu_drv",
				QEMUDeviceName: "gp_gpu",
			},
		},
	}

	// Total budget: 2 CPUs, 512 MB memory.
	// Since each run requests 2 CPUs and 512 MB memory, only ONE run can run at a time!
	allocator := NewResourceAllocator(2, 512)
	scheduler := NewScheduler(2)

	engine := NewExecutionEngine(
		scheduler,
		store,
		registry,
		&mockVMManager{},
		&mockAgentCoordinator{},
		allocator,
		cfg,
		logger,
		nil,
		nil,
	)

	engine.Start()
	defer engine.Stop()

	// 1. Manually exhaust the allocator resources first
	alloc := Allocation{CPUs: 2, MemMB: 512}
	if !allocator.TryAcquire(alloc) {
		t.Fatal("expected manually acquiring resource to succeed")
	}

	// 2. Submit test run — should get deferred because resources are exhausted
	run := &TestRun{
		ID:          "run-deferred-test",
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
		Status:      TestRunStatusPending,
		Priority:    1,
	}

	engine.SubmitTestRun(run)

	// Wait briefly and verify status goes to PENDING with "waiting for resources"
	time.Sleep(500 * time.Millisecond)

	store.mu.Lock()
	status := store.runs[run.ID]
	errMsg := store.errors[run.ID]
	store.mu.Unlock()

	if status != TestRunStatusPending {
		t.Errorf("expected status PENDING while waiting for resources, got %s", status)
	}
	if errMsg != "waiting for resources" {
		t.Errorf("expected message 'waiting for resources', got %q", errMsg)
	}

	// 3. Release the manual reservation to allow the engine to proceed
	allocator.Release(alloc)

	// Wait up to 5 seconds for completion now
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		status = store.runs[run.ID]
		store.mu.Unlock()

		if status == TestRunStatusPassed || status == TestRunStatusFailed || status == TestRunStatusErrored {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	store.mu.Lock()
	status = store.runs[run.ID]
	store.mu.Unlock()

	if status != TestRunStatusPassed {
		t.Errorf("expected status to eventually transition to PASSED, got %s", status)
	}
}

// TestExecutionEngine_GPURTErrors_ReportedAsFailed is the regression test for
// the "PASSED with GPURT errors" bug.
//
// Scenario: the agent returns status="failed" (after our fix, the agent reconciles
// exit code 0 + GPURT errors to "failed") and an output JSON with failed=1.
// The orchestrator must store the test result as FAILED, not PASSED.
func TestExecutionEngine_GPURTErrors_ReportedAsFailed(t *testing.T) {
	gpurtErrorOutput := `{
		"suite": "vishwa/regression/vecaddx",
		"results": [
			{
				"test": "vishwa/regression/vecaddx",
				"status": "FAIL",
				"duration_ms": 500.0,
				"message": "[GPURT ERROR] device open failed: No such file or directory"
			}
		],
		"summary": {"total": 1, "passed": 0, "failed": 1, "duration_ms": 500.0}
	}`

	store := newMockStore()
	agent := &mockAgentCoordinator{
		agentStatus: "failed",
		outputJSON:  gpurtErrorOutput,
	}

	logger, _ := zap.NewDevelopment()
	cfg := &config.GlobalConfig{
		VMDefaults: config.VMDefaultsConfig{
			MaxConcurrentVMs:   2,
			TestTimeoutSeconds: 30,
		},
		QEMU: config.QEMUConfig{
			DefaultCPUs:     1,
			DefaultMemoryMB: 256,
		},
	}
	registry := &config.DeviceRegistry{
		Devices: []config.DeviceEntry{
			{
				ID:             "gpgpu",
				TargetModes:    []string{"qemu"},
				DeviceNode:     "/dev/gpgpu",
				DriverModule:   "gpgpu_drv",
				QEMUDeviceName: "gp_gpu",
			},
		},
	}
	allocator := NewResourceAllocator(4, 1024)
	scheduler := NewScheduler(2)

	engine := NewExecutionEngine(
		scheduler,
		store,
		registry,
		&mockVMManager{},
		agent,
		allocator,
		cfg,
		logger,
		nil,
		nil,
	)

	engine.Start()
	defer engine.Stop()

	run := &TestRun{
		ID:          "run-gpurt-error-test",
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
		Status:      TestRunStatusPending,
		Priority:    1,
	}

	engine.SubmitTestRun(run)

	// Wait up to 5 seconds for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		status := store.runs[run.ID]
		store.mu.Unlock()

		if status == TestRunStatusPassed || status == TestRunStatusFailed || status == TestRunStatusErrored {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	store.mu.Lock()
	finalStatus := store.runs[run.ID]
	results := store.results[run.ID]
	store.mu.Unlock()

	// The overall run must be FAILED, not PASSED.
	if finalStatus != TestRunStatusFailed {
		t.Errorf("expected run status FAILED (GPURT errors detected), got %s", finalStatus)
	}

	if len(results) == 0 {
		t.Fatal("expected individual test results to be saved")
	}
	for _, r := range results {
		if r.Status == TestRunStatusPassed {
			t.Errorf("result %q has status PASSED despite GPURT errors", r.TestName)
		}
	}
}

// TestExecutionEngine_GenuinePass_StaysPassedAfterReconciliation verifies that
// a real PASS (agent says "passed" AND parser agrees) is not wrongly marked FAILED.
func TestExecutionEngine_GenuinePass_StaysPassedAfterReconciliation(t *testing.T) {
	genuinePassOutput := `{
		"suite": "vecadd",
		"results": [
			{
				"test": "vector_addition",
				"status": "PASS",
				"duration_ms": 15.0,
				"message": ""
			}
		],
		"summary": {"total": 1, "passed": 1, "failed": 0, "duration_ms": 15.0}
	}`

	store := newMockStore()
	agent := &mockAgentCoordinator{
		agentStatus: "passed",
		outputJSON:  genuinePassOutput,
	}

	logger, _ := zap.NewDevelopment()
	cfg := &config.GlobalConfig{
		VMDefaults: config.VMDefaultsConfig{
			MaxConcurrentVMs:   2,
			TestTimeoutSeconds: 30,
		},
		QEMU: config.QEMUConfig{
			DefaultCPUs:     1,
			DefaultMemoryMB: 256,
		},
	}
	registry := &config.DeviceRegistry{
		Devices: []config.DeviceEntry{
			{
				ID:             "gpgpu",
				TargetModes:    []string{"qemu"},
				DeviceNode:     "/dev/gpgpu",
				DriverModule:   "gpgpu_drv",
				QEMUDeviceName: "gp_gpu",
			},
		},
	}
	allocator := NewResourceAllocator(4, 1024)
	scheduler := NewScheduler(2)

	engine := NewExecutionEngine(
		scheduler,
		store,
		registry,
		&mockVMManager{},
		agent,
		allocator,
		cfg,
		logger,
		nil,
		nil,
	)

	engine.Start()
	defer engine.Stop()

	run := &TestRun{
		ID:          "run-genuine-pass-test",
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
		Status:      TestRunStatusPending,
		Priority:    1,
	}

	engine.SubmitTestRun(run)

	// Wait up to 5 seconds for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		status := store.runs[run.ID]
		store.mu.Unlock()

		if status == TestRunStatusPassed || status == TestRunStatusFailed || status == TestRunStatusErrored {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	store.mu.Lock()
	finalStatus := store.runs[run.ID]
	store.mu.Unlock()

	if finalStatus != TestRunStatusPassed {
		t.Errorf("expected genuine pass to remain PASSED, got %s", finalStatus)
	}
}

// TestMapAgentStatus verifies all known agent status strings are mapped correctly.
func TestMapAgentStatus(t *testing.T) {
	cases := []struct {
		input    string
		expected TestRunStatus
	}{
		{"passed", TestRunStatusPassed},
		{"PASS", TestRunStatusPassed},
		{"failed", TestRunStatusFailed},
		{"FAIL", TestRunStatusFailed},
		{"errored", TestRunStatusErrored},
		{"unknown_value", TestRunStatusErrored},
		{"", TestRunStatusErrored},
	}

	for _, tc := range cases {
		got := mapAgentStatus(tc.input)
		if got != tc.expected {
			t.Errorf("mapAgentStatus(%q): expected %s, got %s", tc.input, tc.expected, got)
		}
	}
}

// TestIsVishwaSuite verifies prefix detection for Vishwa test IDs.
func TestIsVishwaSuite(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"vishwa/regression/vecaddx", true},
		{"vishwa/opencl/vecadd", true},
		{"smoke", false},
		{"regression/patterns", false},
		{"vishwa", false},  // prefix itself, too short
		{"", false},
	}

	for _, tc := range cases {
		got := isVishwaSuite(tc.input)
		if got != tc.expected {
			t.Errorf("isVishwaSuite(%q): expected %v, got %v", tc.input, tc.expected, got)
		}
	}
}
