// Package api implements the TelemetryService gRPC server.
// It receives streamed logs, metrics, and events from guest agents
// and logs them via the orchestrator's structured logger.
package api

import (
	"io"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/kartheekbudime/driver-validation-suite/go-orchestrator/proto/telemetrypb"
)

// TelemetryServer implements the TelemetryService gRPC interface.
type TelemetryServer struct {
	pb.UnimplementedTelemetryServiceServer
	logger *zap.Logger
}

// NewTelemetryServer creates a new telemetry gRPC server.
func NewTelemetryServer(logger *zap.Logger) *TelemetryServer {
	return &TelemetryServer{logger: logger}
}

// RegisterWith registers this server on a gRPC server instance.
func (s *TelemetryServer) RegisterWith(srv *grpc.Server) {
	pb.RegisterTelemetryServiceServer(srv, s)
}

// StreamLogs receives log entries from a guest agent until the stream closes.
func (s *TelemetryServer) StreamLogs(stream grpc.ClientStreamingServer[pb.LogEntry, pb.StreamAck]) error {
	var count int64
	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.StreamAck{ReceivedCount: count, Status: "ok"})
		}
		if err != nil {
			return err
		}
		count++
		s.logger.Info("agent_log",
			zap.String("vm_id", entry.VmId),
			zap.String("severity", entry.Severity),
			zap.String("source", entry.Source),
			zap.String("message", entry.Message),
		)
	}
}

// StreamMetrics receives metric data points from a guest agent.
func (s *TelemetryServer) StreamMetrics(stream grpc.ClientStreamingServer[pb.MetricEntry, pb.StreamAck]) error {
	var count int64
	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.StreamAck{ReceivedCount: count, Status: "ok"})
		}
		if err != nil {
			return err
		}
		count++
		s.logger.Info("agent_metric",
			zap.String("vm_id", entry.VmId),
			zap.String("name", entry.Name),
			zap.Float64("value", entry.Value),
		)
	}
}

// StreamEvents receives structured events from a guest agent.
func (s *TelemetryServer) StreamEvents(stream grpc.ClientStreamingServer[pb.EventEntry, pb.StreamAck]) error {
	var count int64
	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.StreamAck{ReceivedCount: count, Status: "ok"})
		}
		if err != nil {
			return err
		}
		count++
		s.logger.Info("agent_event",
			zap.String("vm_id", entry.VmId),
			zap.String("event_type", entry.EventType),
			zap.String("payload", entry.Payload),
		)
	}
}
