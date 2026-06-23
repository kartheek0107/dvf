// Package core provides the orchestration logic for the DVF orchestrator.
//
// This file implements the AgentCoordinator — it manages per-VM command
// queues and result channels, allowing the execution engine to communicate
// with guest agents via a synchronous request/response pattern.
package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// AgentCoordinator manages the lifecycle of guest agent communication.
// Each VM has a command queue (host → agent) and result channels
// (agent → host). The execution engine pushes commands and waits
// for results; the agent gRPC service reads commands and reports results.
type AgentCoordinator struct {
	mu     sync.RWMutex
	agents map[string]*agentState // key: VM ID

	logger interface {
		Info(msg string, fields ...interface{})
	}
}

// agentState tracks the state of a single guest agent.
type agentState struct {
	vmID          string
	agentID       string
	ready         chan struct{}           // closed when agent registers
	readyOnce     sync.Once
	commands      chan *AgentCommand      // pending commands for this agent
	resultWaiters map[string]chan *AgentResult // key: command ID
	mu            sync.Mutex
}

// NewAgentCoordinator creates a new agent coordinator.
func NewAgentCoordinator() *AgentCoordinator {
	return &AgentCoordinator{
		agents: make(map[string]*agentState),
	}
}

// RegisterVM prepares the coordinator to receive an agent for the given VM.
// Must be called before the VM boots.
func (ac *AgentCoordinator) RegisterVM(vmID string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	ac.agents[vmID] = &agentState{
		vmID:          vmID,
		ready:         make(chan struct{}),
		commands:      make(chan *AgentCommand, 16),
		resultWaiters: make(map[string]chan *AgentResult),
	}
}

// UnregisterVM cleans up agent state for a VM that is being destroyed.
func (ac *AgentCoordinator) UnregisterVM(vmID string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if state, ok := ac.agents[vmID]; ok {
		close(state.commands)
		delete(ac.agents, vmID)
	}
}

// NotifyAgentReady signals that the agent in the given VM has registered
// and is ready to receive commands.
func (ac *AgentCoordinator) NotifyAgentReady(vmID string, agentID string) error {
	ac.mu.RLock()
	state, ok := ac.agents[vmID]
	ac.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no agent state for VM %s", vmID)
	}

	state.agentID = agentID
	state.readyOnce.Do(func() {
		close(state.ready)
	})
	return nil
}

// WaitForAgent blocks until the agent in the given VM registers, or
// until the context is cancelled.
func (ac *AgentCoordinator) WaitForAgent(ctx context.Context, vmID string) error {
	ac.mu.RLock()
	state, ok := ac.agents[vmID]
	ac.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no agent state for VM %s", vmID)
	}

	select {
	case <-state.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for agent in VM %s: %w", vmID, ctx.Err())
	}
}

// SendCommand sends a command to the agent in the given VM and waits
// for the result. This is called by the execution engine.
func (ac *AgentCoordinator) SendCommand(ctx context.Context, vmID string, cmd *AgentCommand) (*AgentResult, error) {
	ac.mu.RLock()
	state, ok := ac.agents[vmID]
	ac.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no agent state for VM %s", vmID)
	}

	// Create a result waiter channel
	resultCh := make(chan *AgentResult, 1)
	state.mu.Lock()
	state.resultWaiters[cmd.ID] = resultCh
	state.mu.Unlock()

	defer func() {
		state.mu.Lock()
		delete(state.resultWaiters, cmd.ID)
		state.mu.Unlock()
	}()

	// Enqueue the command
	select {
	case state.commands <- cmd:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Wait for the result
	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetNextCommand is called by the agent gRPC service to retrieve the
// next pending command for a given VM. Blocks until a command is available
// or the context is cancelled.
func (ac *AgentCoordinator) GetNextCommand(ctx context.Context, vmID string) (*AgentCommand, error) {
	ac.mu.RLock()
	state, ok := ac.agents[vmID]
	ac.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no agent state for VM %s", vmID)
	}

	select {
	case cmd, ok := <-state.commands:
		if !ok {
			return nil, fmt.Errorf("command channel closed for VM %s", vmID)
		}
		return cmd, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DeliverResult is called by the agent gRPC service when an agent
// reports the result of a command. It unblocks the SendCommand caller.
func (ac *AgentCoordinator) DeliverResult(vmID string, result *AgentResult) error {
	ac.mu.RLock()
	state, ok := ac.agents[vmID]
	ac.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no agent state for VM %s", vmID)
	}

	state.mu.Lock()
	waiter, ok := state.resultWaiters[result.CommandID]
	state.mu.Unlock()

	if !ok {
		return fmt.Errorf("no waiter for command %s in VM %s", result.CommandID, vmID)
	}

	select {
	case waiter <- result:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout delivering result for command %s", result.CommandID)
	}
}
