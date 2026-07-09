// Package api implements the gRPC and REST API layer for the DVF orchestrator.
package api

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
	pb "github.com/kartheekbudime/driver-validation-suite/go-orchestrator/proto/orchestratorpb"
)

// GRPCServer implements the OrchestratorService gRPC interface.
type GRPCServer struct {
	pb.UnimplementedOrchestratorServiceServer

	store    storage.Store
	registry *config.DeviceRegistry
	logger   *zap.Logger
	engine   *core.ExecutionEngine
}

// NewGRPCServer creates a new gRPC server with all dependencies injected.
func NewGRPCServer(store storage.Store, registry *config.DeviceRegistry, engine *core.ExecutionEngine, logger *zap.Logger) *GRPCServer {
	return &GRPCServer{
		store:    store,
		registry: registry,
		engine:   engine,
		logger:   logger,
	}
}

// RegisterWith registers this server on a gRPC server instance.
func (s *GRPCServer) RegisterWith(srv *grpc.Server) {
	pb.RegisterOrchestratorServiceServer(srv, s)
}

// --- Test Run Endpoints ---

// SubmitTestRun creates a new test run in PENDING status.
func (s *GRPCServer) SubmitTestRun(ctx context.Context, req *pb.SubmitTestRunRequest) (*pb.SubmitTestRunResponse, error) {
	s.logger.Info("SubmitTestRun",
		zap.String("device_id", req.DeviceId),
		zap.String("test_suite_id", req.TestSuiteId),
		zap.String("requested_by", req.RequestedBy),
	)

	// Validate device exists in registry
	if _, err := s.registry.FindDevice(req.DeviceId); err != nil {
		return nil, status.Errorf(codes.NotFound, "device not found: %v", err)
	}

	if req.TestSuiteId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "test_suite_id is required")
	}

	run := &core.TestRun{
		DeviceID:    req.DeviceId,
		TestSuiteID: req.TestSuiteId,
		Priority:    int(req.Priority),
		Status:      core.TestRunStatusPending,
		Tags:        req.Tags,
		RequestedBy: req.RequestedBy,
	}

	created, err := s.store.CreateTestRun(ctx, run)
	if err != nil {
		s.logger.Error("failed to create test run", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create test run: %v", err)
	}

	s.logger.Info("test run created", zap.String("id", created.ID))

	// Submit test run to the execution engine
	if s.engine != nil {
		s.engine.SubmitTestRun(created)
	}

	return &pb.SubmitTestRunResponse{
		TestRun: testRunToProto(created),
	}, nil
}

// GetTestRun retrieves a test run by ID.
func (s *GRPCServer) GetTestRun(ctx context.Context, req *pb.GetTestRunRequest) (*pb.TestRun, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	run, err := s.store.GetTestRun(ctx, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "test run not found: %v", err)
	}

	return testRunToProto(run), nil
}

// ListTestRuns returns test runs with optional filtering.
func (s *GRPCServer) ListTestRuns(ctx context.Context, req *pb.ListTestRunsRequest) (*pb.ListTestRunsResponse, error) {
	coreReq := &core.ListTestRunsRequest{
		DeviceID: req.DeviceId,
		Limit:    int(req.Limit),
		Offset:   int(req.Offset),
	}
	if req.Status != pb.TestRunStatus_TEST_RUN_STATUS_UNSPECIFIED {
		coreReq.Status = protoToTestRunStatus(req.Status)
	}

	runs, err := s.store.ListTestRuns(ctx, coreReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list test runs: %v", err)
	}

	pbRuns := make([]*pb.TestRun, len(runs))
	for i, r := range runs {
		pbRuns[i] = testRunToProto(r)
	}

	return &pb.ListTestRunsResponse{
		TestRuns:   pbRuns,
		TotalCount: int32(len(pbRuns)),
	}, nil
}

// CancelTestRun cancels a pending or running test run.
func (s *GRPCServer) CancelTestRun(ctx context.Context, req *pb.CancelTestRunRequest) (*pb.CancelTestRunResponse, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	run, err := s.store.GetTestRun(ctx, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "test run not found: %v", err)
	}

	// Only pending or running runs can be cancelled
	if run.Status != core.TestRunStatusPending && run.Status != core.TestRunStatusRunning &&
		run.Status != core.TestRunStatusQueued {
		return &pb.CancelTestRunResponse{
			Success: false,
			Message: fmt.Sprintf("cannot cancel test run in status %s", run.Status),
		}, nil
	}

	if err := s.store.UpdateTestRunStatus(ctx, req.Id, core.TestRunStatusCancelled, "cancelled by user"); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to cancel: %v", err)
	}

	s.logger.Info("test run cancelled", zap.String("id", req.Id))

	return &pb.CancelTestRunResponse{
		Success: true,
		Message: "test run cancelled",
	}, nil
}

