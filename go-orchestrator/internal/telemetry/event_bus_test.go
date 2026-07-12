package telemetry

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
)

func TestEventBusGracefulDegradation(t *testing.T) {
	// Provide unreachable Redis configuration to test graceful degradation.
	cfg := config.RedisConfig{
		Host:     "127.0.0.1",
		Port:     9999, // Unreachable port
		Password: "",
		DB:       0,
		PoolSize: 5,
	}

	logger := zap.NewNop()
	bus, err := NewEventBus(cfg, logger)
	if err != nil {
		t.Fatalf("NewEventBus should not return error on unreachable Redis; it should degrade gracefully: %v", err)
	}

	if bus.Enabled() {
		t.Errorf("expected event bus to be disabled when Redis is unreachable")
	}

	// Try publishing — should not panic/error, but degrade silently.
	ctx := context.Background()
	err = bus.Publish(ctx, Event{
		Type:     EventTestRunSubmitted,
		EntityID: "test-run-123",
		DeviceID: "device-456",
		Status:   "pending",
	})
	if err != nil {
		t.Errorf("Publish on disabled bus returned error: %v", err)
	}

	// Ping should report error
	if err := bus.Ping(ctx); err == nil {
		t.Errorf("expected Ping to return error on disabled bus")
	}

	// Close should be safe
	if err := bus.Close(); err != nil {
		t.Errorf("expected Close to succeed, got: %v", err)
	}
}
