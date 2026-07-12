// Package cluster provides the in-memory node registry for DVF orchestrator clustering.
//
// NodeRegistry maintains the set of known orchestrator nodes. It is updated
// via heartbeats (Register / Heartbeat) and periodically swept by Evict to
// mark stale nodes as offline.
//
// Usage:
//
//	registry := cluster.NewNodeRegistry(30 * time.Second)
//	go registry.RunEviction(ctx)
//
//	// On POST /cluster/heartbeat:
//	registry.Register(req)
//
//	// On GET /cluster/nodes:
//	nodes := registry.List()
package cluster

import (
	"context"
	"sync"
	"time"
)

// NodeRegistry is a thread-safe in-memory store of cluster nodes.
type NodeRegistry struct {
	mu         sync.RWMutex
	nodes      map[string]*Node
	heartbeatTTL time.Duration
}

// NewNodeRegistry creates a registry with the given heartbeat TTL.
// A node is considered offline if no heartbeat is received within this duration.
func NewNodeRegistry(heartbeatTTL time.Duration) *NodeRegistry {
	if heartbeatTTL <= 0 {
		heartbeatTTL = 30 * time.Second
	}
	return &NodeRegistry{
		nodes:        make(map[string]*Node),
		heartbeatTTL: heartbeatTTL,
	}
}

// Register upserts a node from an incoming heartbeat payload.
// If the node already exists, its resource stats and LastSeen are updated.
func (r *NodeRegistry) Register(req HeartbeatRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.nodes[req.NodeID]
	if !ok {
		existing = &Node{ID: req.NodeID}
		r.nodes[req.NodeID] = existing
	}

	existing.Hostname   = req.Hostname
	existing.GRPCAddr   = req.GRPCAddr
	existing.Role       = req.Role
	existing.TotalCPUs  = req.TotalCPUs
	existing.TotalMemMB = req.TotalMemMB
	existing.UsedCPUs   = req.UsedCPUs
	existing.UsedMemMB  = req.UsedMemMB
	existing.Version    = req.Version
	existing.LastSeen   = time.Now().UTC()
	existing.Status     = NodeStatusHealthy
}

// Heartbeat refreshes the LastSeen timestamp for an existing node.
// Returns false if the node is not yet registered.
func (r *NodeRegistry) Heartbeat(nodeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.nodes[nodeID]
	if !ok {
		return false
	}
	n.LastSeen = time.Now().UTC()
	n.Status = NodeStatusHealthy
	return true
}

// List returns a snapshot of all known nodes (including offline ones).
func (r *NodeRegistry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, *n)
	}
	return out
}

// ListHealthy returns only nodes whose status is healthy or degraded
// (i.e., not offline) — suitable for work dispatch.
func (r *NodeRegistry) ListHealthy() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []Node
	for _, n := range r.nodes {
		if n.Status != NodeStatusOffline {
			out = append(out, *n)
		}
	}
	return out
}

// Get returns a single node by ID, or false if not found.
func (r *NodeRegistry) Get(nodeID string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n, ok := r.nodes[nodeID]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// Evict marks nodes that have not sent a heartbeat within heartbeatTTL as offline.
// It does NOT remove them from the registry — offline nodes are visible via List()
// for debugging.
func (r *NodeRegistry) Evict() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	evicted := 0
	for _, n := range r.nodes {
		age := now.Sub(n.LastSeen)
		if n.Status != NodeStatusOffline && age > r.heartbeatTTL {
			n.Status = NodeStatusOffline
			evicted++
		}
	}
	return evicted
}

// RunEviction periodically calls Evict until the context is cancelled.
// Suggested interval: heartbeatTTL / 2.
func (r *NodeRegistry) RunEviction(ctx context.Context) {
	ticker := time.NewTicker(r.heartbeatTTL / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Evict()
		}
	}
}

// Count returns the number of nodes in each status bucket.
func (r *NodeRegistry) Count() (healthy, degraded, offline int) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, n := range r.nodes {
		switch n.Status {
		case NodeStatusHealthy:
			healthy++
		case NodeStatusDegraded:
			degraded++
		case NodeStatusOffline:
			offline++
		}
	}
	return
}
