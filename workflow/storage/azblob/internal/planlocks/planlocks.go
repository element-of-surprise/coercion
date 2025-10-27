// Package planlocks provides a mechanism to manage read-write locks for different plan IDs.
// This is useful for ensuring that only one goroutine is creating or modifying a plan at a time,
// while still allowing concurrent read access to plans that are not being modified.
package planlocks

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/google/uuid"
)

// Group manages read-write locks for different plan IDs.
type Group struct {
	canceled    *atomic.Bool
	mu          sync.Mutex
	createLocks map[uuid.UUID]*sync.RWMutex
}

// New creates a new Group for managing plan locks. There is a background goroutine that
// cleans up unused locks every minute. If the provided context is canceled, the cleanup goroutine will stop and
// the Group will no longer function.
func New(ctx context.Context) *Group {
	g := &Group{
		canceled:    &atomic.Bool{},
		createLocks: map[uuid.UUID]*sync.RWMutex{},
	}
	_ = context.Pool(ctx).Submit(
		ctx,
		func() {
			g.clean(ctx)
		},
	)

	return g
}

// clean periodically cleans up unused locks from the Group.
func (g *Group) clean(ctx context.Context) {
	t := time.NewTicker(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			g.canceled.Store(true)
			return
		case <-t.C:
			for range t.C {
				g.mu.Lock()
				for planID, lock := range g.createLocks {
					// This is a best-effort cleanup. If the lock is still held, we skip it.
					if !lock.TryLock() {
						continue
					}
					delete(g.createLocks, planID)
				}
				g.mu.Unlock()
			}
		}
	}
}

// Lock acquires a write lock for the given planID.
func (g *Group) Lock(planID uuid.UUID) {
	if g.canceled.Load() {
		panic("planlocks Group has been canceled")
	}
	g.mu.Lock()
	if g.createLocks == nil {
		g.createLocks = map[uuid.UUID]*sync.RWMutex{}
	}
	lock, exists := g.createLocks[planID]
	if !exists {
		lock = &sync.RWMutex{}
		g.createLocks[planID] = lock
	}
	g.mu.Unlock()
	lock.Lock()
}

// Unlock releases the write lock for the given planID.
func (g *Group) Unlock(planID uuid.UUID) {
	if g.canceled.Load() {
		panic("planlocks Group has been canceled")
	}
	g.mu.Lock()
	lock, exists := g.createLocks[planID]
	g.mu.Unlock()

	if !exists {
		log.Println("plan doesn't exist: ", planID)
		panic("unlocking a planID that was not locked")
	}
	lock.Unlock()
}

// RLock acquires a read lock for the given planID.
func (g *Group) RLock(planID uuid.UUID) {
	if g.canceled.Load() {
		panic("planlocks Group has been canceled")
	}
	g.mu.Lock()
	if g.createLocks == nil {
		g.createLocks = map[uuid.UUID]*sync.RWMutex{}
	}
	lock, exists := g.createLocks[planID]
	if !exists {
		lock = &sync.RWMutex{}
		g.createLocks[planID] = lock
	}
	g.mu.Unlock()
	lock.RLock()
}

// RUnlock releases the read lock for the given planID.
func (g *Group) RUnlock(planID uuid.UUID) {
	if g.canceled.Load() {
		panic("planlocks Group has been canceled")
	}
	g.mu.Lock()
	lock, exists := g.createLocks[planID]
	g.mu.Unlock()

	if !exists {
		panic("unlocking a planID that was not locked")
	}
	lock.RUnlock()
}
