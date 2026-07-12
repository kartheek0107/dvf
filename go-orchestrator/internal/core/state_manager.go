// Package core provides crash-recovery state reconciliation for the DVF orchestrator.
//
// On startup, before engine.Start(), call RecoverState to reconcile the
// persistent Postgres state with the in-memory Scheduler:
//
//   - Any test run in RUNNING or QUEUED state is marked ERRORED with an
//     "orchestrator restarted" message, then re-enqueued as PENDING.
//   - Any test run already PENDING is re-enqueued into the Scheduler so it
//     is not lost if the process crashed before dispatching it.
//   - Orphaned VMs (CREATING, BOOTING, RUNNING_TEST) are marked ERROR in
//     Postgres. If the VM's PID is still alive, it is SIGKILLed so the
//     QEMU process doesn't outlive the orchestrator.
package core

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"go.uber.org/zap"
)

// RecoveryStore is the subset of the storage interface required by RecoverState.
type RecoveryStore interface {
	ListRunningTestRuns(ctx context.Context) ([]*TestRun, error)
	UpdateTestRunStatus(ctx context.Context, id string, status TestRunStatus, errMsg string) error
	ListOrphanedVMs(ctx context.Context) ([]*VMInstance, error)
	UpdateVMStatus(ctx context.Context, id string, status VMStatus) error
}

// RecoverState reconciles in-flight state from Postgres back into the scheduler.
//
// Call this once after the store and scheduler are initialised, before
// calling engine.Start(). It is idempotent: calling it multiple times is safe.
func RecoverState(ctx context.Context, store RecoveryStore, scheduler *Scheduler, logger *zap.Logger) error {
	log := logger.Named("state_recovery")
	log.Info("starting state recovery")

	// ── 1. Recover in-flight test runs ────────────────────────────────────────

	runs, err := store.ListRunningTestRuns(ctx)
	if err != nil {
		return fmt.Errorf("listing running test runs for recovery: %w", err)
	}

	var recovered, requeued int

	for _, run := range runs {
		switch run.Status {
		case TestRunStatusRunning, TestRunStatusQueued:
			// These were mid-execution when the orchestrator crashed.
			// Mark as ERRORED, then re-enqueue as PENDING so they get a fresh attempt.
			if err := store.UpdateTestRunStatus(ctx, run.ID, TestRunStatusErrored,
				"orchestrator restarted — re-queued for retry"); err != nil {
				log.Warn("failed to mark run errored", zap.String("run_id", run.ID), zap.Error(err))
				continue
			}
			// Reset in-memory state for re-dispatch
			run.Status = TestRunStatusPending
			run.VMID = ""
			scheduler.Enqueue(run)
			recovered++
			log.Info("recovered crashed run — re-enqueued",
				zap.String("run_id", run.ID),
				zap.String("device_id", run.DeviceID),
			)

		case TestRunStatusPending:
			// Process restarted before the run was picked up. Just re-enqueue.
			scheduler.Enqueue(run)
			requeued++
			log.Info("re-enqueued pending run",
				zap.String("run_id", run.ID),
			)
		}
	}

	// ── 2. Clean up orphaned VMs ──────────────────────────────────────────────

	orphans, err := store.ListOrphanedVMs(ctx)
	if err != nil {
		return fmt.Errorf("listing orphaned VMs for recovery: %w", err)
	}

	var vmsCleaned int

	for _, vm := range orphans {
		// Try to kill the QEMU process if PID is known
		if vm.PID > 0 {
			if proc, pErr := os.FindProcess(vm.PID); pErr == nil {
				if kErr := proc.Signal(syscall.SIGKILL); kErr != nil {
					log.Warn("could not kill orphaned VM process",
						zap.String("vm_id", vm.ID),
						zap.Int("pid", vm.PID),
						zap.Error(kErr),
					)
				} else {
					log.Info("killed orphaned VM process",
						zap.String("vm_id", vm.ID),
						zap.Int("pid", vm.PID),
					)
				}
			}
		}

		if err := store.UpdateVMStatus(ctx, vm.ID, VMStatusError); err != nil {
			log.Warn("failed to mark orphaned VM as error",
				zap.String("vm_id", vm.ID),
				zap.Error(err),
			)
			continue
		}
		vmsCleaned++
		log.Info("marked orphaned VM as error",
			zap.String("vm_id", vm.ID),
			zap.String("device_id", vm.DeviceID),
			zap.String("old_status", string(vm.Status)),
		)
	}

	log.Info("state recovery complete",
		zap.Int("runs_recovered", recovered),
		zap.Int("runs_requeued", requeued),
		zap.Int("vms_cleaned", vmsCleaned),
	)
	return nil
}
