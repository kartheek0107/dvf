package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
)

// MemoryStore is a thread-safe in-memory implementation of Store.
// Useful for development, testing, and when Postgres is not available.
type MemoryStore struct {
	mu          sync.RWMutex
	testRuns    map[string]*core.TestRun
	testResults map[string][]*core.TestResult // key: testRunID
	vms         map[string]*core.VMInstance
	counter     int64
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		testRuns:    make(map[string]*core.TestRun),
		testResults: make(map[string][]*core.TestResult),
		vms:         make(map[string]*core.VMInstance),
	}
}

func (m *MemoryStore) nextID(prefix string) string {
	m.counter++
	return fmt.Sprintf("%s-%d", prefix, m.counter)
}

// --- TestRunStore ---

func (m *MemoryStore) CreateTestRun(_ context.Context, run *core.TestRun) (*core.TestRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	run.ID = m.nextID("tr")
	run.CreatedAt = time.Now().UTC()
	if run.Status == "" {
		run.Status = core.TestRunStatusPending
	}
	// Store a copy
	stored := *run
	m.testRuns[run.ID] = &stored
	return run, nil
}

func (m *MemoryStore) GetTestRun(_ context.Context, id string) (*core.TestRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.testRuns[id]
	if !ok {
		return nil, fmt.Errorf("test run %q not found", id)
	}
	result := *run
	return &result, nil
}

func (m *MemoryStore) UpdateTestRunStatus(_ context.Context, id string, status core.TestRunStatus, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.testRuns[id]
	if !ok {
		return fmt.Errorf("test run %q not found", id)
	}
	run.Status = status
	if errMsg != "" {
		run.ErrorMessage = errMsg
	}
	now := time.Now().UTC()
	switch status {
	case core.TestRunStatusRunning:
		run.StartedAt = &now
	case core.TestRunStatusPassed, core.TestRunStatusFailed, core.TestRunStatusErrored,
		core.TestRunStatusCancelled, core.TestRunStatusTimeout:
		run.CompletedAt = &now
		if run.StartedAt != nil {
			run.DurationMs = now.Sub(*run.StartedAt).Milliseconds()
		}
	}
	return nil
}

func (m *MemoryStore) ListTestRuns(_ context.Context, req *core.ListTestRunsRequest) ([]*core.TestRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*core.TestRun
	for _, run := range m.testRuns {
		if req.DeviceID != "" && run.DeviceID != req.DeviceID {
			continue
		}
		if req.Status != "" && run.Status != req.Status {
			continue
		}
		cp := *run
		results = append(results, &cp)
	}

	// Apply limit/offset
	if req.Offset > 0 && req.Offset < len(results) {
		results = results[req.Offset:]
	}
	if req.Limit > 0 && req.Limit < len(results) {
		results = results[:req.Limit]
	}

	return results, nil
}

// --- TestResultStore ---

func (m *MemoryStore) SaveTestResult(_ context.Context, result *core.TestResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if result.ID == "" {
		result.ID = m.nextID("tres")
	}
	cp := *result
	m.testResults[result.TestRunID] = append(m.testResults[result.TestRunID], &cp)
	return nil
}

func (m *MemoryStore) GetTestResults(_ context.Context, testRunID string) ([]*core.TestResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := m.testResults[testRunID]
	out := make([]*core.TestResult, len(results))
	for i, r := range results {
		cp := *r
		out[i] = &cp
	}
	return out, nil
}

// --- VMStore ---

func (m *MemoryStore) SaveVM(_ context.Context, vm *core.VMInstance) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if vm.ID == "" {
		vm.ID = m.nextID("vm")
	}
	cp := *vm
	m.vms[vm.ID] = &cp
	return nil
}

func (m *MemoryStore) GetVM(_ context.Context, id string) (*core.VMInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vm, ok := m.vms[id]
	if !ok {
		return nil, fmt.Errorf("vm %q not found", id)
	}
	cp := *vm
	return &cp, nil
}

func (m *MemoryStore) UpdateVMStatus(_ context.Context, id string, status core.VMStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, ok := m.vms[id]
	if !ok {
		return fmt.Errorf("vm %q not found", id)
	}
	vm.Status = status
	return nil
}

func (m *MemoryStore) ListVMs(_ context.Context, req *core.ListVMsRequest) ([]*core.VMInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*core.VMInstance
	for _, vm := range m.vms {
		if req.Status != "" && vm.Status != req.Status {
			continue
		}
		if req.DeviceID != "" && vm.DeviceID != req.DeviceID {
			continue
		}
		cp := *vm
		results = append(results, &cp)
	}
	return results, nil
}

func (m *MemoryStore) DeleteVM(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.vms[id]; !ok {
		return fmt.Errorf("vm %q not found", id)
	}
	delete(m.vms, id)
	return nil
}

// --- Utility ---

func (m *MemoryStore) Ping(_ context.Context) error {
	return nil // always available
}

func (m *MemoryStore) Close() error {
	return nil
}
