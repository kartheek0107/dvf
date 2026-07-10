// Package core provides the orchestration logic for the DVF orchestrator.
//
// This file implements the Execution Engine — the "brain" that ties
// VM lifecycle management to test run execution. When a test run is
// submitted, the engine picks it up from the scheduler, provisions a VM,
// waits for the guest agent, dispatches tests, collects results, and
// tears everything down.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
)

// VMManagerInterface abstracts the VM Manager so the engine can be tested
// without needing a real QEMU installation.
type VMManagerInterface interface {
	CreateVM(ctx context.Context, vmCfg interface{}) (*VMInstance, error)
	StartVM(ctx context.Context, vmID string) error
	StopVM(ctx context.Context, vmID string) error
	DestroyVM(ctx context.Context, vmID string) error
}

// AgentCoordinatorInterface abstracts the agent coordination layer.
type AgentCoordinatorInterface interface {
	// RegisterVM prepares the coordinator to receive an agent for the given VM.
	RegisterVM(vmID string)
	// UnregisterVM cleans up agent state for a VM.
	UnregisterVM(vmID string)

	// WaitForAgent blocks until the agent in the given VM registers, or
	// until the context is cancelled.
	WaitForAgent(ctx context.Context, vmID string) error

	// SendCommand sends a command to the agent in the given VM and waits
	// for the result.
	SendCommand(ctx context.Context, vmID string, cmd *AgentCommand) (*AgentResult, error)
}

// AgentCommand represents a command to send to a guest agent.
type AgentCommand struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"` // "load_driver", "verify_device", "start_test", "shutdown"
	Parameters map[string]interface{} `json:"parameters"`
}

// AgentResult represents the result of a command executed by the guest agent.
type AgentResult struct {
	CommandID  string `json:"command_id"`
	Status     string `json:"status"` // "passed", "failed", "errored"
	Output     string `json:"output"` // JSON-encoded test results
	Logs       string `json:"logs"`
	DurationMs int64  `json:"duration_ms"`
}

// EngineStore defines the storage operations needed by the execution engine.
// This is a subset of the full Store interface, defined here to avoid an
// import cycle between core and storage.
type EngineStore interface {
	UpdateTestRunStatus(ctx context.Context, id string, status TestRunStatus, errMsg string) error
	GetTestResults(ctx context.Context, testRunID string) ([]*TestResult, error)
	SaveTestResult(ctx context.Context, result *TestResult) error
}

