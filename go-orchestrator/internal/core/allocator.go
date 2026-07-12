// Package core provides a resource allocator for the DVF orchestrator.
//
// ResourceAllocator tracks CPU and memory budgets for the host and
// enforces limits so the orchestrator does not over-subscribe the machine.
// All operations are safe for concurrent use from multiple goroutines.
//
// Budget initialisation:
//   - TotalCPUs  → from GlobalConfig.VMDefaults or runtime.NumCPU() if unset.
//   - TotalMemMB → from GlobalConfig.VMDefaults or /proc/meminfo if unset.
//
// Usage in the execution engine:
//
//	alloc := core.Allocation{CPUs: vm.AllocatedCPUs, MemMB: vm.AllocatedMemMB}
//	if !allocator.TryAcquire(alloc) {
//	    // re-enqueue with backoff
//	}
//	defer allocator.Release(alloc)
package core

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Allocation is the resource requirement for a single VM.
type Allocation struct {
	CPUs  int
	MemMB int
}

// ResourceBudget is a snapshot of the allocator's current state.
type ResourceBudget struct {
	TotalCPUs  int
	TotalMemMB int
	UsedCPUs   int
	UsedMemMB  int
}

// AvailableCPUs returns the number of unallocated CPU cores.
func (b ResourceBudget) AvailableCPUs() int { return b.TotalCPUs - b.UsedCPUs }

// AvailableMemMB returns the unallocated memory in MiB.
func (b ResourceBudget) AvailableMemMB() int { return b.TotalMemMB - b.UsedMemMB }

// ResourceAllocator manages the host CPU and memory budget.
type ResourceAllocator struct {
	mu     sync.Mutex
	budget ResourceBudget
}

// NewResourceAllocator creates an allocator with the given limits.
// If totalCPUs or totalMemMB is ≤ 0, the value is auto-detected from
// the host (runtime.NumCPU for CPUs, /proc/meminfo ÷ 2 for memory).
func NewResourceAllocator(totalCPUs, totalMemMB int) *ResourceAllocator {
	if totalCPUs <= 0 {
		totalCPUs = runtime.NumCPU()
	}
	if totalMemMB <= 0 {
		totalMemMB = detectHostMemMB()
	}
	return &ResourceAllocator{
		budget: ResourceBudget{
			TotalCPUs:  totalCPUs,
			TotalMemMB: totalMemMB,
		},
	}
}

// TryAcquire atomically reserves the requested resources.
// Returns true if the allocation succeeded, false if the host
// does not have enough headroom right now.
func (r *ResourceAllocator) TryAcquire(a Allocation) bool {
	if a.CPUs < 0 || a.MemMB < 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.budget.UsedCPUs+a.CPUs > r.budget.TotalCPUs {
		return false
	}
	if r.budget.UsedMemMB+a.MemMB > r.budget.TotalMemMB {
		return false
	}

	r.budget.UsedCPUs += a.CPUs
	r.budget.UsedMemMB += a.MemMB
	return true
}

// Release returns the previously acquired resources to the pool.
// It is safe to call Release with a zero Allocation.
func (r *ResourceAllocator) Release(a Allocation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.budget.UsedCPUs -= a.CPUs
	if r.budget.UsedCPUs < 0 {
		r.budget.UsedCPUs = 0
	}
	r.budget.UsedMemMB -= a.MemMB
	if r.budget.UsedMemMB < 0 {
		r.budget.UsedMemMB = 0
	}
}

// CanFit returns true if the given allocation would succeed right now.
// Unlike TryAcquire, it does not actually reserve the resources.
func (r *ResourceAllocator) CanFit(a Allocation) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.budget.UsedCPUs+a.CPUs <= r.budget.TotalCPUs &&
		r.budget.UsedMemMB+a.MemMB <= r.budget.TotalMemMB
}

// Available returns a snapshot of the current budget state.
func (r *ResourceAllocator) Available() ResourceBudget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.budget
}

// SetTotal updates the total budget limits at runtime (e.g., after hot-add).
// Existing allocations are not affected, but the new limit is enforced for
// future TryAcquire calls.
func (r *ResourceAllocator) SetTotal(cpus, memMB int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cpus > 0 {
		r.budget.TotalCPUs = cpus
	}
	if memMB > 0 {
		r.budget.TotalMemMB = memMB
	}
}

// String returns a human-readable summary of the budget, e.g. "CPU 2/8, MEM 1024/16384 MiB".
func (r *ResourceAllocator) String() string {
	b := r.Available()
	return fmt.Sprintf("CPU %d/%d, MEM %d/%d MiB",
		b.UsedCPUs, b.TotalCPUs, b.UsedMemMB, b.TotalMemMB)
}

// ─── host detection helpers ───────────────────────────────────────────────

// detectHostMemMB reads the total physical memory from /proc/meminfo and
// returns 50 % of it as the usable budget (conservative). Falls back to
// 8 192 MiB if /proc/meminfo is unavailable (e.g., non-Linux).
func detectHostMemMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 8192
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// Format: "MemTotal:       32768000 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kbTotal, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			break
		}
		// Convert kB → MiB, use 50 % as the budget
		return int(kbTotal/1024) / 2
	}
	return 8192
}
