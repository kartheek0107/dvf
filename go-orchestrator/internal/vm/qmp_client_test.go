package vm

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock QMP Server
// ---------------------------------------------------------------------------
// This creates a fake QEMU QMP server on a Unix socket for testing.
// It speaks the QMP protocol: sends a greeting, accepts qmp_capabilities,
// and responds to commands.

type mockQMPServer struct {
	socketPath string
	listener   net.Listener
	conn       net.Conn
	encoder    *json.Encoder
	decoder    *json.Decoder
}

func newMockQMPServer(t *testing.T) *mockQMPServer {
	t.Helper()

	// Create a temp directory for the socket
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "qmp.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create mock QMP listener: %v", err)
	}

	return &mockQMPServer{
		socketPath: socketPath,
		listener:   listener,
	}
}

// accept waits for a client connection and performs the QMP handshake:
// 1. Send greeting
// 2. Wait for qmp_capabilities
// 3. Send success response
func (m *mockQMPServer) accept() {
	conn, err := m.listener.Accept()
	if err != nil {
		panic(err)
	}
	m.conn = conn
	m.encoder = json.NewEncoder(conn)
	m.decoder = json.NewDecoder(conn)

	// Send QMP greeting
	greeting := map[string]interface{}{
		"QMP": map[string]interface{}{
			"version": map[string]interface{}{
				"qemu": map[string]interface{}{
					"major": 10,
					"minor": 1,
					"micro": 5,
				},
				"package": "test",
			},
			"capabilities": []string{"oob"},
		},
	}
	if err := m.encoder.Encode(greeting); err != nil {
		panic(err)
	}

	// Read qmp_capabilities command
	var cmd QMPCommand
	if err := m.decoder.Decode(&cmd); err != nil {
		panic(err)
	}
	if cmd.Execute != "qmp_capabilities" {
		panic("expected qmp_capabilities command")
	}

	// Send success response
	if err := m.encoder.Encode(map[string]interface{}{"return": map[string]interface{}{}}); err != nil {
		panic(err)
	}
}

// readCommand reads the next command from the client.
func (m *mockQMPServer) readCommand() QMPCommand {
	var cmd QMPCommand
	if err := m.decoder.Decode(&cmd); err != nil {
		panic(err)
	}
	return cmd
}

// sendResponse sends a success response with the given payload.
func (m *mockQMPServer) sendResponse(payload interface{}) {
	resp := map[string]interface{}{"return": payload}
	if err := m.encoder.Encode(resp); err != nil {
		panic(err)
	}
}

// sendError sends an error response.
func (m *mockQMPServer) sendError(class, desc string) {
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"class": class,
			"desc":  desc,
		},
	}
	if err := m.encoder.Encode(resp); err != nil {
		panic(err)
	}
}

// sendEvent sends an async event.
func (m *mockQMPServer) sendEvent(name string, data interface{}) {
	event := map[string]interface{}{
		"event": name,
		"data":  data,
		"timestamp": map[string]interface{}{
			"seconds":      time.Now().Unix(),
			"microseconds": 0,
		},
	}
	if err := m.encoder.Encode(event); err != nil {
		panic(err)
	}
}

// close shuts down the mock server.
func (m *mockQMPServer) close() {
	if m.conn != nil {
		m.conn.Close()
	}
	m.listener.Close()
}

// testLogger creates a no-op logger for tests.
func testLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestQMPConnect verifies the full connection handshake:
// dial → greeting → qmp_capabilities → ready
func TestQMPConnect(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	// Run the mock server handshake in background
	done := make(chan struct{})
	go func() {
		mock.accept()
		close(done)
	}()

	// Connect the client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	<-done

	// Verify version was parsed from greeting
	v := client.Version()
	if v.QEMU.Major != 10 || v.QEMU.Minor != 1 || v.QEMU.Micro != 5 {
		t.Errorf("version mismatch: got %s, want 10.1.5", v.String())
	}
}