// ExecutionEngine orchestrates the end-to-end lifecycle of test runs.
type ExecutionEngine struct {
	scheduler  *Scheduler
	store      EngineStore
	registry   *config.DeviceRegistry
	vmManager  VMManagerInterface
	agentCoord AgentCoordinatorInterface
	logger     *zap.Logger
	cfg        *config.GlobalConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewExecutionEngine creates a new execution engine.
func NewExecutionEngine(
	scheduler *Scheduler,
	store EngineStore,
	registry *config.DeviceRegistry,
	vmManager VMManagerInterface,
	agentCoord AgentCoordinatorInterface,
	cfg *config.GlobalConfig,
	logger *zap.Logger,
) *ExecutionEngine {
	ctx, cancel := context.WithCancel(context.Background())
	return &ExecutionEngine{
		scheduler:  scheduler,
		store:      store,
		registry:   registry,
		vmManager:  vmManager,
		agentCoord: agentCoord,
		cfg:        cfg,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start launches the engine's background worker goroutine that polls
// the scheduler for pending test runs and executes them.
func (e *ExecutionEngine) Start() {
	e.wg.Add(1)
	go e.worker()
	e.logger.Info("execution engine started")
}

// Stop gracefully shuts down the engine, waiting for in-flight runs to complete.
func (e *ExecutionEngine) Stop() {
	e.logger.Info("execution engine stopping...")
	e.cancel()
	e.scheduler.Close()
	e.wg.Wait()
	e.logger.Info("execution engine stopped")
}

// SubmitTestRun enqueues a test run for execution.
func (e *ExecutionEngine) SubmitTestRun(run *TestRun) {
	e.scheduler.Enqueue(run)
	e.logger.Info("test run enqueued",
		zap.String("id", run.ID),
		zap.String("device", run.DeviceID),
		zap.String("suite", run.TestSuiteID),
	)
}

// worker is the background goroutine that continuously pulls test runs
// from the scheduler and executes them.
func (e *ExecutionEngine) worker() {
	defer e.wg.Done()

	for {
		// This blocks until work is available and a slot is free.
		run := e.scheduler.Next()
		if run == nil {
			// Scheduler was closed — shut down.
			return
		}

		// Execute in a new goroutine so we can process multiple runs
		// concurrently (up to the scheduler's concurrency limit).
		e.wg.Add(1)
		go func(r *TestRun) {
			defer e.wg.Done()
			defer e.scheduler.Release()

			if err := e.executeTestRun(e.ctx, r); err != nil {
				e.logger.Error("test run failed",
					zap.String("id", r.ID),
					zap.Error(err),
				)
			}
		}(run)
	}
}

// executeTestRun is the main lifecycle method for a single test run.
//
// Steps:
//  1. Update status → RUNNING
//  2. Look up device in registry
//  3. Create + start VM
//  4. Wait for guest agent to register
//  5. Send load_driver command
//  6. For each test: send start_test, collect result
//  7. Tear down VM
//  8. Update final status (PASSED/FAILED/ERRORED)
func (e *ExecutionEngine) executeTestRun(ctx context.Context, run *TestRun) error {
	runCtx, runCancel := context.WithTimeout(ctx,
		time.Duration(e.cfg.VMDefaults.TestTimeoutSeconds)*time.Second)
	defer runCancel()

	e.logger.Info("executing test run",
		zap.String("id", run.ID),
		zap.String("device", run.DeviceID),
		zap.String("suite", run.TestSuiteID),
	)

	// Step 1: Update status → RUNNING
	if err := e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusRunning, ""); err != nil {
		return fmt.Errorf("updating status to RUNNING: %w", err)
	}

	// Step 2: Look up device
	device, err := e.registry.FindDevice(run.DeviceID)
	if err != nil {
		e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored,
			fmt.Sprintf("device not found: %v", err))
		return fmt.Errorf("device lookup: %w", err)
	}

	// Step 2b: Gate on target_mode.
	// Devices like FPGA have target_modes:["fpga","hybrid"] and require a
	// real physical PCIe device passed via VFIO. Trying to boot them via
	// plain QEMU (no host= argument) causes an immediate QEMU exit.
	// Skip such runs gracefully instead of creating a VM that will fail.
	if !deviceSupportsQEMU(device.TargetModes) {
		msg := fmt.Sprintf(
			"skipped: device %q target_modes %v does not include 'qemu'; "+
				"physical hardware passthrough required",
			device.ID, device.TargetModes,
		)
		e.logger.Warn("skipping non-QEMU device", zap.String("device", device.ID),
			zap.Strings("target_modes", device.TargetModes))
		e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusCancelled, msg)
		return nil // not an error — expected skip
	}

	// Step 3: Create + start VM
	// We pass the device entry as a generic interface since VMManagerInterface
	// uses interface{} for flexibility.
	vmInstance, err := e.vmManager.CreateVM(runCtx, device)
	if err != nil {
		e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored,
			fmt.Sprintf("VM creation failed: %v", err))
		return fmt.Errorf("creating VM: %w", err)
	}


	// Update the test run with the VM ID
	run.VMID = vmInstance.ID

	if e.agentCoord != nil {
		e.agentCoord.RegisterVM(vmInstance.ID)
	}

	if err := e.vmManager.StartVM(runCtx, vmInstance.ID); err != nil {
		if e.agentCoord != nil {
			e.agentCoord.UnregisterVM(vmInstance.ID)
		}
		e.vmManager.DestroyVM(runCtx, vmInstance.ID)
		e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored,
			fmt.Sprintf("VM start failed: %v", err))
		return fmt.Errorf("starting VM: %w", err)
	}

	// Ensure we clean up the VM no matter what
	defer func() {
		e.logger.Info("tearing down VM", zap.String("vm_id", vmInstance.ID))
		if e.agentCoord != nil {
			e.agentCoord.UnregisterVM(vmInstance.ID)
		}
		e.vmManager.StopVM(context.Background(), vmInstance.ID)
		e.vmManager.DestroyVM(context.Background(), vmInstance.ID)
	}()

	// Step 4: Wait for guest agent
	if e.agentCoord != nil {
		agentCtx, agentCancel := context.WithTimeout(runCtx,
			time.Duration(e.cfg.VMDefaults.BootTimeoutSeconds)*time.Second)
		defer agentCancel()

		e.logger.Info("waiting for agent", zap.String("vm_id", vmInstance.ID))
		if err := e.agentCoord.WaitForAgent(agentCtx, vmInstance.ID); err != nil {
			e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored,
				fmt.Sprintf("agent registration timeout: %v", err))
			return fmt.Errorf("waiting for agent: %w", err)
		}
		e.logger.Info("agent ready", zap.String("vm_id", vmInstance.ID))

		// Step 5: Load driver
		loadCmd := &AgentCommand{
			ID:   fmt.Sprintf("cmd-load-%s", run.ID),
			Type: "load_driver",
			Parameters: map[string]interface{}{
				"ko_path":     device.DriverPath,
				"module_name": device.DriverModule,
			},
		}

		result, err := e.agentCoord.SendCommand(runCtx, vmInstance.ID, loadCmd)
		if err != nil || result.Status != "passed" {
			errMsg := "driver load failed"
			if err != nil {
				errMsg = err.Error()
			} else if result != nil {
				errMsg = result.Output
			}
			e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored, errMsg)
			return fmt.Errorf("loading driver: %s", errMsg)
		}
		e.logger.Info("driver loaded", zap.String("module", device.DriverModule))

		// Vishwa suites require a verify_device step before the actual test.
		if isVishwaSuite(run.TestSuiteID) {
			verifyCmd := &AgentCommand{
				ID:   fmt.Sprintf("cmd-verify-%s", run.ID),
				Type: "verify_device",
				Parameters: map[string]interface{}{
					"device_node": device.DeviceNode,
				},
			}
			verifyResult, verifyErr := e.agentCoord.SendCommand(runCtx, vmInstance.ID, verifyCmd)
			if verifyErr != nil || verifyResult.Status != "passed" {
				errMsg := "device verification failed"
				if verifyErr != nil {
					errMsg = verifyErr.Error()
				} else if verifyResult != nil {
					errMsg = verifyResult.Output
				}
				e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored, errMsg)
				return fmt.Errorf("verifying device: %s", errMsg)
			}
			e.logger.Info("device verified", zap.String("node", device.DeviceNode))
		}

		// Step 6: Execute tests
		// Build the start_test command — for Vishwa suites we embed the env map
		// and binary_dir so the agent can run the test with the correct environment
		// and working directory.
		var testCmd *AgentCommand
		if isVishwaSuite(run.TestSuiteID) {
			cmds := vishwaCommandSequence(run, device)
			// vishwaCommandSequence returns [load_driver, verify_device, start_test];
			// we already sent load_driver and verify_device above, so use index 2.
			testCmd = cmds[2]
		} else {
			testCmd = &AgentCommand{
				ID:   fmt.Sprintf("cmd-test-%s", run.ID),
				Type: "start_test",
				Parameters: map[string]interface{}{
					"test_suite_id": run.TestSuiteID,
					"device_id":     run.DeviceID,
				},
			}
		}

		testResult, err := e.agentCoord.SendCommand(runCtx, vmInstance.ID, testCmd)
		if err != nil {
			e.store.UpdateTestRunStatus(runCtx, run.ID, TestRunStatusErrored,
				fmt.Sprintf("test execution failed: %v", err))
			return fmt.Errorf("executing tests: %w", err)
		}

		// Step 7: Parse and store results
		if err := e.processResults(runCtx, run, testResult); err != nil {
			e.logger.Warn("failed to process results",
				zap.String("run_id", run.ID), zap.Error(err))
		}
	} else {
		// No agent coordinator — mark as passed for now (development mode)
		e.logger.Warn("no agent coordinator configured, skipping test execution",
			zap.String("run_id", run.ID))
	}

	// Step 8: Determine final status
	results, _ := e.store.GetTestResults(runCtx, run.ID)
	finalStatus := TestRunStatusPassed
	for _, r := range results {
		if r.Status == TestRunStatusFailed || r.Status == TestRunStatusErrored {
			finalStatus = TestRunStatusFailed
			break
		}
	}

	e.store.UpdateTestRunStatus(runCtx, run.ID, finalStatus, "")
	e.logger.Info("test run completed",
		zap.String("id", run.ID),
		zap.String("status", string(finalStatus)),
	)

	return nil
}