// GetTestResults retrieves the results of a completed test run.
func (s *GRPCServer) GetTestResults(ctx context.Context, req *pb.GetTestResultsRequest) (*pb.GetTestResultsResponse, error) {
	if req.TestRunId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "test_run_id is required")
	}

	results, err := s.store.GetTestResults(ctx, req.TestRunId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get results: %v", err)
	}

	pbResults := make([]*pb.TestResult, len(results))
	for i, r := range results {
		pbResults[i] = &pb.TestResult{
			Id:          r.ID,
			TestRunId:   r.TestRunID,
			TestName:    r.TestName,
			Category:    string(r.Category),
			Status:      testRunStatusToProto(r.Status),
			DurationMs:  r.DurationMs,
			Message:     r.Message,
			Metrics:     r.Metrics,
			Logs:        r.Logs,
			CompletedAt: timestamppb.New(r.CompletedAt),
		}
	}

	return &pb.GetTestResultsResponse{Results: pbResults}, nil
}

// --- VM Endpoints ---

// ListVMs returns all VMs with optional filtering.
func (s *GRPCServer) ListVMs(ctx context.Context, req *pb.ListVMsRequest) (*pb.ListVMsResponse, error) {
	coreReq := &core.ListVMsRequest{
		DeviceID: req.DeviceId,
	}
	if req.Status != pb.VMStatus_VM_STATUS_UNSPECIFIED {
		coreReq.Status = protoToVMStatus(req.Status)
	}

	vms, err := s.store.ListVMs(ctx, coreReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list VMs: %v", err)
	}

	pbVMs := make([]*pb.VMInstance, len(vms))
	for i, vm := range vms {
		pbVMs[i] = vmToProto(vm)
	}

	return &pb.ListVMsResponse{Vms: pbVMs}, nil
}

// GetVM retrieves a VM by ID.
func (s *GRPCServer) GetVM(ctx context.Context, req *pb.GetVMRequest) (*pb.VMInstance, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	vm, err := s.store.GetVM(ctx, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "vm not found: %v", err)
	}

	return vmToProto(vm), nil
}

// --- Device Endpoints ---

// ListDevices returns all registered devices from the device registry.
func (s *GRPCServer) ListDevices(_ context.Context, _ *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	pbDevices := make([]*pb.DeviceEntry, len(s.registry.Devices))
	for i, d := range s.registry.Devices {
		pbDevices[i] = deviceToProto(&d)
	}
	return &pb.ListDevicesResponse{Devices: pbDevices}, nil
}

// GetDevice retrieves a device from the registry.
func (s *GRPCServer) GetDevice(_ context.Context, req *pb.GetDeviceRequest) (*pb.DeviceEntry, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}

	d, err := s.registry.FindDevice(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "device not found: %v", err)
	}

	return deviceToProto(d), nil
}

// --- Health ---

// GetHealth returns the health status of the orchestrator.
func (s *GRPCServer) GetHealth(ctx context.Context, _ *pb.GetHealthRequest) (*pb.GetHealthResponse, error) {
	components := make(map[string]*pb.ComponentHealth)

	// Check store
	if err := s.store.Ping(ctx); err != nil {
		components["storage"] = &pb.ComponentHealth{Status: "unhealthy", Message: err.Error()}
	} else {
		components["storage"] = &pb.ComponentHealth{Status: "healthy", Message: "ok"}
	}

	components["grpc_server"] = &pb.ComponentHealth{Status: "healthy", Message: "serving"}

	overallStatus := "healthy"
	for _, c := range components {
		if c.Status == "unhealthy" {
			overallStatus = "degraded"
			break
		}
	}

	return &pb.GetHealthResponse{
		Status:     overallStatus,
		Components: components,
		Timestamp:  timestamppb.Now(),
	}, nil
}

// --- Proto Conversion Helpers ---

func testRunToProto(r *core.TestRun) *pb.TestRun {
	p := &pb.TestRun{
		Id:           r.ID,
		DeviceId:     r.DeviceID,
		TestSuiteId:  r.TestSuiteID,
		Status:       testRunStatusToProto(r.Status),
		VmId:         r.VMID,
		Priority:     int32(r.Priority),
		CreatedAt:    timestamppb.New(r.CreatedAt),
		DurationMs:   r.DurationMs,
		RequestedBy:  r.RequestedBy,
		Tags:         r.Tags,
		ErrorMessage: r.ErrorMessage,
	}
	if r.StartedAt != nil {
		p.StartedAt = timestamppb.New(*r.StartedAt)
	}
	if r.CompletedAt != nil {
		p.CompletedAt = timestamppb.New(*r.CompletedAt)
	}
	return p
}

func vmToProto(vm *core.VMInstance) *pb.VMInstance {
	p := &pb.VMInstance{
		Id:               vm.ID,
		Status:           vmStatusToProto(vm.Status),
		DeviceId:         vm.DeviceID,
		QmpSocketPath:    vm.QMPSocketPath,
		SerialPorts:      vm.SerialPorts,
		Pid:              int32(vm.PID),
		AllocatedCpus:    int32(vm.AllocatedCPUs),
		AllocatedMemMb:   int32(vm.AllocatedMemMB),
		ImagePath:        vm.ImagePath,
		CreatedAt:        timestamppb.New(vm.CreatedAt),
		AgentStatus:      string(vm.AgentStatus),
		CurrentTestRunId: vm.CurrentTestRunID,
	}
	if vm.LastHeartbeat != nil {
		p.LastHeartbeat = timestamppb.New(*vm.LastHeartbeat)
	}
	return p
}

