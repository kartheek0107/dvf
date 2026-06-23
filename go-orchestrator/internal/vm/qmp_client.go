// Package vm provides QEMU virtual machine management for the DVF orchestrator.
//
// This file implements the QMP (QEMU Machine Protocol) client.
// QMP is a JSON-based protocol that QEMU exposes over a Unix domain socket.
//
// Usage:
//
//	client := NewQMPClient("/var/run/dvf/qmp/vm-1.sock", logger)
//	if err := client.Connect(ctx); err != nil { ... }
//	defer client.Close()
//
//	status, err := client.QueryStatus(ctx)
//	fmt.Println(status.Running) // true
//
//	// Listen for async events
//	go func() {
//	    for event := range client.Events() {
//	        log.Printf("QEMU event: %s", event.Event)
//	    }
//	}()
package vm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// ErrClosed is returned when an operation is attempted on a closed QMP client.
var ErrClosed = errors.New("qmp client is closed")

// QMPClient manages a connection to QEMU's QMP (QEMU Machine Protocol)
// over a Unix domain socket.
//
// The client handles the QMP handshake (greeting + qmp_capabilities),
// routes async events to a separate channel, and serialises command
// execution so that only one command is in flight at a time.
type QMPClient struct {
	socketPath string
	conn       net.Conn
	logger     *zap.Logger

	// Parsed from the QMP greeting.
	version QMPVersion

	// JSON encoder for sending commands. Protected by writeMu.
	encoder *json.Encoder
	writeMu sync.Mutex

	// Serialises Execute calls so at most one command is in flight.
	cmdMu sync.Mutex

	// Reader goroutine routes incoming messages to these channels.
	responseCh chan *QMPResponse // buffered(1): command responses
	eventsCh   chan QMPEvent     // buffered(64): async events

	// Closed when the reader goroutine exits.
	doneCh chan struct{}

	// Atomic flag: 0 = open, 1 = closed.
	closed int32
}

// NewQMPClient creates a new QMP client for the given Unix socket path.
// Call Connect() to establish the connection and perform the handshake.
func NewQMPClient(socketPath string, logger *zap.Logger) *QMPClient {
	return &QMPClient{
		socketPath: socketPath,
		logger:     logger,
		responseCh: make(chan *QMPResponse, 1),
		eventsCh:   make(chan QMPEvent, 64),
		doneCh:     make(chan struct{}),
	}
}

// Connect establishes a connection to the QMP Unix socket and performs
// the initial protocol handshake:
//  1. Read the QMP greeting (version, capabilities)
//  2. Send qmp_capabilities to enter command mode
//  3. Read the success response
//  4. Start the background reader goroutine
//
// The context controls the dial timeout and the handshake deadline.
func (c *QMPClient) Connect(ctx context.Context) error {
	// Dial the Unix socket with context support.
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dialing QMP socket %s: %w", c.socketPath, err)
	}
	c.conn = conn

	reader := bufio.NewReader(conn)
	decoder := json.NewDecoder(reader)
	c.encoder = json.NewEncoder(conn)

	// --- Handshake Step 1: Read the QMP greeting ---
	var greeting QMPGreeting
	if err := decoder.Decode(&greeting); err != nil {
		conn.Close()
		return fmt.Errorf("reading QMP greeting: %w", err)
	}
	c.version = greeting.QMP.Version
	c.logger.Debug("QMP greeting received",
		zap.String("version", c.version.String()),
		zap.Strings("capabilities", greeting.QMP.Capabilities),
	)

	// --- Handshake Step 2: Send qmp_capabilities ---
	capCmd := QMPCommand{Execute: "qmp_capabilities"}
	if err := c.encoder.Encode(capCmd); err != nil {
		conn.Close()
		return fmt.Errorf("sending qmp_capabilities: %w", err)
	}

	// --- Handshake Step 3: Read the response ---
	var resp QMPResponse
	if err := decoder.Decode(&resp); err != nil {
		conn.Close()
		return fmt.Errorf("reading capabilities response: %w", err)
	}
	if resp.Error != nil {
		conn.Close()
		return fmt.Errorf("qmp_capabilities rejected: %w", resp.Error)
	}

	c.logger.Info("QMP connection established",
		zap.String("socket", c.socketPath),
		zap.String("qemu_version", c.version.String()),
	)

	// Start the background reader goroutine for subsequent messages.
	go c.reader(decoder)

	return nil
}