// processResults parses the agent's test output and saves individual
// test results to the store.
func (e *ExecutionEngine) processResults(ctx context.Context, run *TestRun, agentResult *AgentResult) error {
	if agentResult == nil || agentResult.Output == "" {
		return nil
	}

	// Try to parse as a JSON array of test results
	var rawResults []struct {
		Test       string             `json:"test"`
		Status     string             `json:"status"`
		DurationMs float64            `json:"duration_ms"`
		Message    string             `json:"message"`
		Metrics    map[string]float64 `json:"metrics"`
	}

	if err := json.Unmarshal([]byte(agentResult.Output), &rawResults); err != nil {
		// If not an array, store as a single result
		result := &TestResult{
			TestRunID:   run.ID,
			TestName:    run.TestSuiteID,
			Status:      mapAgentStatus(agentResult.Status),
			DurationMs:  agentResult.DurationMs,
			Message:     agentResult.Output,
			Logs:        agentResult.Logs,
			CompletedAt: time.Now().UTC(),
		}
		return e.store.SaveTestResult(ctx, result)
	}

	// Save each individual test result
	for _, raw := range rawResults {
		result := &TestResult{
			TestRunID:   run.ID,
			TestName:    raw.Test,
			Status:      mapAgentStatus(raw.Status),
			DurationMs:  int64(raw.DurationMs),
			Message:     raw.Message,
			Metrics:     raw.Metrics,
			Logs:        agentResult.Logs,
			CompletedAt: time.Now().UTC(),
		}
		if err := e.store.SaveTestResult(ctx, result); err != nil {
			e.logger.Warn("failed to save test result",
				zap.String("test", raw.Test), zap.Error(err))
		}
	}

	return nil
}

