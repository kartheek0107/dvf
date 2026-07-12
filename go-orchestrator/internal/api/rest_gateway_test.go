package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
)

type mockPinger struct {
	err error
}

func (m *mockPinger) Ping(ctx context.Context) error {
	return m.err
}

func TestHealthCheckProbes(t *testing.T) {
	logger := zap.NewNop()
	store := storage.NewMemoryStore()
	defer store.Close()

	gateway := NewRESTGateway("localhost:50051", logger)
	// Register health checks
	pinger := &mockPinger{}
	gateway.RegisterHealthChecks(store, pinger)

	handler := gateway.Handler()

	t.Run("LivenessProbe", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp["status"] != "ok" {
			t.Errorf("expected status 'ok', got %v", resp["status"])
		}
	})

	t.Run("ReadinessProbe_Success", func(t *testing.T) {
		pinger.err = nil

		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp["status"] != "ok" {
			t.Errorf("expected status 'ok', got %v", resp["status"])
		}
	})

	t.Run("ReadinessProbe_Failure", func(t *testing.T) {
		pinger.err = fmt.Errorf("redis connection timed out")

		req := httptest.NewRequest("GET", "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp["status"] != "degraded" {
			t.Errorf("expected status 'degraded', got %v", resp["status"])
		}
	})
}
