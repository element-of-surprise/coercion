package execute

import (
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/values/chans"
	"github.com/gostdlib/base/values/generics/result"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/workflow/context"
)

// running tracks which plan IDs are executing in this process. It is the single source of truth for
// "is this ID running here" and provides atomic claim/release so that exactly one run is ever in
// flight per ID. The zero value is not usable; construct it with newRunning.
type running struct {
	// claims maps a plan ID to the claim in flight for it. Presence of an entry is the authority for whether the ID
	// is claimed (and so running, or about to run) in this process. Ownership and the claim's decision live in the
	// one value, so a caller that loses the claim always gets the claim it lost to and waits on that claim's
	// decision, never on a decision looked up separately by ID.
	claims sync.ShardedMap[uuid.UUID, *claim]
}

// claim is one caller's ownership of a plan ID from claim until release. Its decision is made exactly once: either
// the holder launched a run (launched) or it released the claim without one.
type claim struct {
	r  *running
	id uuid.UUID
	// cancel stops the run. Bookkeeping reserved for a future Stop(); release invokes it.
	cancel context.CancelFunc
	// waiter is closed by release, when the run (if any) has finished.
	waiter chan struct{}
	// decision is set once, by launched or release; once makes the first of them the one that sets it.
	decision *result.Value[decision]
	once     sync.Once
}

// decision is how a claim ended up.
type decision struct {
	// launched is true if the claim became a run.
	launched bool
	// started is a recovered run's report of whether it got going, nil for a run from Start or no run.
	started *sm.Started
}

// newRunning returns a running ready for use.
func newRunning() *running {
	return &running{
		claims: sync.ShardedMap[uuid.UUID, *claim]{IsEqual: func(a, b *claim) bool { return a == b }},
	}
}

// claim attempts to register id as running in this process, taking ownership of cancel. It atomically installs a
// fresh claim only if none exists. On success it returns the new claim and won == true; the caller MUST call its
// release exactly once when the run finishes. On failure a claim for id is already in flight: claim returns that
// claim and won == false, touches no state, and the caller stays responsible for its own cancel. The loser may
// await the returned claim's decision.
func (r *running) claim(id uuid.UUID, cancel context.CancelFunc) (c *claim, won bool) {
	c = &claim{r: r, id: id, cancel: cancel, waiter: make(chan struct{}), decision: result.New[decision]()}
	var inFlight *claim
	// SetAccept inspects the previous entry under the shard lock, so a loser learns the in-flight claim in the same
	// step that refuses its own.
	r.claims.SetAccept(id, c, func(prev *claim, exists bool) bool {
		if exists {
			inFlight = prev
		}
		return !exists
	})
	if inFlight != nil {
		return inFlight, false
	}
	return c, true
}

// wait returns the channel that closes when id's run finishes and ok reporting whether id is claimed
// in this process. The channel is nil when ok is false.
func (r *running) wait(id uuid.UUID) (<-chan struct{}, bool) {
	c, ok := r.claims.Get(id)
	if !ok {
		return nil, false
	}
	return c.waiter, true
}

// launched records that the claim became a run. started is the run's recovery report, or nil for a run that is not
// a recovery. Call it only while holding the claim. It is a no-op once the claim is decided.
func (c *claim) launched(started *sm.Started) {
	c.once.Do(func() { c.decision.Set(decision{launched: true, started: started}, nil) })
}

// release ends the claim: it cancels the run, removes the claim (only its own entry, never one a later claim may
// have installed), closes the waiter and decides the claim if launched never did. The entry is removed before the
// decision is made, so a loser that learns the claim was released can claim at once. Call it exactly once.
func (c *claim) release() {
	c.cancel()
	c.r.claims.CompareAndDelete(c.id, c)
	close(c.waiter)
	c.once.Do(func() { c.decision.Set(decision{}, nil) })
}

// await waits until the claim is decided and returns the decision. It returns ctx's error if ctx ends first.
func (c *claim) await(ctx context.Context) (decision, error) {
	// Done is only closed. A close wins over a done ctx, so a decision made as ctx ends is still reported; Wait itself
	// would pick between the two at random.
	if _, r := chans.Get(ctx, c.decision.Done()); !r.Closed() {
		return decision{}, context.Cause(ctx)
	}
	// The decision is set, so this returns at once. It is not given ctx, which may be done.
	d, _ := c.decision.Wait(context.Background())
	return d, nil
}

// executing waits until the claim is decided and, for a recovered run, until the run reports whether it got going. It
// returns true if the claim's run is executing its Plan: a run from Start, or a recovery that reported success. It
// returns false and no error if the claim was released without a run, and false with the run's error if a recovery
// could not start; that run still holds the claim until it ends. It returns ctx's error if ctx ends first.
func (c *claim) executing(ctx context.Context) (bool, error) {
	d, err := c.await(ctx)
	if err != nil || !d.launched {
		return false, err
	}
	if d.started == nil {
		// A run from Start recorded the Plan as Running itself before launching.
		return true, nil
	}
	if err := d.started.Wait(ctx); err != nil {
		return false, err
	}
	return true, nil
}
