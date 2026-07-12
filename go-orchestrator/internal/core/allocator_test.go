package core

import (
	"sync"
	"testing"
)

func TestAllocator_BasicAcquireRelease(t *testing.T) {
	a := NewResourceAllocator(8, 1024)

	alloc := Allocation{CPUs: 2, MemMB: 256}
	if !a.TryAcquire(alloc) {
		t.Fatal("expected acquire to succeed")
	}

	b := a.Available()
	if b.UsedCPUs != 2 || b.UsedMemMB != 256 {
		t.Errorf("unexpected used: CPU %d, Mem %d", b.UsedCPUs, b.UsedMemMB)
	}

	a.Release(alloc)
	b = a.Available()
	if b.UsedCPUs != 0 || b.UsedMemMB != 0 {
		t.Errorf("expected zeroed usage after release, got CPU %d, Mem %d", b.UsedCPUs, b.UsedMemMB)
	}
}

func TestAllocator_CPUOversubscriptionBlocked(t *testing.T) {
	a := NewResourceAllocator(4, 8192)

	if !a.TryAcquire(Allocation{CPUs: 4, MemMB: 100}) {
		t.Fatal("first acquire should succeed")
	}
	// Now at max CPUs
	if a.TryAcquire(Allocation{CPUs: 1, MemMB: 100}) {
		t.Error("second acquire should fail — CPUs exhausted")
	}
}

func TestAllocator_MemOversubscriptionBlocked(t *testing.T) {
	a := NewResourceAllocator(16, 512)

	if !a.TryAcquire(Allocation{CPUs: 1, MemMB: 512}) {
		t.Fatal("first acquire should succeed")
	}
	if a.TryAcquire(Allocation{CPUs: 1, MemMB: 1}) {
		t.Error("second acquire should fail — memory exhausted")
	}
}

func TestAllocator_CanFitDoesNotReserve(t *testing.T) {
	a := NewResourceAllocator(4, 1024)
	alloc := Allocation{CPUs: 2, MemMB: 512}

	if !a.CanFit(alloc) {
		t.Fatal("CanFit should return true")
	}
	// CanFit must not change Used counters
	b := a.Available()
	if b.UsedCPUs != 0 || b.UsedMemMB != 0 {
		t.Errorf("CanFit changed allocation state: CPU %d, Mem %d", b.UsedCPUs, b.UsedMemMB)
	}
}

func TestAllocator_ReleaseUnderflowSafe(t *testing.T) {
	a := NewResourceAllocator(4, 512)
	// Release without acquire — must not go negative
	a.Release(Allocation{CPUs: 5, MemMB: 1000})

	b := a.Available()
	if b.UsedCPUs < 0 || b.UsedMemMB < 0 {
		t.Errorf("underflow: CPU %d, Mem %d", b.UsedCPUs, b.UsedMemMB)
	}
}

func TestAllocator_ZeroAllocationAlwaysSucceeds(t *testing.T) {
	a := NewResourceAllocator(1, 128)
	// Fill it up
	_ = a.TryAcquire(Allocation{CPUs: 1, MemMB: 128})
	// Zero-size alloc should still succeed
	if !a.TryAcquire(Allocation{CPUs: 0, MemMB: 0}) {
		t.Error("zero allocation should always succeed")
	}
}

func TestAllocator_ConcurrentSafety(t *testing.T) {
	// Fire 50 goroutines all trying to acquire 1 CPU / 64 MiB on a 4 / 256 budget.
	// At most 4 should succeed simultaneously.
	a := NewResourceAllocator(4, 256)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		maxSeen int
		current int
	)

	const goroutines = 50
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			alloc := Allocation{CPUs: 1, MemMB: 64}
			if a.TryAcquire(alloc) {
				mu.Lock()
				current++
				if current > maxSeen {
					maxSeen = current
				}
				mu.Unlock()

				// hold briefly then release
				a.Release(alloc)

				mu.Lock()
				current--
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if maxSeen > 4 {
		t.Errorf("concurrent over-subscription: max %d simultaneous allocations (limit 4)", maxSeen)
	}
}

func TestAllocator_SetTotal(t *testing.T) {
	a := NewResourceAllocator(4, 512)
	a.SetTotal(8, 1024)
	b := a.Available()
	if b.TotalCPUs != 8 || b.TotalMemMB != 1024 {
		t.Errorf("SetTotal did not update limits: %+v", b)
	}
}

func TestAllocator_String(t *testing.T) {
	a := NewResourceAllocator(8, 2048)
	_ = a.TryAcquire(Allocation{CPUs: 2, MemMB: 512})
	s := a.String()
	if s == "" {
		t.Error("String should not be empty")
	}
	// Sanity: should contain numbers
	if s != "CPU 2/8, MEM 512/2048 MiB" {
		t.Errorf("unexpected String output: %q", s)
	}
}
