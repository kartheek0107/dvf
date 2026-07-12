package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/cluster"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
	pb "github.com/kartheekbudime/driver-validation-suite/go-orchestrator/proto/orchestratorpb"
)

// Pinger is satisfied by any backend that can be health-checked.
type Pinger interface {
	Ping(ctx context.Context) error
}

// RESTGateway wraps the gRPC-Gateway reverse proxy that translates
// HTTP/JSON requests into gRPC calls.
type RESTGateway struct {
	mux      *runtime.ServeMux
	logger   *zap.Logger
	grpcAddr string
	handler  http.Handler // final composed handler (gateway + health)
}

// NewRESTGateway creates a new REST gateway that proxies to the given gRPC address.
func NewRESTGateway(grpcAddr string, logger *zap.Logger) *RESTGateway {
	mux := runtime.NewServeMux(
		// Use camelCase for JSON field names (proto default is snake_case)
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions:   runtime.JSONPb{}.MarshalOptions,
			UnmarshalOptions: runtime.JSONPb{}.UnmarshalOptions,
		}),
	)

	return &RESTGateway{
		mux:      mux,
		logger:   logger,
		grpcAddr: grpcAddr,
		handler:  withCORS(mux),
	}
}

// Register connects the REST gateway to the gRPC backend.
func (g *RESTGateway) Register(ctx context.Context) error {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	if err := pb.RegisterOrchestratorServiceHandlerFromEndpoint(ctx, g.mux, g.grpcAddr, opts); err != nil {
		return fmt.Errorf("registering REST gateway: %w", err)
	}

	g.logger.Info("REST gateway registered", zap.String("grpc_backend", g.grpcAddr))
	return nil
}

// RegisterHealthChecks mounts /healthz and /readyz on the handler chain.
// Must be called after Register().
//
//   - /healthz — liveness: always 200 while the process is alive
//   - /readyz  — readiness: 200 when store is reachable, 503 otherwise
func (g *RESTGateway) RegisterHealthChecks(store storage.Store, extras ...Pinger) {
	mux := http.NewServeMux()

	// /healthz — liveness (always ok if the process is running)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// /readyz — readiness (checks all backends)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		type check struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
			Err  string `json:"error,omitempty"`
		}
		var checks []check
		allOK := true

		// Check primary store (Postgres or memory)
		if err := store.Ping(ctx); err != nil {
			checks = append(checks, check{Name: "store", OK: false, Err: err.Error()})
			allOK = false
		} else {
			checks = append(checks, check{Name: "store", OK: true})
		}

		// Check any extra pingers (e.g. Redis event bus)
		for _, p := range extras {
			if err := p.Ping(ctx); err != nil {
				checks = append(checks, check{Name: "extra", OK: false, Err: err.Error()})
				allOK = false
			} else {
				checks = append(checks, check{Name: "extra", OK: true})
			}
		}

		body, _ := json.Marshal(map[string]interface{}{
			"status": func() string {
				if allOK {
					return "ok"
				}
				return "degraded"
			}(),
			"checks": checks,
		})

		w.Header().Set("Content-Type", "application/json")
		if allOK {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write(body)
	})

	// All other paths → gRPC-Gateway
	mux.Handle("/", g.handler)

	// Replace the composed handler so Handler() returns the new mux
	g.handler = withCORS(mux)
	g.logger.Info("health check endpoints registered", zap.Strings("paths", []string{"/healthz", "/readyz"}))
}

// RegisterClusterRoutes mounts the /cluster/* REST endpoints backed by the given NodeRegistry.
//   - POST /cluster/heartbeat — register or refresh a node heartbeat
//   - GET  /cluster/nodes     — list all known cluster members
//
// Must be called after RegisterHealthChecks.
func (g *RESTGateway) RegisterClusterRoutes(registry *cluster.NodeRegistry) {
	currentHandler := g.handler
	mux := http.NewServeMux()

	// POST /cluster/heartbeat
	mux.HandleFunc("/cluster/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req cluster.HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.NodeID == "" {
			http.Error(w, "node_id is required", http.StatusBadRequest)
			return
		}
		registry.Register(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// GET /cluster/nodes
	mux.HandleFunc("/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		nodes := registry.List()
		body, err := json.Marshal(map[string]interface{}{
			"nodes": nodes,
			"count": len(nodes),
		})
		if err != nil {
			http.Error(w, "serialisation error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	// All other routes fall through to the existing handler
	mux.Handle("/", currentHandler)
	g.handler = withCORS(mux)
	g.logger.Info("cluster routes registered", zap.Strings("paths", []string{"/cluster/heartbeat", "/cluster/nodes"}))
}

// Handler returns the HTTP handler for the REST gateway.
// This should be served on the REST port.
func (g *RESTGateway) Handler() http.Handler {
	return g.handler
}

// withCORS wraps an HTTP handler with permissive CORS headers for development.
func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, traceparent")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
