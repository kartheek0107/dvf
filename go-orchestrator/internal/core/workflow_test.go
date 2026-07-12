package core

import (
	"fmt"
	"testing"
)

// ─── helpers ───────────────────────────────────────────────────────────────

func mkStep(id string, deps ...string) WorkflowStep {
	return WorkflowStep{
		ID:        id,
		DependsOn: deps,
	}
}

func mustWorkflow(t *testing.T, steps []WorkflowStep) *Workflow {
	t.Helper()
	w, err := NewWorkflow("wf-test", "run-1", steps)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	return w
}

// ─── construction tests ────────────────────────────────────────────────────

func TestWorkflow_EmptySteps(t *testing.T) {
	_, err := NewWorkflow("id", "run", []WorkflowStep{})
	if err == nil {
		t.Fatal("expected error for empty steps")
	}
}

func TestWorkflow_DuplicateID(t *testing.T) {
	_, err := NewWorkflow("id", "run", []WorkflowStep{
		{ID: "a"}, {ID: "a"},
	})
	if err == nil {
		t.Fatal("expected error for duplicate step IDs")
	}
}

func TestWorkflow_UnknownDep(t *testing.T) {
	_, err := NewWorkflow("id", "run", []WorkflowStep{
		mkStep("a", "nonexistent"),
	})
	if err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestWorkflow_CycleDetected(t *testing.T) {
	_, err := NewWorkflow("id", "run", []WorkflowStep{
		mkStep("a", "b"),
		mkStep("b", "c"),
		mkStep("c", "a"), // cycle
	})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestWorkflow_Valid(t *testing.T) {
	// a → b → c (linear chain)
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("a"),
		mkStep("b", "a"),
		mkStep("c", "b"),
	})
	if w == nil {
		t.Fatal("expected valid workflow")
	}
}

// ─── execution flow tests ──────────────────────────────────────────────────

func TestWorkflow_InitialReady(t *testing.T) {
	// a and d have no deps; b depends on a; c depends on b+d
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("a"),
		mkStep("b", "a"),
		mkStep("c", "b", "d"),
		mkStep("d"),
	})

	ready := w.Ready()
	readyMap := map[string]bool{}
	for _, id := range ready {
		readyMap[id] = true
	}

	if !readyMap["a"] || !readyMap["d"] {
		t.Errorf("expected a and d to be initially ready, got %v", ready)
	}
	if readyMap["b"] || readyMap["c"] {
		t.Errorf("b and c should not be ready yet, got %v", ready)
	}
}

func TestWorkflow_LinearChain(t *testing.T) {
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("a"),
		mkStep("b", "a"),
		mkStep("c", "b"),
	})

	// Drive: a passes → b becomes ready → b passes → c becomes ready → c passes
	steps := []string{"a", "b", "c"}
	for _, sid := range steps {
		ready := w.Ready()
		found := false
		for _, r := range ready {
			if r == sid {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %q to be ready, ready=%v", sid, ready)
		}
		if err := w.MarkRunning(sid); err != nil {
			t.Fatal(err)
		}
		if err := w.Advance(sid, StepStatusPassed); err != nil {
			t.Fatal(err)
		}
	}

	if !w.IsDone() {
		t.Error("expected workflow to be done")
	}
	p, f, sk := w.Summary()
	if p != 3 || f != 0 || sk != 0 {
		t.Errorf("expected 3/0/0, got %d/%d/%d", p, f, sk)
	}
}

func TestWorkflow_FailureCascadesSkip(t *testing.T) {
	// a → b → c
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("a"),
		mkStep("b", "a"),
		mkStep("c", "b"),
	})

	if err := w.MarkRunning("a"); err != nil {
		t.Fatal(err)
	}
	if err := w.Advance("a", StepStatusFailed); err != nil {
		t.Fatal(err)
	}

	// b and c should be skipped; workflow done
	if !w.IsDone() {
		t.Error("expected workflow to be done after cascade")
	}
	p, f, sk := w.Summary()
	if p != 0 || f != 1 || sk != 2 {
		t.Errorf("expected 0/1/2, got %d/%d/%d", p, f, sk)
	}
}

