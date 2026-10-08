package planlocks

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/google/uuid"
)

// mode is one way to hold a plan's lock: as a writer or as a reader.
type mode struct {
	name   string
	lock   func(g *Group, id uuid.UUID)
	unlock func(g *Group, id uuid.UUID)
}

var (
	write = mode{name: "write", lock: (*Group).Lock, unlock: (*Group).Unlock}
	read  = mode{name: "read", lock: (*Group).RLock, unlock: (*Group).RUnlock}
)

// isFree reports whether nobody holds the lock g has for id. A lock g does not have is free.
func isFree(g *Group, id uuid.UUID) bool {
	g.mu.Lock()
	lock, ok := g.createLocks[id]
	g.mu.Unlock()
	if !ok {
		return true
	}
	if !lock.TryLock() {
		return false
	}
	lock.Unlock()
	return true
}

func TestLockUnlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mode   mode
		ids    int
		rounds int
	}{
		{name: "Success: a write lock is taken and released", mode: write, ids: 1, rounds: 1},
		{name: "Success: a write lock is taken and released several times", mode: write, ids: 1, rounds: 3},
		{name: "Success: write locks on different plans are taken and released", mode: write, ids: 2, rounds: 1},
		{name: "Success: a read lock is taken and released", mode: read, ids: 1, rounds: 1},
		{name: "Success: a read lock is taken and released several times", mode: read, ids: 1, rounds: 3},
		{name: "Success: read locks on different plans are taken and released", mode: read, ids: 2, rounds: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			g := New(t.Context())
			ids := make([]uuid.UUID, test.ids)
			for i := 0; i < len(ids); i++ {
				ids[i] = uuid.New()
			}

			for r := 0; r < test.rounds; r++ {
				for _, id := range ids {
					test.mode.lock(g, id)
				}
				for _, id := range ids {
					if isFree(g, id) {
						t.Errorf("TestLockUnlock(%s): got plan %s free while its %s lock is held", test.name, id, test.mode.name)
					}
				}
				for _, id := range ids {
					test.mode.unlock(g, id)
				}
			}

			for _, id := range ids {
				if !isFree(g, id) {
					t.Errorf("TestLockUnlock(%s): got plan %s held after its %s lock was released", test.name, id, test.mode.name)
				}
			}
		})
	}
}

func TestUnlockPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode mode
		// setup locks plans before id is unlocked.
		setup     func(g *Group, id uuid.UUID)
		wantPanic bool
	}{
		{
			name:  "Success: unlocking a write-locked plan does not panic",
			mode:  write,
			setup: write.lock,
		},
		{
			name:      "Error: unlocking a plan that was never locked panics",
			mode:      write,
			setup:     func(g *Group, id uuid.UUID) {},
			wantPanic: true,
		},
		{
			name:      "Error: unlocking a plan when only another plan is locked panics",
			mode:      write,
			setup:     func(g *Group, id uuid.UUID) { g.Lock(uuid.New()) },
			wantPanic: true,
		},
		{
			name:  "Success: read unlocking a read-locked plan does not panic",
			mode:  read,
			setup: read.lock,
		},
		{
			name:      "Error: read unlocking a plan that was never locked panics",
			mode:      read,
			setup:     func(g *Group, id uuid.UUID) {},
			wantPanic: true,
		},
		{
			name:      "Error: read unlocking a plan when only another plan is read locked panics",
			mode:      read,
			setup:     func(g *Group, id uuid.UUID) { g.RLock(uuid.New()) },
			wantPanic: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			g := New(t.Context())
			id := uuid.New()
			test.setup(g, id)

			defer func() {
				r := recover()
				switch {
				case r == nil && test.wantPanic:
					t.Errorf("TestUnlockPanic(%s): got no panic, want panic", test.name)
				case r != nil && !test.wantPanic:
					t.Errorf("TestUnlockPanic(%s): got panic %v, want no panic", test.name, r)
				}
			}()
			test.mode.unlock(g, id)
		})
	}
}