// TestQMPQueryStatus verifies sending query-status and parsing the response.
func TestQMPQueryStatus(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Mock server: read the query-status command and respond
	go func() {
		cmd := mock.readCommand()
		if cmd.Execute != "query-status" {
			t.Errorf("expected query-status, got %q", cmd.Execute)
		}
		mock.sendResponse(map[string]interface{}{
			"running": true,
			"status":  "running",
		})
	}()

	// Client: send query-status
	result, err := client.QueryStatus(ctx)
	if err != nil {
		t.Fatalf("QueryStatus failed: %v", err)
	}

	if !result.Running {
		t.Error("expected running=true")
	}
	if result.Status != "running" {
		t.Errorf("expected status=running, got %q", result.Status)
	}
}

// TestQMPCommandError verifies that QMP errors are properly propagated.
func TestQMPCommandError(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Mock server: respond with an error
	go func() {
		mock.readCommand()
		mock.sendError("GenericError", "Device 'foo' not found")
	}()

	_, err := client.Execute(ctx, "device_del", map[string]interface{}{"id": "foo"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify the error message contains useful info
	qmpErr, ok := err.(*QMPError)
	if !ok {
		t.Fatalf("expected *QMPError, got %T: %v", err, err)
	}
	if qmpErr.Class != "GenericError" {
		t.Errorf("expected error class GenericError, got %q", qmpErr.Class)
	}
}

// TestQMPAsyncEvents verifies that async events from QEMU are received.
func TestQMPAsyncEvents(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Send an async event from the mock server
	mock.sendEvent("SHUTDOWN", map[string]interface{}{
		"guest":  false,
		"reason": "host-qmp-quit",
	})

	// Read the event on the client side
	select {
	case event := <-client.Events():
		if event.Event != "SHUTDOWN" {
			t.Errorf("expected SHUTDOWN event, got %q", event.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestQMPEventsDuringCommand verifies that events arriving between a
// command request and response don't break anything.
func TestQMPEventsDuringCommand(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Mock server: read a command, send an event FIRST, then the response
	go func() {
		cmd := mock.readCommand()
		if cmd.Execute != "query-status" {
			t.Errorf("expected query-status, got %q", cmd.Execute)
		}

		// Send an event before the response — this is what makes QMP tricky
		mock.sendEvent("RTC_CHANGE", map[string]interface{}{"offset": 0})

		// Small delay to ensure event is processed first
		time.Sleep(50 * time.Millisecond)

		// Now send the actual response
		mock.sendResponse(map[string]interface{}{
			"running": true,
			"status":  "running",
		})
	}()

	// Client should still get the correct response
	result, err := client.QueryStatus(ctx)
	if err != nil {
		t.Fatalf("QueryStatus failed: %v", err)
	}
	if !result.Running {
		t.Error("expected running=true")
	}

	// And the event should be available on the events channel
	select {
	case event := <-client.Events():
		if event.Event != "RTC_CHANGE" {
			t.Errorf("expected RTC_CHANGE event, got %q", event.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// TestQMPConnectionTimeout verifies that Connect respects context cancellation.
func TestQMPConnectionTimeout(t *testing.T) {
	// Use a socket path that doesn't exist — dial will fail
	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	client := NewQMPClient(socketPath, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		client.Close()
		t.Fatal("expected error connecting to non-existent socket")
	}
}

// TestQMPClose verifies that Close() cleanly shuts down the reader goroutine.
func TestQMPClose(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close should not hang — the reader goroutine should exit cleanly
	err := client.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify that the Done channel is closed
	select {
	case <-client.Done():
		// good — reader goroutine has exited
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Done channel")
	}
}

// TestQMPExecuteAfterClose verifies that Execute returns an error after Close.
func TestQMPExecuteAfterClose(t *testing.T) {
	mock := newMockQMPServer(t)
	defer mock.close()

	client := NewQMPClient(mock.socketPath, testLogger())

	go mock.accept()

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	client.Close()

	// Execute should fail after close
	_, err := client.Execute(ctx, "query-status", nil)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

// Ensure unused imports don't cause issues
var _ = os.Remove
