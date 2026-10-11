// Package planlocks provides a mechanism to manage read-write locks for different plan IDs.
// This is useful for ensuring that only one goroutine is creating or modifying a plan at a time,
// while still allowing concurrent read access to plans that are not being modified.
package planlocks

import (
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/google/uuid"
)

// sweepEvery is how often unused locks are removed.
const sweepEvery = 1 * time.Minute

// Group manages read-write locks for different plan IDs.
type Group struct {
	// stop cancels the cleanup loop's context.
	stop context.CancelFunc
	// cleaned is closed when the cleanup loop stops, or when New finds it cannot start it. Close waits on it.
	cleaned     chan struct{}
	mu          sync.Mutex
	createLocks map[uuid.UUID]*sync.RWMutex

	// testAfterLookup, if set, runs after a lock is looked up and before it is taken. Tests use it to sweep in that
	// window.
	testAfterLookup func()
}

// New creates a new Group for managing plan locks. There is a background goroutine that cleans up unused locks every
// minute. If the provided context is canceled or Close is called, the cleanup stops, but the locks keep working: a
// caller holding a lock when the process starts shutting down, such as a final Plan write, must still be able to
// release it.
func New(ctx context.Context) *Group {
	return newGroup(ctx, sweepEvery)
}

// newGroup is New with a chosen sweep interval.
func newGroup(ctx context.Context, every time.Duration) *Group {
	ctx, stop := context.WithCancel(ctx)
	g := &Group{
		stop:        stop,
		cleaned:     make(chan struct{}),
		createLocks: map[uuid.UUID]*sync.RWMutex{},
	}
	// The cleanup loop lasts as long as ctx, so it goes on the default pool, never on a limited pool the caller's
	// Context may carry, where it would hold a slot for its whole life.
	submitted := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			defer close(g.cleaned)
			g.clean(ctx, every)
		},
	)
	// Submit does not run the loop if ctx is already canceled. The loop is then as stopped as it will ever be.
	if !submitted {
		close(g.cleaned)
	}

	return g
}

// Close stops the cleanup loop and waits for it to end. Unused locks are no longer removed, but the locks keep working.
// Close can be called more than once.
func (g *Group) Close() {
	g.stop()
	<-g.cleaned
}

// clean removes unused locks every interval until ctx is canceled.
func (g *Group) clean(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, r := chans.Get(ctx, t.C); r != chans.ResultOK {
			return
		}
		g.sweep()
	}
}

// sweep removes every lock that nobody holds. This is best effort: a held lock is skipped. A removed lock is unlocked
// again, so a caller that looked it up just before the sweep is not left waiting on it; lock() sees that its lock
// was removed and takes the current one instead.
func (g *Group) sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for planID, lock := range g.createLocks {
		if !lock.TryLock() {
			continue
		}
		delete(g.createLocks, planID)
		lock.Unlock()
	}
}

// lock takes the lock for planID with take (Lock or RLock) and returns it. A lock can be swept between being looked
// up and being taken, so after taking it lock checks that it is still the one in the map, and otherwise lets it go
// and tries again. Without that, a caller could hold a lock nobody else can see, and its Unlock would find a
// different lock or none.
func (g *Group) lock(planID uuid.UUID, take, give func(*sync.RWMutex)) {
	for {
		g.mu.Lock()
		lock, exists := g.createLocks[planID]
		if !exists {
			lock = &sync.RWMutex{}
			g.createLocks[planID] = lock
		}
		g.mu.Unlock()

		if g.testAfterLookup != nil {
			g.testAfterLookup()
		}
		take(lock)

		g.mu.Lock()
		current := g.createLocks[planID]
		g.mu.Unlock()
		if current == lock {
			return
		}
		give(lock)
	}
}

// Lock acquires a write lock for the given planID.
func (g *Group) Lock(planID uuid.UUID) {
	g.lock(planID, (*sync.RWMutex).Lock, (*sync.RWMutex).Unlock)
}

// Unlock releases the write lock for the given planID.
func (g *Group) Unlock(planID uuid.UUID) {
	g.mu.Lock()
	lock, exists := g.createLocks[planID]
	g.mu.Unlock()

	if !exists {
		panic("unlocking a planID that was not locked: " + planID.String())
	}
	lock.Unlock()
}

// RLock acquires a read lock for the given planID.
func (g *Group) RLock(planID uuid.UUID) {
	g.lock(planID, (*sync.RWMutex).RLock, (*sync.RWMutex).RUnlock)
}

// RUnlock releases the read lock for the given planID.
func (g *Group) RUnlock(planID uuid.UUID) {
	g.mu.Lock()
	lock, exists := g.createLocks[planID]
	g.mu.Unlock()

	if !exists {
		panic("unlocking a planID that was not locked: " + planID.String())
	}
	lock.RUnlock()
}