// reader is the background goroutine that reads all messages from the QMP
// socket after the handshake and routes them:
//   - Response messages (has "return" or "error") → responseCh
//   - Event messages (has "event")                → eventsCh
//
// The goroutine exits when the connection is closed or on read error,
// and closes doneCh to signal this.
func (c *QMPClient) reader(decoder *json.Decoder) {
	defer close(c.doneCh)

	for {
		var msg qmpMessage
		if err := decoder.Decode(&msg); err != nil {
			if atomic.LoadInt32(&c.closed) == 0 {
				c.logger.Debug("QMP reader exiting", zap.Error(err))
			}
			return
		}

		if msg.isResponse() {
			resp := &QMPResponse{
				Return: msg.Return,
				Error:  msg.Error,
			}
			select {
			case c.responseCh <- resp:
			default:
				c.logger.Warn("QMP response channel full, dropping response")
			}
		} else if msg.isEvent() {
			event := QMPEvent{
				Event: msg.Event,
				Data:  msg.Data,
			}
			if msg.Timestamp != nil {
				event.Timestamp = *msg.Timestamp
			}
			c.logger.Debug("QMP event received", zap.String("event", msg.Event))
			select {
			case c.eventsCh <- event:
			default:
				c.logger.Warn("QMP event channel full, dropping event",
					zap.String("event", msg.Event))
			}
		}
	}
}

// Execute sends a QMP command and waits for the response.
// Only one command can be in flight at a time; concurrent calls are serialised.
//
// On success, returns the raw JSON payload from QEMU's "return" field.
// On QMP error, returns a *QMPError (which implements the error interface).
func (c *QMPClient) Execute(ctx context.Context, command string, args interface{}) (json.RawMessage, error) {
	if atomic.LoadInt32(&c.closed) != 0 {
		return nil, ErrClosed
	}

	// Serialise: only one command in flight at a time.
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	// Build and send the command.
	cmd := QMPCommand{
		Execute:   command,
		Arguments: args,
	}

	c.writeMu.Lock()
	err := c.encoder.Encode(cmd)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("sending command %q: %w", command, err)
	}

	// Wait for the response (the reader goroutine routes it here).
	select {
	case resp := <-c.responseCh:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Return, nil
	case <-c.doneCh:
		return nil, errors.New("QMP connection closed while waiting for response")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// QueryStatus sends the "query-status" QMP command and returns the parsed result.
// This is a convenience wrapper around Execute.
func (c *QMPClient) QueryStatus(ctx context.Context) (*QueryStatusResult, error) {
	raw, err := c.Execute(ctx, "query-status", nil)
	if err != nil {
		return nil, err
	}

	var result QueryStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing query-status response: %w", err)
	}
	return &result, nil
}

// Events returns a read-only channel that receives async events from QEMU.
// Events are buffered (capacity 64) — if the consumer doesn't keep up,
// events will be dropped with a warning log.
func (c *QMPClient) Events() <-chan QMPEvent {
	return c.eventsCh
}

// Done returns a channel that is closed when the reader goroutine exits
// (due to connection close, read error, or explicit Close() call).
func (c *QMPClient) Done() <-chan struct{} {
	return c.doneCh
}

// Version returns the QEMU version parsed from the QMP greeting.
// Only valid after a successful Connect().
func (c *QMPClient) Version() QMPVersion {
	return c.version
}

// Close shuts down the QMP client by closing the underlying connection.
// The reader goroutine will detect the closed connection and exit,
// which in turn closes the Done() channel.
//
// Close is safe to call multiple times; subsequent calls are no-ops.
func (c *QMPClient) Close() error {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return nil // already closed
	}

	var err error
	if c.conn != nil {
		err = c.conn.Close()
	}
	return err
}
