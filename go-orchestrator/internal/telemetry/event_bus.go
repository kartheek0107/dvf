// Package telemetry provides the Redis Streams event bus for DVF lifecycle events.
//
// Events are published as XADD entries to the "dvf:events" stream.
// Each event carries: event_type, entity_id, device_id, status, ts, payload.
//
// The EventBus degrades gracefully: if Redis is unreachable at startup or at
// publish time, events are silently dropped (with a warn log) so the
// orchestrator keeps running without Redis.
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
)

const (
	// StreamKey is the Redis Stream key for all DVF lifecycle events.
	StreamKey = "dvf:events"

	// MaxStreamLen is the maximum number of entries retained in the stream.
	// Older entries are trimmed by Redis (~MAXLEN). Approx 7 days at 1 event/min.
	MaxStreamLen = 10000
)

// EventType is a lifecycle event identifier.
type EventType string

const (
	EventTestRunSubmitted  EventType = "test_run.submitted"
	EventTestRunStarted    EventType = "test_run.started"
	EventTestRunCompleted  EventType = "test_run.completed"
	EventTestRunCancelled  EventType = "test_run.cancelled"
	EventVMCreated         EventType = "vm.created"
	EventVMReady           EventType = "vm.ready"
	EventVMDestroyed       EventType = "vm.destroyed"
	EventDriverLoaded      EventType = "driver.loaded"
	EventTestResultSaved   EventType = "test_result.saved"
)

// Event is a single lifecycle event published to the stream.
type Event struct {
	Type     EventType              `json:"event_type"`
	EntityID string                 `json:"entity_id"`   // test_run_id or vm_id
	DeviceID string                 `json:"device_id"`
	Status   string                 `json:"status,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

// EventBus publishes DVF lifecycle events to a Redis Stream.
type EventBus struct {
	client  *redis.Client
	logger  *zap.Logger
	enabled bool // false when Redis is unavailable
}

// NewEventBus creates a Redis-backed EventBus.
// If Redis is unreachable, it returns a disabled (no-op) bus with a warning.
func NewEventBus(cfg config.RedisConfig, logger *zap.Logger) (*EventBus, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})

	// Probe Redis with a short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		logger.Warn("redis unreachable — event bus disabled (events will be dropped)",
			zap.String("addr", addr),
			zap.Error(err),
		)
		return &EventBus{logger: logger, enabled: false}, nil
	}

	logger.Info("redis event bus connected", zap.String("addr", addr), zap.String("stream", StreamKey))
	return &EventBus{client: client, logger: logger, enabled: true}, nil
}

// Publish sends an event to the DVF Redis Stream.
// Returns nil and logs a warning if the bus is disabled or Redis is down.
func (b *EventBus) Publish(ctx context.Context, event Event) error {
	if !b.enabled {
		return nil // graceful degradation
	}

	payload, err := json.Marshal(event.Payload)
	if err != nil {
		payload = []byte("{}")
	}

	fields := map[string]interface{}{
		"event_type": string(event.Type),
		"entity_id":  event.EntityID,
		"device_id":  event.DeviceID,
		"status":     event.Status,
		"ts":         time.Now().UnixMilli(),
		"payload":    string(payload),
	}

	if err := b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey,
		MaxLen: MaxStreamLen,
		Approx: true,
		Values: fields,
	}).Err(); err != nil {
		b.logger.Warn("failed to publish event to redis stream",
			zap.String("event_type", string(event.Type)),
			zap.Error(err),
		)
		return nil // degrade gracefully — don't break the caller
	}

	b.logger.Debug("event published",
		zap.String("event_type", string(event.Type)),
		zap.String("entity_id", event.EntityID),
	)
	return nil
}

// Ping checks if the Redis connection is alive.
func (b *EventBus) Ping(ctx context.Context) error {
	if !b.enabled {
		return fmt.Errorf("event bus disabled")
	}
	return b.client.Ping(ctx).Err()
}

// Close releases the Redis connection pool.
func (b *EventBus) Close() error {
	if b.client != nil {
		return b.client.Close()
	}
	return nil
}

// Enabled returns true if the bus is connected and publishing.
func (b *EventBus) Enabled() bool {
	return b.enabled
}
