package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/kartheekbudime/driver-validation-suite/go-orchestrator/proto/orchestratorpb"
)

// RESTGateway wraps the gRPC-Gateway reverse proxy that translates
// HTTP/JSON requests into gRPC calls.
type RESTGateway struct {
	mux      *runtime.ServeMux
	logger   *zap.Logger
	grpcAddr string
}

// NewRESTGateway creates a new REST gateway that proxies to the given gRPC address.
func NewRESTGateway(grpcAddr string, logger *zap.Logger) *RESTGateway {
	mux := runtime.NewServeMux(
		// Use camelCase for JSON field names (proto default is snake_case)
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: runtime.JSONPb{}.MarshalOptions,
			UnmarshalOptions: runtime.JSONPb{}.UnmarshalOptions,
		}),
	)

	return &RESTGateway{
		mux:      mux,
		logger:   logger,
		grpcAddr: grpcAddr,
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

// Handler returns the HTTP handler for the REST gateway.
// This should be served on the REST port.
func (g *RESTGateway) Handler() http.Handler {
	return withCORS(g.mux)
}

// withCORS wraps an HTTP handler with permissive CORS headers for development.
func withCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
