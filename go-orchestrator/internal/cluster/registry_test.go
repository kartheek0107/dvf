package cluster

import (
	"testing"
	"time"
	"context"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewNodeRegistry(30 * time.Second)
	r.Register(HeartbeatRequest{
		NodeID:   "node-1",
		Hostname: "host1",
		GRPCAddr: "host1:50051",
		Role:     NodeRoleLeader,
	})
	r.Register(HeartbeatRequest{
		NodeID:   "node-2",
		Hostname: "host2",
		GRPCAddr: "host2:50051",
		Role:     NodeRoleWorker,
	})

	nodes := r.List()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestRegistry_Heartbeat_Refresh(t *testing.T) {
	r := NewNodeRegistry(30 * time.Second)
	r.Register(HeartbeatRequest{NodeID: "n1"})

	before, _ := r.Get("n1")
	time.Sleep(5 * time.Millisecond)
	ok := r.Heartbeat("n1")
	if !ok {
		t.Fatal("Heartbeat should return true for known node")
	}
	after, _ := r.Get("n1")

	if !after.LastSeen.After(before.LastSeen) {
		t.Error("LastSeen should have advanced after heartbeat")
	}
}

func TestRegistry_Heartbeat_UnknownNode(t *testing.T) {
	r := NewNodeRegistry(30 * time.Second)
	if r.Heartbeat("nonexistent") {
		t.Error("Heartbeat should return false for unknown node")
	}
}

func TestRegistry_Eviction(t *testing.T) {
	// Very short TTL so eviction triggers immediately in test
	r := NewNodeRegistry(10 * time.Millisecond)
	r.Register(HeartbeatRequest{NodeID: "stale-node"})

	time.Sleep(20 * time.Millisecond)
	evicted := r.Evict()
	if evicted != 1 {
		t.Errorf("expected 1 eviction, got %d", evicted)
	}

	node, ok := r.Get("stale-node")
	if !ok {
		t.Fatal("node should still exist in registry after eviction")
	}
	if node.Status != NodeStatusOffline {
		t.Errorf("expected status offline, got %s", node.Status)
	}
}

func TestRegistry_ListHealthy_ExcludesOffline(t *testing.T) {
	r := NewNodeRegistry(10 * time.Millisecond)
	r.Register(HeartbeatRequest{NodeID: "fresh"})
	r.Register(HeartbeatRequest{NodeID: "stale"})

	time.Sleep(20 * time.Millisecond)
	// Refresh only "fresh"
	r.Register(HeartbeatRequest{NodeID: "fresh"})
	r.Evict()

	healthy := r.ListHealthy()
	if len(healthy) != 1 || healthy[0].ID != "fresh" {
		t.Errorf("expected only 'fresh' node in healthy list, got %+v", healthy)
	}
}

func TestRegistry_Upsert_UpdatesFields(t *testing.T) {
	r := NewNodeRegistry(30 * time.Second)
	r.Register(HeartbeatRequest{
		NodeID:    "n1",
		TotalCPUs: 4,
		UsedCPUs:  1,
	})
	// Re-register with updated stats
	r.Register(HeartbeatRequest{
		NodeID:    "n1",
		TotalCPUs: 8,
		UsedCPUs:  3,
	})

	n, _ := r.Get("n1")
	if n.TotalCPUs != 8 || n.UsedCPUs != 3 {
		t.Errorf("expected updated stats, got CPUs total=%d used=%d", n.TotalCPUs, n.UsedCPUs)
	}
}

func TestRegistry_Count(t *testing.T) {
	r := NewNodeRegistry(10 * time.Millisecond)
	r.Register(HeartbeatRequest{NodeID: "a"})
	r.Register(HeartbeatRequest{NodeID: "b"})
	r.Register(HeartbeatRequest{NodeID: "c"})

	time.Sleep(20 * time.Millisecond)
	// Refresh a and b; c becomes stale
	r.Register(HeartbeatRequest{NodeID: "a"})
	r.Register(HeartbeatRequest{NodeID: "b"})
	r.Evict()

	healthy, degraded, offline := r.Count()
	if healthy != 2 || degraded != 0 || offline != 1 {
		t.Errorf("expected 2/0/1, got %d/%d/%d", healthy, degraded, offline)
	}
}

func TestRegistry_RunEviction_ContextCancel(t *testing.T) {
	r := NewNodeRegistry(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.RunEviction(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Error("RunEviction did not exit after context cancel")
	}
}
