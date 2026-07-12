// DVF Orchestrator — Entry point for the Device Validation Framework control plane.
//
// This binary starts the gRPC server (with both OrchestratorService and
// AgentService), the REST gateway, the VM Manager, the Agent Coordinator,
// and the Execution Engine. On shutdown, everything is torn down gracefully.
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
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/api"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/observability"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/telemetry"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/vm"
)

func main() {
	configDir   := flag.String("config", "configs", "Path to configuration directory")
	storageMode := flag.String("storage", "memory", "Storage backend: 'memory' or 'postgres'")
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
		zap.String("storage_mode", *storageMode),
	)

	// --- Initialize Distributed Tracing ---
	_, traceShutdown, err := observability.InitTracer(cfg.Telemetry)
	if err != nil {
		logger.Fatal("failed to initialize tracer", zap.Error(err))
	}
	defer traceShutdown(context.Background())

	// --- Initialize Audit Logger ---
	auditLogger := observability.NewAuditLogger(logger)

	// --- Initialize Event Bus ---
	eventBus, err := telemetry.NewEventBus(cfg.Storage.Redis, logger.Named("eventbus"))
	if err != nil {
		logger.Warn("event bus setup finished with warning", zap.Error(err))
	}
	defer eventBus.Close()

	// --- Initialize Storage ---
	var store storage.Store

	if *storageMode == "postgres" {
		dsn := cfg.Storage.Postgres.DSN()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		pgStore, pgErr := storage.NewPostgresStore(ctx, dsn, cfg.Storage.Postgres.MaxConnections)
		cancel()

		if pgErr != nil {
			logger.Warn("postgres unavailable — falling back to in-memory store",
				zap.Error(pgErr),
				zap.String("dsn_host", cfg.Storage.Postgres.Host),
			)
			store = storage.NewMemoryStore()
		} else {
			store = pgStore
			logger.Info("storage initialized",
				zap.String("backend", "postgres"),
				zap.String("host", cfg.Storage.Postgres.Host),
				zap.String("database", cfg.Storage.Postgres.Database),
			)
		}
	} else {
		store = storage.NewMemoryStore()
		logger.Info("storage initialized", zap.String("backend", "memory"))
	}
	defer store.Close()


	// --- Initialize VM Manager ---
	vmManager := vm.NewVMManager(cfg, store, logger.Named("vm"))
	logger.Info("VM manager initialized",
		zap.String("qemu_binary", cfg.QEMU.BinaryPath),
		zap.Int("max_concurrent_vms", cfg.VMDefaults.MaxConcurrentVMs),
	)

	// --- Initialize Agent Coordinator ---
	agentCoord := core.NewAgentCoordinator()

	// --- Initialize VirtioSerialHub (replaces AgentService gRPC) ---
	// The hub owns the host-side Unix sockets and bridges virtio-serial
	// JSON messages to/from the AgentCoordinator.
	virtioHub := vm.NewVirtioSerialHub(agentCoord, logger.Named("virtio"))
	logger.Info("agent coordinator + virtio hub initialized")

	// --- Initialize Scheduler + Execution Engine ---
	scheduler := core.NewScheduler(cfg.VMDefaults.MaxConcurrentVMs)

	engine := core.NewExecutionEngine(
		scheduler,
		store,
		registry,
		&vmManagerAdapter{m: vmManager, hub: virtioHub},
		agentCoord,
		cfg,
		logger.Named("engine"),
		eventBus,
		auditLogger,
	)
	engine.Start()
	defer engine.Stop()

	logger.Info("execution engine started",
		zap.Int("max_concurrent", cfg.VMDefaults.MaxConcurrentVMs),
	)

	// --- Start gRPC Server ---
	grpcAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort)
	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Fatal("failed to listen for gRPC", zap.String("addr", grpcAddr), zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(loggingInterceptor(logger)),
	)

	// Register the orchestrator service
	orchServer := api.NewGRPCServer(store, registry, engine, logger, auditLogger)
	orchServer.RegisterWith(grpcServer)

	// Register the agent service
	// ponytail: AgentService gRPC is kept for potential future remote agents;
	// local QEMU agents use virtio-serial via VirtioSerialHub instead.
	agentServer := api.NewAgentServer(store, agentCoord, logger.Named("agent"))
	agentServer.RegisterWith(grpcServer)

	// Register the telemetry service
	telemetryServer := api.NewTelemetryServer(logger.Named("telemetry"))
	telemetryServer.RegisterWith(grpcServer)

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

	// Register liveness/readiness probes with health dependencies
	restGateway.RegisterHealthChecks(store, eventBus)

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

// vmManagerAdapter adapts the concrete vm.VMManager to the core.VMManagerInterface.
type vmManagerAdapter struct {
	m   *vm.VMManager
	hub *vm.VirtioSerialHub
}

func (a *vmManagerAdapter) CreateVM(ctx context.Context, vmCfg interface{}) (*core.VMInstance, error) {
	device, ok := vmCfg.(*config.DeviceEntry)
	if !ok {
		return nil, fmt.Errorf("invalid vm config type: %T", vmCfg)
	}
	return a.m.CreateVM(ctx, &vm.VMConfig{
		DeviceEntry: device,
	})
}

func (a *vmManagerAdapter) StartVM(ctx context.Context, vmID string) error {
	if err := a.m.StartVM(ctx, vmID); err != nil {
		return err
	}
	// Wire the virtio-serial hub after the VM starts (socket is now created by QEMU)
	a.hub.ConnectVM(ctx, vmID)
	return nil
}

func (a *vmManagerAdapter) StopVM(ctx context.Context, vmID string) error {
	a.hub.DisconnectVM(vmID)
	return a.m.StopVM(ctx, vmID)
}

func (a *vmManagerAdapter) DestroyVM(ctx context.Context, vmID string) error {
	return a.m.DestroyVM(ctx, vmID)
}