func TestWorkflow_ParallelFanOut(t *testing.T) {
	// root → [b, c, d] → sink (all must pass for sink to be ready)
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("root"),
		mkStep("b", "root"),
		mkStep("c", "root"),
		mkStep("d", "root"),
		mkStep("sink", "b", "c", "d"),
	})

	// Pass root
	_ = w.MarkRunning("root")
	_ = w.Advance("root", StepStatusPassed)

	ready := w.Ready()
	if len(ready) != 3 {
		t.Fatalf("expected 3 parallel steps ready, got %v", ready)
	}

	// Pass b, c, d
	for _, sid := range []string{"b", "c", "d"} {
		_ = w.MarkRunning(sid)
		_ = w.Advance(sid, StepStatusPassed)
	}

	// sink should now be ready
	ready = w.Ready()
	if len(ready) != 1 || ready[0] != "sink" {
		t.Fatalf("expected only sink ready, got %v", ready)
	}

	_ = w.MarkRunning("sink")
	_ = w.Advance("sink", StepStatusPassed)

	if !w.IsDone() {
		t.Error("expected workflow done")
	}
}

func TestWorkflow_IsBlocked(t *testing.T) {
	// a → b; if a fails, b is skipped and workflow is "blocked" before IsDone
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("a"),
		mkStep("b", "a"),
	})

	_ = w.MarkRunning("a")
	_ = w.Advance("a", StepStatusFailed)

	// After cascade: b is skipped so both terminal. Done should be true, not blocked.
	if w.IsBlocked() {
		t.Error("IsBlocked should be false when all skipped (IsDone=true)")
	}
	if !w.IsDone() {
		t.Error("expected IsDone true")
	}
}

func TestWorkflow_Attempts(t *testing.T) {
	w := mustWorkflow(t, []WorkflowStep{
		{ID: "a", RetryMax: 2},
	})

	_ = w.MarkRunning("a")
	_ = w.Advance("a", StepStatusFailed)

	// Re-set to pending for retry simulation
	w.steps["a"].status = StepStatusPending

	_ = w.MarkRunning("a")
	attempts := w.Attempts("a")
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestWorkflow_StepDefAndStatus(t *testing.T) {
	w := mustWorkflow(t, []WorkflowStep{
		{ID: "x", TestBinary: "/bin/test", TimeoutSec: 30},
	})

	def, ok := w.StepDef("x")
	if !ok || def.TestBinary != "/bin/test" {
		t.Errorf("unexpected def: %+v", def)
	}

	status, ok := w.StepStatus("x")
	if !ok || status != StepStatusPending {
		t.Errorf("expected pending, got %v", status)
	}

	_, ok = w.StepDef("missing")
	if ok {
		t.Error("expected false for missing step")
	}
}

func TestWorkflow_DiamondDependency(t *testing.T) {
	// Classic diamond: root → [L, R] → sink
	// sink must wait for BOTH L and R
	w := mustWorkflow(t, []WorkflowStep{
		mkStep("root"),
		mkStep("L", "root"),
		mkStep("R", "root"),
		mkStep("sink", "L", "R"),
	})

	_ = w.MarkRunning("root")
	_ = w.Advance("root", StepStatusPassed)

	// Pass L but not R yet
	_ = w.MarkRunning("L")
	_ = w.Advance("L", StepStatusPassed)

	ready := w.Ready()
	for _, r := range ready {
		if r == "sink" {
			t.Fatal("sink should not be ready until R also passes")
		}
	}

	_ = w.MarkRunning("R")
	_ = w.Advance("R", StepStatusPassed)

	ready = w.Ready()
	if len(ready) != 1 || ready[0] != "sink" {
		t.Fatalf("expected sink ready, got %v", ready)
	}
}

// Ensure unused import (fmt) doesn't break test build.
var _ = fmt.Sprintf
