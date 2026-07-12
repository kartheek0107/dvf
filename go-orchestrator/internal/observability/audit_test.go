package observability

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestAuditLogger(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	audit := NewAuditLogger(logger)

	ctx := context.Background()

	t.Run("LogSubmit", func(t *testing.T) {
		audit.LogSubmit(ctx, "run-1", "dev-1", "suite-1", "user-1", []string{"tag-1"})

		allLogs := logs.All()
		if len(allLogs) != 1 {
			t.Fatalf("expected 1 log entry, got %d", len(allLogs))
		}
		entry := allLogs[0]
		if entry.Message != "test_run.submitted" {
			t.Errorf("expected msg 'test_run.submitted', got %q", entry.Message)
		}

		// Verify logger name (should be 'audit')
		if entry.LoggerName != "audit" {
			t.Errorf("expected logger name 'audit', got %q", entry.LoggerName)
		}

		// Verify fields
		fields := entry.ContextMap()
		if fields["audit"] != true {
			t.Errorf("expected field 'audit' to be true, got %v", fields["audit"])
		}
		if fields["run_id"] != "run-1" {
			t.Errorf("expected run_id 'run-1', got %v", fields["run_id"])
		}
		if fields["device_id"] != "dev-1" {
			t.Errorf("expected device_id 'dev-1', got %v", fields["device_id"])
		}
	})

	t.Run("LogCancel", func(t *testing.T) {
		_ = logs.TakeAll() // clear previous logs

		audit.LogCancel(ctx, "run-2", "user-1")

		allLogs := logs.All()
		if len(allLogs) != 1 {
			t.Fatalf("expected 1 log entry, got %d", len(allLogs))
		}
		entry := allLogs[0]
		if entry.Message != "test_run.cancelled" {
			t.Errorf("expected msg 'test_run.cancelled', got %q", entry.Message)
		}

		fields := entry.ContextMap()
		if fields["run_id"] != "run-2" {
			t.Errorf("expected run_id 'run-2', got %v", fields["run_id"])
		}
	})

	t.Run("LogComplete", func(t *testing.T) {
		_ = logs.TakeAll() // clear previous logs

		audit.LogComplete(ctx, "run-3", "passed", 5678)

		allLogs := logs.All()
		if len(allLogs) != 1 {
			t.Fatalf("expected 1 log entry, got %d", len(allLogs))
		}
		entry := allLogs[0]
		if entry.Message != "test_run.completed" {
			t.Errorf("expected msg 'test_run.completed', got %q", entry.Message)
		}

		fields := entry.ContextMap()
		if fields["run_id"] != "run-3" {
			t.Errorf("expected run_id 'run-3', got %v", fields["run_id"])
		}
		// Duration values are read as float64 or int64 in map
		duration, ok := fields["duration_ms"]
		if !ok {
			t.Fatalf("duration_ms field not found")
		}
		// Depending on json/zap representation it could be int64 or float64. In ContextMap it is preserved as is.
		if dVal, ok := duration.(int64); ok && dVal != 5678 {
			t.Errorf("expected duration 5678, got %d", dVal)
		}
	})
}