func TestConcurrentWriteLocks(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	g := New(ctx)
	id := uuid.New()
	const writers = 10

	var inside, done atomic.Int64
	var overlapped atomic.Bool
	work := context.Pool(ctx).Group()
	for i := 0; i < writers; i++ {
		work.Go(ctx, func(ctx context.Context) error {
			g.Lock(id)
			defer g.Unlock(id)

			if inside.Add(1) != 1 {
				overlapped.Store(true)
			}
			time.Sleep(time.Millisecond) // Widens the window another writer would have to overlap in.
			done.Add(1)
			inside.Add(-1)
			return nil
		})
	}
	if err := work.Wait(ctx); err != nil {
		t.Fatalf("TestConcurrentWriteLocks: got err == %s, want err == nil", err)
	}

	if got := done.Load(); got != writers {
		t.Errorf("TestConcurrentWriteLocks: got %d writers done, want %d", got, writers)
	}
	if overlapped.Load() {
		t.Errorf("TestConcurrentWriteLocks: got two writers holding the lock at once, want one at a time")
	}
}

func TestConcurrentReadLocks(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	g := New(ctx)
	id := uuid.New()
	const readers = 10

	// Every reader holds its lock until all of them hold one, which only happens if they can hold it together.
	var holding atomic.Int64
	all := make(chan struct{})
	release := make(chan struct{})
	work := context.Pool(ctx).Group()
	for i := 0; i < readers; i++ {
		work.Go(ctx, func(ctx context.Context) error {
			g.RLock(id)
			defer g.RUnlock(id)

			if holding.Add(1) == readers {
				close(all)
			}
			<-release
			return nil
		})
	}

	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Errorf("TestConcurrentReadLocks: got %d readers holding the lock at once, want %d", holding.Load(), readers)
	}
	close(release)
	if err := work.Wait(ctx); err != nil {
		t.Errorf("TestConcurrentReadLocks: got err == %s, want err == nil", err)
	}
}

func TestLockExclusion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// hold is taken first and held while try is attempted.
		hold mode
		try  mode
		// otherPlan has try lock a different plan than hold.
		otherPlan   bool
		wantBlocked bool
	}{
		{name: "Success: a write lock blocks another write lock", hold: write, try: write, wantBlocked: true},
		{name: "Success: a write lock blocks a read lock", hold: write, try: read, wantBlocked: true},
		{name: "Success: a read lock blocks a write lock", hold: read, try: write, wantBlocked: true},
		{name: "Success: a read lock does not block another read lock", hold: read, try: read},
		{name: "Success: a write lock does not block a write lock on another plan", hold: write, try: write, otherPlan: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			g := New(ctx)
			held := uuid.New()
			tried := held
			if test.otherPlan {
				tried = uuid.New()
			}

			test.hold.lock(g, held)
			acquired := make(chan struct{})
			context.Pool(ctx).Submit(
				ctx,
				func() {
					test.try.lock(g, tried)
					close(acquired)
					test.try.unlock(g, tried)
				},
			)

			if test.wantBlocked {
				// This wait can only miss a lock that fails to block, never fail one that works.
				select {
				case <-acquired:
					t.Errorf("TestLockExclusion(%s): got the %s lock while the %s lock is held, want it blocked", test.name, test.try.name, test.hold.name)
				case <-time.After(100 * time.Millisecond):
				}
			}
			if !test.wantBlocked {
				select {
				case <-acquired:
				case <-time.After(5 * time.Second):
					t.Errorf("TestLockExclusion(%s): got the %s lock blocked by the %s lock, want it taken", test.name, test.try.name, test.hold.name)
				}
			}

			test.hold.unlock(g, held)
			select {
			case <-acquired:
			case <-time.After(5 * time.Second):
				t.Errorf("TestLockExclusion(%s): got the %s lock still blocked after the %s lock was released", test.name, test.try.name, test.hold.name)
			}
		})
	}
}

func TestConcurrentOperationsDifferentPlanIDs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	g := New(ctx)
	const plans = 10
	const opsPerPlan = 5

	ids := make([]uuid.UUID, plans)
	work := context.Pool(ctx).Group()
	for i := 0; i < plans; i++ {
		ids[i] = uuid.New()
		for j := 0; j < opsPerPlan; j++ {
			work.Go(ctx, func(ctx context.Context) error {
				g.Lock(ids[i])
				time.Sleep(time.Millisecond)
				g.Unlock(ids[i])
				return nil
			})
		}
	}
	if err := work.Wait(ctx); err != nil {
		t.Fatalf("TestConcurrentOperationsDifferentPlanIDs: got err == %s, want err == nil", err)
	}

	for _, id := range ids {
		if !isFree(g, id) {
			t.Errorf("TestConcurrentOperationsDifferentPlanIDs: got plan %s held after every operation finished", id)
		}
	}
}

