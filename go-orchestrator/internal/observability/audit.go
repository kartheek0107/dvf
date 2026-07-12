// Package observability provides structured audit logging for the DVF orchestrator.
//
// Audit events are emitted as structured Zap log lines tagged with
// "audit":true so they can be filtered, shipped to a SIEM, or queried with:
//
//	cat dvf.log | jq 'select(.audit == true)'
package observability

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// AuditLogger writes structured, append-only audit records.
// All methods are safe to call concurrently.
type AuditLogger struct {
	logger *zap.Logger
}

// NewAuditLogger creates an AuditLogger backed by the given Zap logger.
// The logger should write to a persistent sink (file or log aggregator) in
// production. For development, any Zap logger works — the records are
// distinguishable by the "audit":true field.
func NewAuditLogger(logger *zap.Logger) *AuditLogger {
	return &AuditLogger{
		logger: logger.Named("audit").With(zap.Bool("audit", true)),
	}
}

// LogSubmit records that a test run was submitted.
func (a *AuditLogger) LogSubmit(
	_ context.Context,
	runID, deviceID, suiteID, requestedBy string,
	tags []string,
) {
	a.logger.Info("test_run.submitted",
		zap.String("run_id", runID),
		zap.String("device_id", deviceID),
		zap.String("test_suite_id", suiteID),
		zap.String("requested_by", requestedBy),
		zap.Strings("tags", tags),
		zap.Int64("ts_ms", time.Now().UnixMilli()),
	)
}

// LogCancel records that a test run was cancelled.
func (a *AuditLogger) LogCancel(
	_ context.Context,
	runID, requestedBy string,
) {
	a.logger.Info("test_run.cancelled",
		zap.String("run_id", runID),
		zap.String("requested_by", requestedBy),
		zap.Int64("ts_ms", time.Now().UnixMilli()),
	)
}

// LogComplete records that a test run reached a terminal state.
func (a *AuditLogger) LogComplete(
	_ context.Context,
	runID, status string,
	durationMs int64,
) {
	a.logger.Info("test_run.completed",
		zap.String("run_id", runID),
		zap.String("final_status", status),
		zap.Int64("duration_ms", durationMs),
		zap.Int64("ts_ms", time.Now().UnixMilli()),
	)
}

// LogVMEvent records a VM lifecycle event.
func (a *AuditLogger) LogVMEvent(
	_ context.Context,
	event, vmID, deviceID, testRunID string,
) {
	a.logger.Info(event,
		zap.String("vm_id", vmID),
		zap.String("device_id", deviceID),
		zap.String("test_run_id", testRunID),
		zap.Int64("ts_ms", time.Now().UnixMilli()),
	)
}
