package planlocks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLockUnlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: basic lock and unlock",
		},
		{
			name: "Success: lock and unlock same plan ID multiple times",
		},
		{
			name: "Success: lock and unlock different plan IDs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			switch test.name {
			case "Success: basic lock and unlock":
				g.Lock(planID)
				g.Unlock(planID)

			case "Success: lock and unlock same plan ID multiple times":
				g.Lock(planID)
				g.Unlock(planID)
				g.Lock(planID)
				g.Unlock(planID)

			case "Success: lock and unlock different plan IDs":
				planID2 := uuid.New()
				g.Lock(planID)
				g.Lock(planID2)
				g.Unlock(planID)
				g.Unlock(planID2)
			}
		})
	}
}

func TestRLockRUnlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: basic read lock and unlock",
		},
		{
			name: "Success: read lock and unlock same plan ID multiple times",
		},
		{
			name: "Success: read lock and unlock different plan IDs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			switch test.name {
			case "Success: basic read lock and unlock":
				g.RLock(planID)
				g.RUnlock(planID)

			case "Success: read lock and unlock same plan ID multiple times":
				g.RLock(planID)
				g.RUnlock(planID)
				g.RLock(planID)
				g.RUnlock(planID)

			case "Success: read lock and unlock different plan IDs":
				planID2 := uuid.New()
				g.RLock(planID)
				g.RLock(planID2)
				g.RUnlock(planID)
				g.RUnlock(planID2)
			}
		})
	}
}

func TestUnlockPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setupFunc func(*Group, uuid.UUID)
	}{
		{
			name: "Error: unlock without lock panics",
			setupFunc: func(g *Group, planID uuid.UUID) {
				// Don't lock anything
			},
		},
		{
			name: "Error: unlock non-existent plan ID panics",
			setupFunc: func(g *Group, planID uuid.UUID) {
				// Lock a different plan ID
				otherID := uuid.New()
				g.Lock(otherID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			test.setupFunc(g, planID)

			defer func() {
				if r := recover(); r == nil {
					t.Errorf("TestUnlockPanic(%s): expected panic but didn't panic", test.name)
				}
			}()

			g.Unlock(planID)
		})
	}
}

func TestRUnlockPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setupFunc func(*Group, uuid.UUID)
	}{
		{
			name: "Error: read unlock without lock panics",
			setupFunc: func(g *Group, planID uuid.UUID) {
				// Don't lock anything
			},
		},
		{
			name: "Error: read unlock non-existent plan ID panics",
			setupFunc: func(g *Group, planID uuid.UUID) {
				// Lock a different plan ID
				otherID := uuid.New()
				g.RLock(otherID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			test.setupFunc(g, planID)

			defer func() {
				if r := recover(); r == nil {
					t.Errorf("TestRUnlockPanic(%s): expected panic but didn't panic", test.name)
				}
			}()

			g.RUnlock(planID)
		})
	}
}

func TestConcurrentWriteLocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: multiple goroutines with write locks serialize access",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			var counter int64
			var maxConcurrent int64
			var currentConcurrent int64
			const numGoroutines = 10

			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for i := 0; i < numGoroutines; i++ {
				go func() {
					defer wg.Done()

					g.Lock(planID)
					defer g.Unlock(planID)

					// Track concurrent access
					current := atomic.AddInt64(&currentConcurrent, 1)
					if current > atomic.LoadInt64(&maxConcurrent) {
						atomic.StoreInt64(&maxConcurrent, current)
					}

					// Simulate work
					time.Sleep(10 * time.Millisecond)
					atomic.AddInt64(&counter, 1)

					atomic.AddInt64(&currentConcurrent, -1)
				}()
			}

			wg.Wait()

			if counter != numGoroutines {
				t.Errorf("TestConcurrentWriteLocks(%s): counter = %d, want %d", test.name, counter, numGoroutines)
			}

			// With write locks, max concurrent should be 1
			if maxConcurrent != 1 {
				t.Errorf("TestConcurrentWriteLocks(%s): maxConcurrent = %d, want 1", test.name, maxConcurrent)
			}
		})
	}
}

func TestConcurrentReadLocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: multiple goroutines with read locks run concurrently",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			var counter int64
			var maxConcurrent int64
			var currentConcurrent int64
			const numGoroutines = 10

			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for i := 0; i < numGoroutines; i++ {
				go func() {
					defer wg.Done()

					g.RLock(planID)
					defer g.RUnlock(planID)

					// Track concurrent access
					current := atomic.AddInt64(&currentConcurrent, 1)
					if current > atomic.LoadInt64(&maxConcurrent) {
						atomic.StoreInt64(&maxConcurrent, current)
					}

					// Simulate work
					time.Sleep(50 * time.Millisecond)
					atomic.AddInt64(&counter, 1)

					atomic.AddInt64(&currentConcurrent, -1)
				}()
			}

			wg.Wait()

			if counter != numGoroutines {
				t.Errorf("TestConcurrentReadLocks(%s): counter = %d, want %d", test.name, counter, numGoroutines)
			}

			// With read locks, we should see concurrent access
			if maxConcurrent < 2 {
				t.Errorf("TestConcurrentReadLocks(%s): maxConcurrent = %d, want >= 2", test.name, maxConcurrent)
			}
		})
	}
}

func TestMixedReadWriteLocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: write lock blocks read locks",
		},
		{
			name: "Success: read locks block write lock",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			switch test.name {
			case "Success: write lock blocks read locks":
				// Acquire write lock
				g.Lock(planID)

				// Try to acquire read lock in goroutine
				readLockAcquired := make(chan bool)
				go func() {
					g.RLock(planID)
					readLockAcquired <- true
					g.RUnlock(planID)
				}()

				// Read lock should not be acquired while write lock is held
				select {
				case <-readLockAcquired:
					t.Errorf("TestMixedReadWriteLocks(%s): read lock acquired while write lock held", test.name)
				case <-time.After(100 * time.Millisecond):
					// Expected - read lock is blocked
				}

				// Release write lock
				g.Unlock(planID)

				// Now read lock should be acquired
				select {
				case <-readLockAcquired:
					// Expected
				case <-time.After(100 * time.Millisecond):
					t.Errorf("TestMixedReadWriteLocks(%s): read lock not acquired after write lock released", test.name)
				}

			case "Success: read locks block write lock":
				// Acquire read lock
				g.RLock(planID)

				// Try to acquire write lock in goroutine
				writeLockAcquired := make(chan bool)
				go func() {
					g.Lock(planID)
					writeLockAcquired <- true
					g.Unlock(planID)
				}()

				// Write lock should not be acquired while read lock is held
				select {
				case <-writeLockAcquired:
					t.Errorf("TestMixedReadWriteLocks(%s): write lock acquired while read lock held", test.name)
				case <-time.After(100 * time.Millisecond):
					// Expected - write lock is blocked
				}

				// Release read lock
				g.RUnlock(planID)

				// Now write lock should be acquired
				select {
				case <-writeLockAcquired:
					// Expected
				case <-time.After(100 * time.Millisecond):
					t.Errorf("TestMixedReadWriteLocks(%s): write lock not acquired after read lock released", test.name)
				}
			}
		})
	}
}

func TestMultiplePlanIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: different plan IDs have independent locks",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID1 := uuid.New()
			planID2 := uuid.New()

			// Lock planID1
			g.Lock(planID1)

			// Should be able to lock planID2 concurrently
			lockAcquired := make(chan bool)
			go func() {
				g.Lock(planID2)
				lockAcquired <- true
				g.Unlock(planID2)
			}()

			select {
			case <-lockAcquired:
				// Expected - different plan IDs have independent locks
			case <-time.After(100 * time.Millisecond):
				t.Errorf("TestMultiplePlanIDs(%s): could not acquire lock for planID2 while planID1 is locked", test.name)
			}

			g.Unlock(planID1)
		})
	}
}

func TestConcurrentOperationsDifferentPlanIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: concurrent operations on different plan IDs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			const numPlanIDs = 10
			const opsPerPlan = 5

			var wg sync.WaitGroup
			wg.Add(numPlanIDs * opsPerPlan)

			for i := 0; i < numPlanIDs; i++ {
				planID := uuid.New()
				for j := 0; j < opsPerPlan; j++ {
					go func() {
						defer wg.Done()

						g.Lock(planID)
						time.Sleep(10 * time.Millisecond)
						g.Unlock(planID)
					}()
				}
			}

			// Wait with a timeout
			done := make(chan bool)
			go func() {
				wg.Wait()
				done <- true
			}()

			select {
			case <-done:
				// Expected - all operations completed
			case <-time.After(5 * time.Second):
				t.Errorf("TestConcurrentOperationsDifferentPlanIDs(%s): timed out waiting for operations to complete", test.name)
			}
		})
	}
}

func TestMultipleReadLocksSamePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "Success: multiple read locks can be held simultaneously on same plan",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := New(t.Context())
			planID := uuid.New()

			// Acquire first read lock
			g.RLock(planID)

			// Try to acquire second read lock in goroutine
			readLockAcquired := make(chan bool)
			go func() {
				g.RLock(planID)
				readLockAcquired <- true
				g.RUnlock(planID)
			}()

			// Second read lock should be acquired immediately
			select {
			case <-readLockAcquired:
				// Expected - multiple read locks allowed
			case <-time.After(100 * time.Millisecond):
				t.Errorf("TestMultipleReadLocksSamePlan(%s): second read lock not acquired", test.name)
			}

			g.RUnlock(planID)
		})
	}
}