func TestSweep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// hold takes the lock before the sweep and returns how to let it go.
		hold     func(g *Group, id uuid.UUID) func()
		wantKept bool
	}{
		{
			name:     "Success: an unused lock is removed",
			hold:     func(g *Group, id uuid.UUID) func() { g.Lock(id); g.Unlock(id); return func() {} },
			wantKept: false,
		},
		{
			name:     "Success: a write-held lock is kept",
			hold:     func(g *Group, id uuid.UUID) func() { g.Lock(id); return func() { g.Unlock(id) } },
			wantKept: true,
		},
		{
			name:     "Success: a read-held lock is kept",
			hold:     func(g *Group, id uuid.UUID) func() { g.RLock(id); return func() { g.RUnlock(id) } },
			wantKept: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			g := New(t.Context())
			id := uuid.New()
			release := test.hold(g, id)
			g.sweep()

			g.mu.Lock()
			_, kept := g.createLocks[id]
			g.mu.Unlock()
			if kept != test.wantKept {
				t.Errorf("TestSweep(%s): got lock kept == %v, want %v", test.name, kept, test.wantKept)
			}
			release()
		})
	}
}

// TestLockAcrossSweep is a regression test: a lock swept between being looked up and being taken was taken anyway,
// leaving the caller holding a lock no longer in the Group, so its Unlock panicked (or unlocked someone else's lock).
// Lock and RLock must end up holding the lock the Group has for the plan.
func TestLockAcrossSweep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode mode
	}{
		{
			name: "Success: Lock after its lock was swept holds the Group's lock",
			mode: write,
		},
		{
			name: "Success: RLock after its lock was swept holds the Group's lock",
			mode: read,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			g := New(t.Context())
			id := uuid.New()
			swept := false
			g.testAfterLookup = func() {
				if !swept {
					swept = true
					g.sweep()
				}
			}

			test.mode.lock(g, id)

			g.mu.Lock()
			current, ok := g.createLocks[id]
			g.mu.Unlock()
			if !ok {
				t.Fatalf("TestLockAcrossSweep(%s): the Group has no lock for the plan while it is held", test.name)
			}
			if current.TryLock() {
				t.Errorf("TestLockAcrossSweep(%s): the Group's %s lock is not held", test.name, test.mode.name)
				current.Unlock()
			}
			test.mode.unlock(g, id)
		})
	}
}

// TestClean is a regression test for three bugs. After its first tick the cleanup loop never looked at its context
// again, so it never stopped. Once it did stop, it marked the Group canceled and every Lock and Unlock after that
// panicked, so a canceled process context crashed a Plan write that held a lock through shutdown. And the Vault gives
// the Group a context that is never canceled, with no other way to stop the loop, so every Vault leaked it. Canceling
// the context or calling Close must stop the loop and leave the locks working, including one taken before the stop.
func TestClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// cancelFirst cancels the context before the Group is made, so the loop never starts.
		cancelFirst bool
		// stop stops the loop of a Group made with a context that cancel cancels.
		stop func(g *Group, cancel context.CancelFunc)
	}{
		{
			name: "Success: canceling the context stops the loop and the locks keep working",
			stop: func(g *Group, cancel context.CancelFunc) { cancel() },
		},
		{
			name: "Success: Close stops the loop and the locks keep working",
			stop: func(g *Group, cancel context.CancelFunc) { g.Close() },
		},
		{
			name: "Success: Close twice stops the loop and the locks keep working",
			stop: func(g *Group, cancel context.CancelFunc) { g.Close(); g.Close() },
		},
		{
			name:        "Success: a Group made with a canceled context reports its loop stopped and the locks work",
			cancelFirst: true,
			stop:        func(g *Group, cancel context.CancelFunc) {},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancelFirst {
				cancel()
			}
			g := newGroup(ctx, time.Millisecond)
			time.Sleep(20 * time.Millisecond) // Let the loop tick several times.

			held := uuid.New()
			g.Lock(held)
			test.stop(g, cancel)

			select {
			case <-g.cleaned:
			case <-time.After(5 * time.Second):
				t.Fatalf("TestClean(%s): the cleanup loop did not stop", test.name)
			}

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("TestClean(%s): got panic %v using locks after the loop stopped, want the locks to keep working", test.name, r)
				}
			}()
			g.Unlock(held)
			id := uuid.New()
			g.Lock(id)
			g.Unlock(id)
			g.RLock(id)
			g.RUnlock(id)
		})
	}
}
