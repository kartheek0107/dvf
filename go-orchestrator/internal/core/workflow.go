// Package core provides the DAG-based workflow manager for the DVF orchestrator.
//
// A Workflow is a directed acyclic graph (DAG) of test steps. Each step declares
// zero or more upstream dependencies (DependsOn). The engine drives execution by
// calling Ready() to get steps whose dependencies have all passed, launching them
// concurrently, and calling Advance() when each step completes.
//
// Cycle detection is performed at construction time via iterative DFS.
package core

import (
	"fmt"
)

// StepStatus is the lifecycle state of a single workflow step.
type StepStatus string

const (
	StepStatusPending StepStatus = "pending"
	StepStatusRunning StepStatus = "running"
	StepStatusPassed  StepStatus = "passed"
	StepStatusFailed  StepStatus = "failed"
	StepStatusSkipped StepStatus = "skipped" // set when an upstream dep failed
)

// step is the internal representation of a node in the DAG.
type step struct {
	def      WorkflowStep
	status   StepStatus
	attempts int // how many times it has been tried
}

// Workflow is an executable DAG of test steps.
type Workflow struct {
	id    string
	runID string
	steps map[string]*step
	order []string // topological order; used to iterate deterministically
}

// NewWorkflow validates the step graph, detects cycles, and returns a ready-to-drive Workflow.
func NewWorkflow(id, runID string, defs []WorkflowStep) (*Workflow, error) {
	if len(defs) == 0 {
		return nil, fmt.Errorf("workflow %q: no steps provided", id)
	}

	w := &Workflow{
		id:    id,
		runID: runID,
		steps: make(map[string]*step, len(defs)),
	}

	// Index steps
	for i := range defs {
		d := &defs[i]
		if d.ID == "" {
			return nil, fmt.Errorf("workflow %q: step at index %d has empty ID", id, i)
		}
		if _, dup := w.steps[d.ID]; dup {
			return nil, fmt.Errorf("workflow %q: duplicate step ID %q", id, d.ID)
		}
		w.steps[d.ID] = &step{def: *d, status: StepStatusPending}
	}

	// Validate dependency references
	for sid, s := range w.steps {
		for _, dep := range s.def.DependsOn {
			if _, ok := w.steps[dep]; !ok {
				return nil, fmt.Errorf("workflow %q: step %q depends on unknown step %q", id, sid, dep)
			}
		}
	}

	// Cycle detection + topological sort (Kahn's algorithm)
	inDegree := make(map[string]int, len(w.steps))
	adj := make(map[string][]string, len(w.steps)) // dep -> [dependents]
	for sid, s := range w.steps {
		inDegree[sid] += 0 // ensure key exists
		for _, dep := range s.def.DependsOn {
			adj[dep] = append(adj[dep], sid)
			inDegree[sid]++
		}
	}

	queue := make([]string, 0, len(w.steps))
	for sid, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, sid)
		}
	}

	order := make([]string, 0, len(w.steps))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) != len(w.steps) {
		return nil, fmt.Errorf("workflow %q: cycle detected in step dependency graph", id)
	}

	w.order = order
	return w, nil
}

// ID returns the workflow identifier.
func (w *Workflow) ID() string { return w.id }

// RunID returns the associated test run ID.
func (w *Workflow) RunID() string { return w.runID }

// Ready returns the IDs of steps that are eligible to run right now:
// all their dependencies have passed and they are still in pending state.
func (w *Workflow) Ready() []string {
	var ready []string
	for _, sid := range w.order {
		s := w.steps[sid]
		if s.status != StepStatusPending {
			continue
		}
		if w.depsAllPassed(s.def.DependsOn) {
			ready = append(ready, sid)
		}
	}
	return ready
}

// depsAllPassed returns true if every step in the dependency list has passed.
func (w *Workflow) depsAllPassed(deps []string) bool {
	for _, dep := range deps {
		if w.steps[dep].status != StepStatusPassed {
			return false
		}
	}
	return true
}

// depsAnyFailed returns true if any step in the dependency list has failed.
func (w *Workflow) depsAnyFailed(deps []string) bool {
	for _, dep := range deps {
		st := w.steps[dep].status
		if st == StepStatusFailed || st == StepStatusSkipped {
			return true
		}
	}
	return false
}

// Advance records the outcome of a step. On failure it skips all
// downstream-only-reachable dependents. Returns the updated status.
func (w *Workflow) Advance(stepID string, status StepStatus) error {
	s, ok := w.steps[stepID]
	if !ok {
		return fmt.Errorf("workflow %q: unknown step %q", w.id, stepID)
	}
	s.status = status

	if status == StepStatusFailed {
		// Propagate skips to all transitively dependent steps
		w.propagateSkip(stepID)
	}
	return nil
}

// MarkRunning marks a step as currently executing.
func (w *Workflow) MarkRunning(stepID string) error {
	s, ok := w.steps[stepID]
	if !ok {
		return fmt.Errorf("workflow %q: unknown step %q", w.id, stepID)
	}
	s.status = StepStatusRunning
	s.attempts++
	return nil
}

// propagateSkip transitively marks all steps that can only be reached via
// failed/skipped dependencies as skipped.
func (w *Workflow) propagateSkip(fromID string) {
	for _, sid := range w.order {
		s := w.steps[sid]
		if s.status == StepStatusPending && w.depsAnyFailed(s.def.DependsOn) {
			s.status = StepStatusSkipped
		}
	}
}

// StepDef returns the definition of a step (read-only copy).
func (w *Workflow) StepDef(stepID string) (WorkflowStep, bool) {
	s, ok := w.steps[stepID]
	if !ok {
		return WorkflowStep{}, false
	}
	return s.def, true
}

// StepStatus returns the current status of a step.
func (w *Workflow) StepStatus(stepID string) (StepStatus, bool) {
	s, ok := w.steps[stepID]
	if !ok {
		return "", false
	}
	return s.status, true
}

// Attempts returns how many times a step has been tried.
func (w *Workflow) Attempts(stepID string) int {
	s, ok := w.steps[stepID]
	if !ok {
		return 0
	}
	return s.attempts
}

// IsDone returns true when there are no more steps in pending or running state.
func (w *Workflow) IsDone() bool {
	for _, s := range w.steps {
		if s.status == StepStatusPending || s.status == StepStatusRunning {
			return false
		}
	}
	return true
}

// IsBlocked returns true when no step is running or ready, but the workflow is not done.
// This happens when a root-level step failed and all dependents are now skipped.
func (w *Workflow) IsBlocked() bool {
	if w.IsDone() {
		return false
	}
	return len(w.Ready()) == 0
}

// Summary returns passed, failed, skipped counts.
func (w *Workflow) Summary() (passed, failed, skipped int) {
	for _, s := range w.steps {
		switch s.status {
		case StepStatusPassed:
			passed++
		case StepStatusFailed:
			failed++
		case StepStatusSkipped:
			skipped++
		}
	}
	return
}
