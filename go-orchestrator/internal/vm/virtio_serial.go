// Package vm provides the VirtioSerialHub — a bridge between QEMU virtio-serial
// Unix sockets (host side) and the AgentCoordinator (core logic).
//
// Protocol: newline-delimited JSON over the Unix socket.
//
// Guest → Host messages:
//   {"msg":"register","vm_id":"...","hostname":"...","agent_version":"1.0"}
//   {"msg":"heartbeat","vm_id":"...","state":"READY"}
//   {"msg":"result","command_id":"...","status":"passed","output":"...","logs":"...","duration_ms":100}
//   {"msg":"log","vm_id":"...","severity":"INFO","message":"..."}
//
// Host → Guest messages:
//   {"msg":"ack","agent_id":"..."}
//   {"msg":"command","command_id":"...","cmd":"load_driver","params":{"ko_path":"..."}}
package vm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
)

// VirtioSerialHub manages virtio-serial connections for all live VMs.
// One goroutine per VM reads/writes JSON over the QEMU-created Unix socket.
type VirtioSerialHub struct {
	coord  *core.AgentCoordinator
	logger *zap.Logger

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // key: VM ID
}

// NewVirtioSerialHub creates a new hub.
func NewVirtioSerialHub(coord *core.AgentCoordinator, logger *zap.Logger) *VirtioSerialHub {
	return &VirtioSerialHub{
		coord:   coord,
		logger:  logger,
		cancels: make(map[string]context.CancelFunc),
	}
}

// AgentSocketPath returns the host-side Unix socket path for a given VM.
func AgentSocketPath(vmID string) string {
	return filepath.Join("/tmp/dvf/agent", vmID+".sock")
}

// ConnectVM starts a goroutine that connects to the VM's virtio-serial socket
// and dispatches messages to/from the AgentCoordinator.
// Call after StartVM so the socket file exists.
func (h *VirtioSerialHub) ConnectVM(parentCtx context.Context, vmID string) {
	ctx, cancel := context.WithCancel(parentCtx)
	h.mu.Lock()
	h.cancels[vmID] = cancel
	h.mu.Unlock()

	go h.serveVM(ctx, vmID)
}

// DisconnectVM stops the goroutine for a VM.
func (h *VirtioSerialHub) DisconnectVM(vmID string) {
	h.mu.Lock()
	cancel, ok := h.cancels[vmID]
	delete(h.cancels, vmID)
	h.mu.Unlock()
	if ok {
		cancel()
	}
}

// serveVM connects to the Unix socket and handles messages in a loop.
func (h *VirtioSerialHub) serveVM(ctx context.Context, vmID string) {
	socketPath := AgentSocketPath(vmID)
	h.logger.Info("virtio-serial: waiting for agent connection", zap.String("vm_id", vmID), zap.String("socket", socketPath))

	// ponytail: retry dial until context cancelled — QEMU creates the socket
	// immediately but the guest agent may take a few seconds to open it.
	var conn net.Conn
	for {
		var err error
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	defer conn.Close()
	h.logger.Info("virtio-serial: agent connected", zap.String("vm_id", vmID))

	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Bytes()
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			h.logger.Warn("virtio-serial: bad JSON from agent", zap.String("vm_id", vmID), zap.Error(err))
			continue
		}

		switch msg["msg"] {
		case "register":
			agentID := fmt.Sprintf("agent-%s", vmID)
			h.logger.Info("agent registered", zap.String("vm_id", vmID), zap.String("agent_id", agentID))
			_ = h.coord.NotifyAgentReady(vmID, agentID)
			// Send ack
			_ = encoder.Encode(map[string]string{"msg": "ack", "agent_id": agentID})
			// Start command pump — forwards pending commands to the agent
			go h.commandPump(ctx, vmID, encoder)

		case "result":
			result := &core.AgentResult{
				CommandID:  strVal(msg, "command_id"),
				Status:     strVal(msg, "status"),
				Output:     strVal(msg, "output"),
				Logs:       strVal(msg, "logs"),
				DurationMs: int64Val(msg, "duration_ms"),
			}
			if err := h.coord.DeliverResult(vmID, result); err != nil {
				h.logger.Warn("virtio-serial: could not deliver result", zap.String("vm_id", vmID), zap.Error(err))
			}

		case "log":
			h.logger.Info("agent_log",
				zap.String("vm_id", vmID),
				zap.String("severity", strVal(msg, "severity")),
				zap.String("message", strVal(msg, "message")),
			)

		case "heartbeat":
			// no-op for now; ponytail: add health tracking if needed

		default:
			h.logger.Debug("virtio-serial: unknown message type", zap.String("vm_id", vmID), zap.Any("msg", msg["msg"]))
		}
	}

	if err := scanner.Err(); err != nil {
		h.logger.Warn("virtio-serial: scanner error", zap.String("vm_id", vmID), zap.Error(err))
	}
}

// commandPump forwards commands from AgentCoordinator to the agent over the socket.
func (h *VirtioSerialHub) commandPump(ctx context.Context, vmID string, enc *json.Encoder) {
	for {
		cmd, err := h.coord.GetNextCommand(ctx, vmID)
		if err != nil {
			// context cancelled or channel closed — normal shutdown
			return
		}
		msg := map[string]interface{}{
			"msg":        "command",
			"command_id": cmd.ID,
			"cmd":        cmd.Type,
			"params":     cmd.Parameters,
		}
		if err := enc.Encode(msg); err != nil {
			h.logger.Warn("virtio-serial: write error", zap.String("vm_id", vmID), zap.Error(err))
			return
		}
	}
}

// strVal safely reads a string from a map[string]interface{}.
func strVal(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// int64Val safely reads a number as int64 from a map[string]interface{}.
func int64Val(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		}
	}
	return 0
}
