// Package core provides the domain types used throughout the DVF orchestrator.
package core

import "time"

// --- Enums ---

// TestRunStatus represents the lifecycle state of a test run.
type TestRunStatus string

const (
	TestRunStatusPending   TestRunStatus = "PENDING"
	TestRunStatusQueued    TestRunStatus = "QUEUED"
	TestRunStatusRunning   TestRunStatus = "RUNNING"
	TestRunStatusPassed    TestRunStatus = "PASSED"
	TestRunStatusFailed    TestRunStatus = "FAILED"
	TestRunStatusErrored   TestRunStatus = "ERRORED"
	TestRunStatusCancelled TestRunStatus = "CANCELLED"
	TestRunStatusTimeout   TestRunStatus = "TIMEOUT"
)

// VMStatus represents the lifecycle state of a virtual machine.
type VMStatus string

const (
	VMStatusCreating    VMStatus = "CREATING"
	VMStatusBooting     VMStatus = "BOOTING"
	VMStatusReady       VMStatus = "READY"
	VMStatusRunningTest VMStatus = "RUNNING_TEST"
	VMStatusStopping    VMStatus = "STOPPING"
	VMStatusStopped     VMStatus = "STOPPED"
	VMStatusError       VMStatus = "ERROR"
	VMStatusDestroyed   VMStatus = "DESTROYED"
)

// AgentStatus represents the health state of a guest agent.
type AgentStatus string

const (
	AgentStatusUnknown     AgentStatus = "UNKNOWN"
	AgentStatusInitializing AgentStatus = "INITIALIZING"
	AgentStatusReady       AgentStatus = "READY"
	AgentStatusExecuting   AgentStatus = "EXECUTING"
	AgentStatusReporting   AgentStatus = "REPORTING"
	AgentStatusDead        AgentStatus = "DEAD"
)

// DeviceType identifies which custom QEMU device is under test.
type DeviceType string

const (
	DeviceTypeDvfGPU     DeviceType = "dvf-gpu"
	DeviceTypeDvfAIAccel DeviceType = "dvf-ai-accel"
)

// TestCategory classifies the kind of validation test.
type TestCategory string

const (
	TestCategoryReadWrite       TestCategory = "read_write"
	TestCategoryInterrupt       TestCategory = "interrupt_handling"
	TestCategoryErrorInjection  TestCategory = "error_injection"
	TestCategoryStress          TestCategory = "stress_performance"
	TestCategoryPowerManagement TestCategory = "power_management"
	TestCategoryConcurrency     TestCategory = "concurrency"
	TestCategoryDataIntegrity   TestCategory = "data_integrity"
)

// --- Domain Models ---

// TestRun represents a single test execution lifecycle.
type TestRun struct {
	ID           string        `json:"id"`
	DeviceID     string        `json:"device_id"`
	TestSuiteID  string        `json:"test_suite_id"`
	Status       TestRunStatus `json:"status"`
	VMID         string        `json:"vm_id,omitempty"`
	Priority     int           `json:"priority"`
	CreatedAt    time.Time     `json:"created_at"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
	DurationMs   int64         `json:"duration_ms,omitempty"`
	RequestedBy  string        `json:"requested_by,omitempty"`
	Tags         []string      `json:"tags,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
}

// TestResult holds the outcome of a single test case within a test run.
type TestResult struct {
	ID          string       `json:"id"`
	TestRunID   string       `json:"test_run_id"`
	TestName    string       `json:"test_name"`
	Category    TestCategory `json:"category"`
	Status      TestRunStatus `json:"status"`
	DurationMs  int64        `json:"duration_ms"`
	Message     string       `json:"message,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	Logs        string       `json:"logs,omitempty"`
	CompletedAt time.Time    `json:"completed_at"`
}

// VMInstance represents a running QEMU virtual machine.
type VMInstance struct {
	ID              string     `json:"id"`
	Status          VMStatus   `json:"status"`
	DeviceID        string     `json:"device_id"`
	QMPSocketPath   string     `json:"qmp_socket_path"`
	SerialPorts     []string   `json:"serial_ports"`
	PID             int        `json:"pid,omitempty"`
	AllocatedCPUs   int        `json:"allocated_cpus"`
	AllocatedMemMB  int        `json:"allocated_mem_mb"`
	ImagePath       string     `json:"image_path"`
	OverlayPath     string     `json:"overlay_path,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	AgentStatus     AgentStatus `json:"agent_status"`
	LastHeartbeat   *time.Time `json:"last_heartbeat,omitempty"`
	CurrentTestRunID string    `json:"current_test_run_id,omitempty"`
}

// TestSuite defines a collection of tests targeting a specific device and category.
type TestSuite struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	DeviceID    string       `json:"device_id"`
	Category    TestCategory `json:"category"`
	Description string       `json:"description"`
	BinaryPath  string       `json:"binary_path"`
	Timeout     int          `json:"timeout_seconds"`
	Tags        []string     `json:"tags,omitempty"`
}

// SubmitTestRunRequest is the input for submitting a new test run.
type SubmitTestRunRequest struct {
	DeviceID    string   `json:"device_id"`
	TestSuiteID string   `json:"test_suite_id"`
	Priority    int      `json:"priority,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	RequestedBy string   `json:"requested_by,omitempty"`
}

// ListTestRunsRequest is the input for listing test runs with optional filters.
type ListTestRunsRequest struct {
	DeviceID string        `json:"device_id,omitempty"`
	Status   TestRunStatus `json:"status,omitempty"`
	Limit    int           `json:"limit,omitempty"`
	Offset   int           `json:"offset,omitempty"`
}

// ListVMsRequest is the input for listing VMs with optional filters.
type ListVMsRequest struct {
	Status   VMStatus `json:"status,omitempty"`
	DeviceID string   `json:"device_id,omitempty"`
}