// mapAgentStatus converts an agent status string to a TestRunStatus.
func mapAgentStatus(s string) TestRunStatus {
	switch s {
	case "passed", "PASS":
		return TestRunStatusPassed
	case "failed", "FAIL":
		return TestRunStatusFailed
	default:
		return TestRunStatusErrored
	}
}

// isVishwaSuite returns true when the test suite ID starts with the "vishwa/"
// prefix, indicating it requires the Vishwa OpenCL runtime environment.
func isVishwaSuite(suiteID string) bool {
	return len(suiteID) > 7 && suiteID[:7] == "vishwa/"
}

// vishwaCommandSequence builds the ordered command slice for a Vishwa test run:
//
//  1. load_driver   — insmod the driver .ko from the 9p share
//  2. verify_device — ls the device node (orchestrator already called this
//     before invoking vishwaCommandSequence; included here for completeness
//     so callers that want the full slice can use it)
//  3. start_test    — run the binary via the Vishwa loader with the full env
//
// The caller is expected to have already dispatched commands 1 and 2 and
// should pass cmds[2] (start_test) to SendCommand.
func vishwaCommandSequence(run *TestRun, device *config.DeviceEntry) []*AgentCommand {
	// Derive the suite sub-path: "vishwa/opencl/vecadd" → "opencl/vecadd"
	// and infer the binary name as the last path segment.
	suitePath := run.TestSuiteID[7:] // strip "vishwa/"
	segments := splitPath(suitePath)
	binaryName := segments[len(segments)-1]

	testDir := device.VishwaTestDir
	if testDir == "" {
		testDir = "/mnt/share/vishwa_tests"
	}
	libDir := device.VishwaLibDir
	if libDir == "" {
		libDir = "/mnt/share/vishwa_tests/lib"
	}
	loader := device.VishwaLoader
	if loader == "" {
		loader = "/mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2"
	}

	// Construct the full binary path and the directory to cd into.
	binaryPath := testDir + "/" + suitePath + "/" + binaryName
	binaryDir := testDir + "/" + suitePath

	// Merge device-level Vishwa env with any run-level overrides.
	env := make(map[string]interface{}, len(device.VishwaEnv))
	for k, v := range device.VishwaEnv {
		env[k] = v
	}

	return []*AgentCommand{
		{
			ID:   fmt.Sprintf("cmd-load-%s", run.ID),
			Type: "load_driver",
			Parameters: map[string]interface{}{
				"ko_path":     device.DriverPath,
				"module_name": device.DriverModule,
			},
		},
		{
			ID:   fmt.Sprintf("cmd-verify-%s", run.ID),
			Type: "verify_device",
			Parameters: map[string]interface{}{
				"device_node": device.DeviceNode,
			},
		},
		{
			ID:   fmt.Sprintf("cmd-test-%s", run.ID),
			Type: "start_test",
			Parameters: map[string]interface{}{
				"binary":      binaryPath,
				"binary_dir":  binaryDir,
				"loader":      loader,
				"lib_dir":     libDir,
				"timeout":     "60",
				"env":         env,
				// suite_name is passed so the agent can label logs
				"suite_name":  run.TestSuiteID,
				"binary_name": binaryName,
			},
		},
	}
}

// splitPath splits a slash-separated path string into its segments.
func splitPath(p string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			if i > start {
				parts = append(parts, p[start:i])
			}
			start = i + 1
		}
	}
	return parts
}

// deviceSupportsQEMU returns true when the device's target_modes list
// contains "qemu". Devices with only ["fpga"] or ["hybrid"] require a
// physical PCIe passthrough via VFIO and cannot be tested using the
// standard QEMU software-emulation path.
func deviceSupportsQEMU(targetModes []string) bool {
	// If no modes are declared, assume QEMU is supported (backwards-compat).
	if len(targetModes) == 0 {
		return true
	}
	for _, m := range targetModes {
		if m == "qemu" {
			return true
		}
	}
	return false
}
