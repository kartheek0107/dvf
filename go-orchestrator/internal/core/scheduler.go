// Package core provides the orchestration logic for the DVF orchestrator.
//
// This file implements a simple FIFO scheduler with concurrency limiting.
// It controls how many test runs can execute simultaneously and
// distributes pending work to available execution slots.
package core

import (
	"sync"
)

// Scheduler manages a queue of test runs and enforces concurrency limits.
// When a slot is available and work is pending, Next() returns the next
// test run to execute.
type Scheduler struct {
	mu             sync.Mutex
	maxConcurrent  int
	activeCount    int
	queue          []*TestRun
	notify         chan struct{} // signalled when new work is enqueued
}

// NewScheduler creates a scheduler with the given concurrency limit.
func NewScheduler(maxConcurrent int) *Scheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Scheduler{
		maxConcurrent: maxConcurrent,
		notify:        make(chan struct{}, 1),
	}
}

// Enqueue adds a test run to the pending queue.
func (s *Scheduler) Enqueue(run *TestRun) {
	s.mu.Lock()
	s.queue = append(s.queue, run)
	s.mu.Unlock()

	// Non-blocking signal that work is available
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Next returns the next test run to execute, blocking until a slot is
// available and work is pending. Returns nil if the notify channel is closed.
func (s *Scheduler) Next() *TestRun {
	for {
		s.mu.Lock()
		if s.activeCount < s.maxConcurrent && len(s.queue) > 0 {
			run := s.queue[0]
			s.queue = s.queue[1:]
			s.activeCount++
			s.mu.Unlock()
			return run
		}
		s.mu.Unlock()

		// Wait for notification of new work or a released slot
		_, ok := <-s.notify
		if !ok {
			return nil // scheduler is shutting down
		}
	}
}

// TryNext is a non-blocking version of Next. Returns nil if no work
// is available or concurrency limit is reached.
func (s *Scheduler) TryNext() *TestRun {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeCount < s.maxConcurrent && len(s.queue) > 0 {
		run := s.queue[0]
		s.queue = s.queue[1:]
		s.activeCount++
		return run
	}
	return nil
}

// Release frees one execution slot, allowing the next queued run to proceed.
func (s *Scheduler) Release() {
	s.mu.Lock()
	if s.activeCount > 0 {
		s.activeCount--
	}
	s.mu.Unlock()

	// Signal that a slot is available.
	// Use recover so a concurrent Close() doesn't panic with
	// "send on closed channel" during graceful shutdown.
	defer func() { recover() }()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// QueueLen returns the number of test runs waiting in the queue.
func (s *Scheduler) QueueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// ActiveCount returns the number of currently executing test runs.
func (s *Scheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeCount
}

// Close shuts down the scheduler. Any goroutine blocked on Next() will return nil.
func (s *Scheduler) Close() {
	close(s.notify)
}
