// Package vm provides QEMU virtual machine management for the DVF orchestrator.
//
// This file defines the QMP (QEMU Machine Protocol) wire types.
// QMP is a JSON-based protocol that QEMU exposes over a Unix domain socket.
// Every message is a single JSON object terminated by a newline.
//
// Protocol flow:
//  1. Client connects to the Unix socket
//  2. QEMU sends a greeting:    {"QMP": {"version": {...}, "capabilities": [...]}}
//  3. Client sends:             {"execute": "qmp_capabilities"}
//  4. QEMU responds:            {"return": {}}
//  5. Client is now in command mode — can send any QMP command
//  6. QEMU may send async events at any time: {"event": "...", "data": {...}}
package vm

import (
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Wire types — these map directly to the JSON QEMU sends/receives
// ---------------------------------------------------------------------------

// QMPGreeting is the first message QEMU sends after a client connects.
// Example: {"QMP": {"version": {"qemu": {"major":10,"minor":1,"micro":5}, ...}, "capabilities": ["oob"]}}
type QMPGreeting struct {
	QMP struct {
		Version      QMPVersion `json:"version"`
		Capabilities []string   `json:"capabilities"`
	} `json:"QMP"`
}

// QMPVersion describes the QEMU version from the greeting.
type QMPVersion struct {
	QEMU struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
		Micro int `json:"micro"`
	} `json:"qemu"`
	Package string `json:"package"`
}

// String returns a human-readable version string like "10.1.5".
func (v QMPVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.QEMU.Major, v.QEMU.Minor, v.QEMU.Micro)
}

// QMPCommand is the message format for sending commands to QEMU.
// Example: {"execute": "query-status"} or {"execute": "device_add", "arguments": {"driver": "vfio-pci", ...}}
type QMPCommand struct {
	Execute   string      `json:"execute"`
	Arguments interface{} `json:"arguments,omitempty"`
}

// QMPResponse is QEMU's response to a command.
// Exactly one of Return or Error will be set.
type QMPResponse struct {
	Return json.RawMessage `json:"return,omitempty"` // success payload (can be {} or complex)
	Error  *QMPError       `json:"error,omitempty"`  // error payload
}

// QMPError is the error object inside a QMP error response.
// Example: {"class": "GenericError", "desc": "Device '001' not found"}
type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

// Error implements the error interface.
func (e *QMPError) Error() string {
	return fmt.Sprintf("QMP error [%s]: %s", e.Class, e.Desc)
}

// QMPEvent is an asynchronous event from QEMU.
// Events are sent unprompted — they can arrive between command/response pairs.
// Example: {"event": "SHUTDOWN", "data": {"guest": false, "reason": "host-qmp-quit"}, "timestamp": {...}}
type QMPEvent struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp QMPTimestamp    `json:"timestamp"`
}

// QMPTimestamp is QEMU's timestamp format: seconds + microseconds since epoch.
type QMPTimestamp struct {
	Seconds      int64 `json:"seconds"`
	Microseconds int64 `json:"microseconds"`
}

// Time converts the QMP timestamp to a Go time.Time.
func (t QMPTimestamp) Time() time.Time {
	return time.Unix(t.Seconds, t.Microseconds*1000) // microseconds → nanoseconds
}

// qmpMessage is an internal type for the reader goroutine to distinguish
// between responses (to our commands) and async events.
// QEMU sends three kinds of top-level JSON objects:
//   - Greeting:  has "QMP" key     → only at connect time
//   - Response:  has "return" or "error" key → reply to our command
//   - Event:     has "event" key   → async notification
type qmpMessage struct {
	// Exactly one of these will be populated based on which JSON key is present.
	QMP       json.RawMessage `json:"QMP,omitempty"`       // greeting
	Return    json.RawMessage `json:"return,omitempty"`    // success response
	Error     *QMPError       `json:"error,omitempty"`     // error response
	Event     string          `json:"event,omitempty"`     // async event name
	Data      json.RawMessage `json:"data,omitempty"`      // event data (only with Event)
	Timestamp *QMPTimestamp   `json:"timestamp,omitempty"` // event timestamp
}

// isResponse returns true if this message is a command response (success or error).
func (m *qmpMessage) isResponse() bool {
	return m.Return != nil || m.Error != nil
}

// isEvent returns true if this message is an async event.
func (m *qmpMessage) isEvent() bool {
	return m.Event != ""
}

// ---------------------------------------------------------------------------
// Common QMP command result types
// ---------------------------------------------------------------------------

// QueryStatusResult is the parsed response from "query-status".
// Example: {"running": true, "status": "running"}
type QueryStatusResult struct {
	Running bool   `json:"running"`
	Status  string `json:"status"` // "running", "paused", "prelaunch", "shutdown", etc.
}

// QueryCPUsResult is the parsed response from "query-cpus-fast".
type QueryCPUsResult struct {
	CPUIndex int    `json:"cpu-index"`
	QOMPath  string `json:"qom-path"`
	ThreadID int    `json:"thread-id"`
	Target   string `json:"target"` // e.g., "x86_64"
}

// QueryBlockResult is one entry from "query-block".
type QueryBlockResult struct {
	Device   string `json:"device"`
	Type     string `json:"type"`
	Inserted *struct {
		File                 string `json:"file"`
		BackingFile          string `json:"backing_file,omitempty"`
		DriveType            string `json:"drv"`
		ReadOnly             bool   `json:"ro"`
		EncryptionKeyMissing bool   `json:"encrypted"`
	} `json:"inserted,omitempty"`
}
