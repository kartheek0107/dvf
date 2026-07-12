// Package storage defines interfaces for all storage backends.
package storage

import (
	"context"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
)

// TestRunStore defines persistence operations for test runs.
type TestRunStore interface {
	// CreateTestRun persists a new test run and returns it with a generated ID.
	CreateTestRun(ctx context.Context, run *core.TestRun) (*core.TestRun, error)

	// GetTestRun retrieves a test run by ID.
	GetTestRun(ctx context.Context, id string) (*core.TestRun, error)

	// UpdateTestRunStatus updates the status (and optionally error message) of a test run.
	UpdateTestRunStatus(ctx context.Context, id string, status core.TestRunStatus, errMsg string) error

	// ListTestRuns returns test runs matching the given filters.
	ListTestRuns(ctx context.Context, req *core.ListTestRunsRequest) ([]*core.TestRun, error)

	// ListRunningTestRuns returns all test runs in RUNNING, QUEUED, or PENDING state.
	// Used on startup to reconcile in-flight work after a crash.
	ListRunningTestRuns(ctx context.Context) ([]*core.TestRun, error)
}

// TestResultStore defines persistence operations for individual test results.
type TestResultStore interface {
	// SaveTestResult persists a test result.
	SaveTestResult(ctx context.Context, result *core.TestResult) error

	// GetTestResults retrieves all results for a given test run.
	GetTestResults(ctx context.Context, testRunID string) ([]*core.TestResult, error)
}

// VMStore defines persistence operations for VM instance tracking.
type VMStore interface {
	// SaveVM persists a VM instance record.
	SaveVM(ctx context.Context, vm *core.VMInstance) error

	// GetVM retrieves a VM by ID.
	GetVM(ctx context.Context, id string) (*core.VMInstance, error)

	// UpdateVMStatus updates the status of a VM.
	UpdateVMStatus(ctx context.Context, id string, status core.VMStatus) error

	// ListVMs returns VMs matching the given filters.
	ListVMs(ctx context.Context, req *core.ListVMsRequest) ([]*core.VMInstance, error)

	// ListOrphanedVMs returns VMs in transient states (CREATING, BOOTING, RUNNING_TEST)
	// that were never properly torn down — typically left over after a crash.
	ListOrphanedVMs(ctx context.Context) ([]*core.VMInstance, error)

	// DeleteVM removes a VM record.
	DeleteVM(ctx context.Context, id string) error
}

// Store is the aggregate interface combining all storage backends.
// Implementations may back this with Postgres, in-memory, or both.
type Store interface {
	TestRunStore
	TestResultStore
	VMStore

	// Ping checks that the storage backend is reachable.
	Ping(ctx context.Context) error

	// Close releases any resources held by the store.
	Close() error
}
