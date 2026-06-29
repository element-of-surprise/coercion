package execute

import (
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"

	"github.com/element-of-surprise/coercion/workflow/context"
)

// running tracks which plan IDs are executing in this process. It is the single source of truth for
// "is this ID running here" and provides atomic claim/release so that exactly one run is ever in
// flight per ID. The zero value is not usable; construct it with newRunning.
type running struct {
	// waiters maps a plan ID to a channel closed when that ID's run finishes. Presence of an entry
	// is the authority for whether the ID is running in this process.
	waiters sync.ShardedMap[uuid.UUID, chan struct{}]
	// stoppers maps a plan ID to the CancelFunc that stops its run. Bookkeeping reserved for a future
	// Stop(); release invokes and removes the entry on completion.
	stoppers sync.ShardedMap[uuid.UUID, context.CancelFunc]
}

// newRunning returns a running ready for use.
func newRunning() *running {
	return &running{
		waiters:  sync.ShardedMap[uuid.UUID, chan struct{}]{IsEqual: func(a, b chan struct{}) bool { return a == b }},
		stoppers: sync.ShardedMap[uuid.UUID, context.CancelFunc]{},
	}
}

// claim attempts to register id as running in this process, taking ownership of cancel. It atomically
// installs a fresh waiter only if none exists. On success it returns won==true and a release closure
// that MUST be called exactly once when the run finishes: release cancels the run, removes the
// stopper, closes the waiter, and deletes only this run's own waiter entry. On failure a run for id
// is already in flight: claim returns (nil, false), touches no state, and the caller stays
// responsible for its own cancel.
func (r *running) claim(id uuid.UUID, cancel context.CancelFunc) (release func(), won bool) {
	waiter := make(chan struct{})
	if !r.waiters.CompareAndSwap(id, nil, waiter) {
		return nil, false
	}
	r.stoppers.Set(id, cancel)
	release = func() {
		cancel()
		r.stoppers.Del(id)
		close(waiter)
		// Delete only our own waiter, never one a later run may have installed.
		r.waiters.CompareAndDelete(id, waiter)
	}
	return release, true
}

// wait returns the channel that closes when id's run finishes and ok reporting whether id is running
// in this process. The channel is nil when ok is false.
func (r *running) wait(id uuid.UUID) (<-chan struct{}, bool) {
	w, ok := r.waiters.Get(id)
	return w, ok
}