func deviceToProto(d *config.DeviceEntry) *pb.DeviceEntry {
	return &pb.DeviceEntry{
		Id:             d.ID,
		Name:           d.Name,
		VendorId:       d.VendorID,
		DeviceId:       d.DeviceID,
		PciClass:       d.PCIClass,
		QemuDeviceName: d.QEMUDeviceName,
		DriverModule:   d.DriverModule,
		DriverPath:     d.DriverPath,
		Description:    d.Description,
		Capabilities:   d.Capabilities,
		TestSuites:     d.TestSuites,
	}
}

func testRunStatusToProto(s core.TestRunStatus) pb.TestRunStatus {
	switch s {
	case core.TestRunStatusPending:
		return pb.TestRunStatus_TEST_RUN_STATUS_PENDING
	case core.TestRunStatusQueued:
		return pb.TestRunStatus_TEST_RUN_STATUS_QUEUED
	case core.TestRunStatusRunning:
		return pb.TestRunStatus_TEST_RUN_STATUS_RUNNING
	case core.TestRunStatusPassed:
		return pb.TestRunStatus_TEST_RUN_STATUS_PASSED
	case core.TestRunStatusFailed:
		return pb.TestRunStatus_TEST_RUN_STATUS_FAILED
	case core.TestRunStatusErrored:
		return pb.TestRunStatus_TEST_RUN_STATUS_ERRORED
	case core.TestRunStatusCancelled:
		return pb.TestRunStatus_TEST_RUN_STATUS_CANCELLED
	case core.TestRunStatusTimeout:
		return pb.TestRunStatus_TEST_RUN_STATUS_TIMEOUT
	default:
		return pb.TestRunStatus_TEST_RUN_STATUS_UNSPECIFIED
	}
}

func protoToTestRunStatus(s pb.TestRunStatus) core.TestRunStatus {
	switch s {
	case pb.TestRunStatus_TEST_RUN_STATUS_PENDING:
		return core.TestRunStatusPending
	case pb.TestRunStatus_TEST_RUN_STATUS_QUEUED:
		return core.TestRunStatusQueued
	case pb.TestRunStatus_TEST_RUN_STATUS_RUNNING:
		return core.TestRunStatusRunning
	case pb.TestRunStatus_TEST_RUN_STATUS_PASSED:
		return core.TestRunStatusPassed
	case pb.TestRunStatus_TEST_RUN_STATUS_FAILED:
		return core.TestRunStatusFailed
	case pb.TestRunStatus_TEST_RUN_STATUS_ERRORED:
		return core.TestRunStatusErrored
	case pb.TestRunStatus_TEST_RUN_STATUS_CANCELLED:
		return core.TestRunStatusCancelled
	case pb.TestRunStatus_TEST_RUN_STATUS_TIMEOUT:
		return core.TestRunStatusTimeout
	default:
		return ""
	}
}

func vmStatusToProto(s core.VMStatus) pb.VMStatus {
	switch s {
	case core.VMStatusCreating:
		return pb.VMStatus_VM_STATUS_CREATING
	case core.VMStatusBooting:
		return pb.VMStatus_VM_STATUS_BOOTING
	case core.VMStatusReady:
		return pb.VMStatus_VM_STATUS_READY
	case core.VMStatusRunningTest:
		return pb.VMStatus_VM_STATUS_RUNNING_TEST
	case core.VMStatusStopping:
		return pb.VMStatus_VM_STATUS_STOPPING
	case core.VMStatusStopped:
		return pb.VMStatus_VM_STATUS_STOPPED
	case core.VMStatusError:
		return pb.VMStatus_VM_STATUS_ERROR
	case core.VMStatusDestroyed:
		return pb.VMStatus_VM_STATUS_DESTROYED
	default:
		return pb.VMStatus_VM_STATUS_UNSPECIFIED
	}
}

func protoToVMStatus(s pb.VMStatus) core.VMStatus {
	switch s {
	case pb.VMStatus_VM_STATUS_CREATING:
		return core.VMStatusCreating
	case pb.VMStatus_VM_STATUS_BOOTING:
		return core.VMStatusBooting
	case pb.VMStatus_VM_STATUS_READY:
		return core.VMStatusReady
	case pb.VMStatus_VM_STATUS_RUNNING_TEST:
		return core.VMStatusRunningTest
	case pb.VMStatus_VM_STATUS_STOPPING:
		return core.VMStatusStopping
	case pb.VMStatus_VM_STATUS_STOPPED:
		return core.VMStatusStopped
	case pb.VMStatus_VM_STATUS_ERROR:
		return core.VMStatusError
	case pb.VMStatus_VM_STATUS_DESTROYED:
		return core.VMStatusDestroyed
	default:
		return ""
	}
}

// Ensure unused import doesn't cause issue
var _ = time.Now
