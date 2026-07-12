// Package cluster provides the node registry and heartbeat management for
// the DVF orchestrator's multi-node infrastructure.
//
// Each DVF orchestrator process is a "node". Nodes self-register by posting
// to the leader's /cluster/heartbeat endpoint. The NodeRegistry evicts nodes
// that have not sent a heartbeat within the configured TTL.
package cluster

import (
	"time"
)

// NodeRole identifies whether this node leads or follows.
type NodeRole string

const (
	NodeRoleLeader NodeRole = "leader"
	NodeRoleWorker NodeRole = "worker"
)

// NodeStatus is the health state of a cluster member.
type NodeStatus string

const (
	NodeStatusHealthy  NodeStatus = "healthy"
	NodeStatusDegraded NodeStatus = "degraded"
	NodeStatusOffline  NodeStatus = "offline"
)

// Node represents a single DVF orchestrator process participating in the cluster.
type Node struct {
	// ID is a stable unique identifier for this node (e.g. hostname:grpc_port or UUID).
	ID string `json:"id"`

	// Hostname is the DNS name or IP of the host running this node.
	Hostname string `json:"hostname"`

	// GRPCAddr is the address:port where this node's gRPC server is listening.
	GRPCAddr string `json:"grpc_addr"`

	// Role is either "leader" or "worker".
	Role NodeRole `json:"role"`

	// TotalCPUs and TotalMemMB are the advertised host resource capacity.
	TotalCPUs  int `json:"total_cpus"`
	TotalMemMB int `json:"total_mem_mb"`

	// UsedCPUs and UsedMemMB are the node's current resource utilisation.
	// Updated via heartbeat payloads.
	UsedCPUs  int `json:"used_cpus"`
	UsedMemMB int `json:"used_mem_mb"`

	// LastSeen is the timestamp of the most recent heartbeat received.
	LastSeen time.Time `json:"last_seen"`

	// Status reflects the node's computed health at the time of last eviction pass.
	Status NodeStatus `json:"status"`

	// Version is the orchestrator binary version reported by the node.
	Version string `json:"version,omitempty"`
}

// HeartbeatRequest is the JSON payload a node sends to POST /cluster/heartbeat.
type HeartbeatRequest struct {
	NodeID     string   `json:"node_id"`
	Hostname   string   `json:"hostname"`
	GRPCAddr   string   `json:"grpc_addr"`
	Role       NodeRole `json:"role"`
	TotalCPUs  int      `json:"total_cpus"`
	TotalMemMB int      `json:"total_mem_mb"`
	UsedCPUs   int      `json:"used_cpus"`
	UsedMemMB  int      `json:"used_mem_mb"`
	Version    string   `json:"version,omitempty"`
}
