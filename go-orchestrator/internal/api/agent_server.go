// Package api implements the gRPC and REST API layer for the DVF orchestrator.
//
// This file implements the AgentService gRPC server. Guest agents call
// these endpoints to register, send heartbeats, receive commands, and
// report test results.
package api

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
	pb "github.com/kartheekbudime/driver-validation-suite/go-orchestrator/proto/agentpb"
)

// AgentServer implements the AgentService gRPC interface.
type AgentServer struct {
	pb.UnimplementedAgentServiceServer

	store      storage.Store
	coordinator *core.AgentCoordinator
	logger     *zap.Logger
}

// NewAgentServer creates a new agent gRPC server.
func NewAgentServer(store storage.Store, coordinator *core.AgentCoordinator, logger *zap.Logger) *AgentServer {
	return &AgentServer{
		store:       store,
		coordinator: coordinator,
		logger:      logger,
	}
}

// RegisterWith registers this server on a gRPC server instance.
func (s *AgentServer) RegisterWith(srv *grpc.Server) {
	pb.RegisterAgentServiceServer(srv, s)
}

// RegisterAgent is called by a guest agent on startup to announce itself.
// It signals the execution engine that the agent is ready to receive commands.
func (s *AgentServer) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	if req.VmId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "vm_id is required")
	}

	s.logger.Info("agent registering",
		zap.String("vm_id", req.VmId),
		zap.String("hostname", req.Hostname),
		zap.String("agent_version", req.AgentVersion),
		zap.Strings("devices", req.AvailableDevices),
	)

	// Generate agent ID
	agentID := fmt.Sprintf("agent-%s", req.VmId)

	// Notify the coordinator that this agent is ready
	if err := s.coordinator.NotifyAgentReady(req.VmId, agentID); err != nil {
		s.logger.Warn("agent registration for unknown VM",
			zap.String("vm_id", req.VmId), zap.Error(err))
		return nil, status.Errorf(codes.NotFound, "VM not registered: %v", err)
	}

	// Update VM agent status in store
	s.store.UpdateVMStatus(ctx, req.VmId, core.VMStatusReady)

	s.logger.Info("agent registered",
		zap.String("agent_id", agentID),
		zap.String("vm_id", req.VmId),
	)

	return &pb.RegisterAgentResponse{
		AgentId:                  agentID,
		HeartbeatIntervalSeconds: 5,
		Status:                   "ok",
	}, nil
}

// Heartbeat is sent periodically by the agent to report its health.
func (s *AgentServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if req.AgentId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "agent_id is required")
	}

	s.logger.Debug("agent heartbeat",
		zap.String("agent_id", req.AgentId),
		zap.String("vm_id", req.VmId),
		zap.String("state", req.State),
		zap.Float64("cpu_usage", req.CpuUsagePercent),
	)

	// TODO: Update last_heartbeat in store for dead-agent detection

	return &pb.HeartbeatResponse{
		Status: "ok",
	}, nil
}

// GetCommand is polled by the agent to receive the next command.
// This blocks until a command is available (long-polling).
func (s *AgentServer) GetCommand(ctx context.Context, req *pb.GetCommandRequest) (*pb.Command, error) {
	if req.VmId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "vm_id is required")
	}

	cmd, err := s.coordinator.GetNextCommand(ctx, req.VmId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "getting command: %v", err)
	}

	s.logger.Info("dispatching command to agent",
		zap.String("vm_id", req.VmId),
		zap.String("command_id", cmd.ID),
		zap.String("type", cmd.Type),
	)

	return &pb.Command{
		CommandId:  cmd.ID,
		Type:       cmd.Type,
		Parameters: cmd.Parameters,
		IssuedAt:   timestamppb.Now(),
	}, nil
}

// ReportResult is called by the agent to report the result of a completed command.
func (s *AgentServer) ReportResult(ctx context.Context, req *pb.ReportResultRequest) (*pb.ReportResultResponse, error) {
	if req.AgentId == "" || req.CommandId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "agent_id and command_id are required")
	}

	s.logger.Info("agent reported result",
		zap.String("agent_id", req.AgentId),
		zap.String("command_id", req.CommandId),
		zap.String("status", req.Status),
		zap.Int64("duration_ms", req.DurationMs),
	)

	// Deliver the result to the waiting execution engine goroutine
	result := &core.AgentResult{
		CommandID:  req.CommandId,
		Status:     req.Status,
		Output:     req.Output,
		Logs:       req.Logs,
		DurationMs: req.DurationMs,
	}

	// Find the VM ID from the agent ID
	// Agent IDs are formatted as "agent-{vm_id}"
	vmID := req.AgentId
	if len(vmID) > 6 && vmID[:6] == "agent-" {
		vmID = vmID[6:]
	}

	if err := s.coordinator.DeliverResult(vmID, result); err != nil {
		s.logger.Warn("failed to deliver result",
			zap.String("command_id", req.CommandId),
			zap.Error(err),
		)
	}

	return &pb.ReportResultResponse{
		Acknowledged: true,
		NextAction:   "continue",
	}, nil
}
