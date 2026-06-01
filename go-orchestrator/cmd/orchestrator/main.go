// DVF Orchestrator — Entry point for the Device Validation Framework control plane.
//
// This binary starts both the gRPC server and REST gateway,
// loads configuration, and initializes all subsystems.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/api"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/observability"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
)

func main() {
	configDir := flag.String("config", "configs", "Path to configuration directory")
	flag.Parse()

	// --- Load Configuration ---
	cfg, err := config.Load(*configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to load config: %v\n", err)
		os.Exit(1)
	}

	registry, err := config.LoadDeviceRegistry(*configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to load device registry: %v\n", err)
		os.Exit(1)
	}

	// --- Initialize Logger ---
	logger, err := observability.NewLogger(cfg.Telemetry.LogLevel, cfg.Telemetry.LogFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("DVF Orchestrator starting",
		zap.Int("grpc_port", cfg.Server.GRPCPort),
		zap.Int("rest_port", cfg.Server.RESTPort),
		zap.Int("registered_devices", len(registry.Devices)),
	)

	// --- Initialize Storage ---
	// Using in-memory store for now. Swap with Postgres when ready.
	store := storage.NewMemoryStore()
	defer store.Close()

	logger.Info("storage initialized", zap.String("backend", "memory"))

	// --- Start gRPC Server ---
	grpcAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort)
	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Fatal("failed to listen for gRPC", zap.String("addr", grpcAddr), zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(loggingInterceptor(logger)),
	)

	// Register the orchestrator service
	orchServer := api.NewGRPCServer(store, registry, logger)
	orchServer.RegisterWith(grpcServer)

	// Enable gRPC reflection for debugging with grpcurl
	reflection.Register(grpcServer)

	go func() {
		logger.Info("gRPC server listening", zap.String("addr", grpcAddr))
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal("gRPC server failed", zap.Error(err))
		}
	}()

	// --- Start REST Gateway ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restGateway := api.NewRESTGateway(grpcAddr, logger)
	if err := restGateway.Register(ctx); err != nil {
		logger.Fatal("failed to register REST gateway", zap.Error(err))
	}

	restAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.RESTPort)
	restServer := &http.Server{
		Addr:    restAddr,
		Handler: restGateway.Handler(),
	}

	go func() {
		logger.Info("REST gateway listening", zap.String("addr", restAddr))
		if err := restServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("REST server failed", zap.Error(err))
		}
	}()

	// --- Print Startup Summary ---
	logger.Info("=== DVF Orchestrator is ready ===")
	logger.Info("gRPC endpoint", zap.String("address", grpcAddr))
	logger.Info("REST endpoint", zap.String("address", "http://"+restAddr))
	logger.Info("registered devices:")
	for _, d := range registry.Devices {
		logger.Info("  device",
			zap.String("id", d.ID),
			zap.String("name", d.Name),
			zap.String("vendor:device", d.VendorID+":"+d.DeviceID),
		)
	}

	// --- Graceful Shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("received shutdown signal", zap.String("signal", sig.String()))

	// Graceful stop
	grpcServer.GracefulStop()
	restServer.Shutdown(ctx)
	logger.Info("DVF Orchestrator stopped")
}

// loggingInterceptor is a gRPC unary interceptor that logs each RPC call.
func loggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		logger.Debug("gRPC call",
			zap.String("method", info.FullMethod),
		)
		resp, err := handler(ctx, req)
		if err != nil {
			logger.Warn("gRPC call failed",
				zap.String("method", info.FullMethod),
				zap.Error(err),
			)
		}
		return resp, err
	}
}
