package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
)

// newTestStore returns a Store for testing.
//
// If DVF_TEST_POSTGRES_DSN is set, it uses a real Postgres instance.
// Otherwise it uses the in-memory store so unit tests always pass without
// any infrastructure.
func newTestStore(t *testing.T) storage.Store {
	t.Helper()
	dsn := os.Getenv("DVF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Log("DVF_TEST_POSTGRES_DSN not set — using in-memory store")
		return storage.NewMemoryStore()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pg, err := storage.NewPostgresStore(ctx, dsn, 5)
	if err != nil {
		t.Fatalf("failed to connect to postgres (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	return pg
}

// ─── TestRun CRUD ─────────────────────────────────────────────────────────────

func TestCreateAndGetTestRun(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	run := &core.TestRun{
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke/register_rw",
		Priority:    1,
		RequestedBy: "ci",
		Tags:        []string{"smoke", "ci"},
	}

	created, err := store.CreateTestRun(ctx, run)
	if err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if created.Status != core.TestRunStatusPending {
		t.Fatalf("expected PENDING, got %s", created.Status)
	}

	got, err := store.GetTestRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTestRun: %v", err)
	}
	if got.DeviceID != "gpgpu" {
		t.Errorf("DeviceID: got %q, want %q", got.DeviceID, "gpgpu")
	}
	if got.Priority != 1 {
		t.Errorf("Priority: got %d, want 1", got.Priority)
	}
}

func TestUpdateTestRunStatus(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	run, _ := store.CreateTestRun(ctx, &core.TestRun{
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
	})

	// → RUNNING
	if err := store.UpdateTestRunStatus(ctx, run.ID, core.TestRunStatusRunning, ""); err != nil {
		t.Fatalf("UpdateTestRunStatus RUNNING: %v", err)
	}
	got, _ := store.GetTestRun(ctx, run.ID)
	if got.Status != core.TestRunStatusRunning {
		t.Errorf("expected RUNNING, got %s", got.Status)
	}
	if got.StartedAt == nil {
		t.Error("expected StartedAt to be set")
	}

	// → PASSED
	if err := store.UpdateTestRunStatus(ctx, run.ID, core.TestRunStatusPassed, ""); err != nil {
		t.Fatalf("UpdateTestRunStatus PASSED: %v", err)
	}
	got, _ = store.GetTestRun(ctx, run.ID)
	if got.Status != core.TestRunStatusPassed {
		t.Errorf("expected PASSED, got %s", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestListTestRuns(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create two test runs
	for _, suite := range []string{"smoke", "regression"} {
		_, err := store.CreateTestRun(ctx, &core.TestRun{
			DeviceID:    "fpga",
			TestSuiteID: suite,
		})
		if err != nil {
			t.Fatalf("CreateTestRun: %v", err)
		}
	}

	runs, err := store.ListTestRuns(ctx, &core.ListTestRunsRequest{DeviceID: "fpga"})
	if err != nil {
		t.Fatalf("ListTestRuns: %v", err)
	}
	if len(runs) < 2 {
		t.Errorf("expected >= 2 runs for fpga, got %d", len(runs))
	}
}

// ─── TestResult CRUD ──────────────────────────────────────────────────────────

func TestSaveAndGetTestResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	run, _ := store.CreateTestRun(ctx, &core.TestRun{
		DeviceID:    "gpgpu",
		TestSuiteID: "smoke",
	})

	result := &core.TestResult{
		TestRunID:   run.ID,
		TestName:    "test_register_rw",
		Category:    core.TestCategoryReadWrite,
		Status:      core.TestRunStatusPassed,
		DurationMs:  42,
		Message:     "all registers verified",
		Metrics:     map[string]float64{"ops_per_sec": 12500.0},
		CompletedAt: time.Now().UTC(),
	}

	if err := store.SaveTestResult(ctx, result); err != nil {
		t.Fatalf("SaveTestResult: %v", err)
	}

	results, err := store.GetTestResults(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetTestResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TestName != "test_register_rw" {
		t.Errorf("TestName: got %q", results[0].TestName)
	}
	if v, ok := results[0].Metrics["ops_per_sec"]; !ok || v != 12500.0 {
		t.Errorf("Metrics['ops_per_sec']: got %v", results[0].Metrics)
	}
}

// ─── VM CRUD ─────────────────────────────────────────────────────────────────

func TestSaveGetUpdateVM(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	vm := &core.VMInstance{
		ID:            "vm-test-001",
		Status:        core.VMStatusCreating,
		DeviceID:      "gpgpu",
		QMPSocketPath: "/tmp/dvf/qmp/vm-test-001.sock",
		SerialPorts:   []string{"/tmp/dvf/agent/vm-test-001.sock"},
		AllocatedCPUs: 2,
		AllocatedMemMB: 1024,
		ImagePath:     "/var/lib/dvf/images/base.ext4",
		AgentStatus:   core.AgentStatusUnknown,
		CreatedAt:     time.Now().UTC(),
	}

	if err := store.SaveVM(ctx, vm); err != nil {
		t.Fatalf("SaveVM: %v", err)
	}

	got, err := store.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if got.QMPSocketPath != vm.QMPSocketPath {
		t.Errorf("QMPSocketPath: got %q, want %q", got.QMPSocketPath, vm.QMPSocketPath)
	}

	if err := store.UpdateVMStatus(ctx, vm.ID, core.VMStatusReady); err != nil {
		t.Fatalf("UpdateVMStatus: %v", err)
	}
	got, _ = store.GetVM(ctx, vm.ID)
	if got.Status != core.VMStatusReady {
		t.Errorf("Status: got %q, want READY", got.Status)
	}

	if err := store.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := store.GetVM(ctx, vm.ID); err == nil {
		t.Fatal("expected error after DeleteVM, got nil")
	}
}

// ─── Ping ─────────────────────────────────────────────────────────────────────

func TestPing(t *testing.T) {
	store := newTestStore(t)
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
