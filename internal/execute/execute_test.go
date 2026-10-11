package execute

import (
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	testplugins "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	pluginsLib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/values/chans"
	"github.com/gostdlib/base/values/generics/result"
	"github.com/kylelemons/godebug/pretty"
)

type badPlugin struct {
	pluginsLib.Plugin
}

func (b badPlugin) Name() string {
	return "bad"
}

func (b badPlugin) Init() error {
	return fmt.Errorf("bad plugin")
}

func (b badPlugin) RetryPolicy() exponential.Policy {
	return pluginsLib.FastRetryPolicy()
}

func (b badPlugin) Request() any {
	return struct{}{}
}

func (b badPlugin) Response() any {
	return struct{}{}
}

type goodPlugin struct {
	name string

	pluginsLib.Plugin
}

func (g goodPlugin) Name() string {
	return g.name
}

func (g goodPlugin) Init() error {
	return nil
}

func (g goodPlugin) RetryPolicy() exponential.Policy {
	return pluginsLib.FastRetryPolicy()
}

func (g goodPlugin) Request() any {
	return struct{}{}
}

func (g goodPlugin) Response() any {
	return struct{}{}
}

func TestInitPlugins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plugins []pluginsLib.Plugin
		wantErr bool
	}{
		{
			name: "Success: a registry with no plugins initializes",
		},
		{
			name: "Success: a registry whose plugins all initialize initializes",
			plugins: []pluginsLib.Plugin{
				goodPlugin{name: "good1"},
				goodPlugin{name: "good2"},
			},
		},
		{
			name: "Error: a registry with a plugin that fails to initialize fails",
			plugins: []pluginsLib.Plugin{
				goodPlugin{name: "good1"},
				badPlugin{},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		reg := registry.New()
		for _, p := range test.plugins {
			reg.Register(p)
		}
		p := &Plans{
			registry: reg,
		}
		err := p.initPlugins(t.Context())
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestInitPlugins(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestInitPlugins(%s): got err == %v, want err == nil", test.name, err)
		}
	}

}

type fakeStore struct {
	storage.Vault

	m           map[uuid.UUID]*workflow.Plan
	updateCalls int
	// updateErr, if set, is returned by UpdatePlan.
	updateErr error
	reads     atomic.Int32
}

func (f *fakeStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	f.reads.Add(1)
	p, ok := f.m[id]
	if !ok {
		return nil, fmt.Errorf("plan(%s): %w", id, storage.ErrNotFound)
	}
	return p, nil
}

func (f *fakeStore) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	f.updateCalls++
	return f.updateErr
}

// UpdateChanges accepts the object writes. The tests using fakeStore check only Plan writes.
func (f *fakeStore) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	return nil
}

type fakeRunner struct {
	called bool
	req    statemachine.Request[sm.Data]
	ran    chan struct{}
}

func (r *fakeRunner) Run(name string, req statemachine.Request[sm.Data], options ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	defer close(r.ran)
	r.called = true
	r.req = req
	return req, nil
}

func NewV7() uuid.UUID {
	for {
		id, err := uuid.NewV7()
		if err == nil {
			return id
		}
	}
}

func TestStart(t *testing.T) {
	t.Parallel()

	storedID := NewV7()
	runningID := NewV7()
	completedID := NewV7()
	failedID := NewV7()
	stoppedID := NewV7()
	updateFailID := NewV7()
	inFlightID := NewV7()

	tests := []struct {
		name string
		id   uuid.UUID
		plan *workflow.Plan
		// updateErr is returned by the store's UpdatePlan.
		updateErr error
		// inFlight has a run for the plan already in flight in this process when Start is called.
		inFlight       bool
		wantErr        bool
		wantRunnerCall bool
		// wantUpdates is how many times Start writes the Plan.
		wantUpdates int
	}{
		{
			name:    "Error: no plan could be found",
			id:      NewV7(),
			wantErr: true,
		},
		{
			name: "Success: plan starts execution",
			id:   storedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: storedID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			wantRunnerCall: true,
			wantUpdates:    1,
		},
		{
			// Regression: Start wrote the Plan before claiming its run, so a duplicate Start overwrote the in-flight
			// run's progress with the stale NotStarted Plan it had read. It must lose the claim and write nothing.
			name: "Success: a duplicate Start while a run is in flight returns nil without writing storage or starting a runner",
			id:   inFlightID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: inFlightID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			inFlight: true,
		},
		{
			// Regression: Start released its claim on the failed write and again in its deferred cleanup, and the
			// second release panicked closing an already closed channel.
			name: "Error: a plan whose Running state cannot be written is not started and its claim is released",
			id:   updateFailID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: updateFailID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.NotStarted})
				return p
			}(),
			updateErr:   errors.New("update failed"),
			wantErr:     true,
			wantUpdates: 1,
		},
		{
			name: "Success: plan already Running returns nil without starting",
			id:   runningID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: runningID, SubmitTime: time.Now()}
				p.State.Set(workflow.State{Status: workflow.Running, Start: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Completed returns nil without starting",
			id:   completedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: completedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Failed returns nil without starting",
			id:   failedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: failedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Failed, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
		{
			name: "Success: plan already Stopped returns nil without starting",
			id:   stoppedID,
			plan: func() *workflow.Plan {
				p := &workflow.Plan{ID: stoppedID, SubmitTime: time.Now().Add(-time.Minute)}
				p.State.Set(workflow.State{Status: workflow.Stopped, Start: time.Now().Add(-time.Minute), End: time.Now()})
				return p
			}(),
			wantRunnerCall: false,
		},
	}

	for _, test := range tests {
		store := &fakeStore{
			m:         map[uuid.UUID]*workflow.Plan{},
			updateErr: test.updateErr,
		}
		if test.plan != nil {
			store.m[test.id] = test.plan
		}

		fr := &fakeRunner{ran: make(chan struct{})}

		p := &Plans{
			store:     store,
			runner:    fr.Run,
			states:    &sm.States{},
			running:   newRunning(),
			maxSubmit: 30 * time.Minute,
		}
		p.addValidators()
		if test.inFlight {
			c, won := p.running.claim(test.id, func() {})
			if !won {
				t.Errorf("TestStart(%s): setup claim did not win", test.name)
				continue
			}
			c.launched(nil)
		}

		err := p.Start(t.Context(), test.id)
		if store.updateCalls != test.wantUpdates {
			t.Errorf("TestStart(%s): got UpdatePlan called %d times, want %d", test.name, store.updateCalls, test.wantUpdates)
		}
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestStart(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestStart(%s): got err == %v, want err == nil", test.name, err)
			continue
		case err != nil:
			// A claim left behind would make every later Start or Resume of the Plan wait on a run that never comes.
			if p.running.claims.Len() > 0 {
				t.Errorf("TestStart(%s): got a claim left after the error, want it released", test.name)
			}
			continue
		}

		if test.wantRunnerCall {
			// ran is only closed.
			wctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			_, r := chans.Get(wctx, fr.ran)
			cancel()
			if !r.Closed() {
				t.Errorf("TestStart(%s): runner was not called", test.name)
			}

			if diff := pretty.Compare(test.plan, fr.req.Data.Plan); diff != "" {
				t.Errorf("TestStart(%s): Plan in Request diff: -want/+got:\n%s", test.name, diff)
			}
			if methodName(fr.req.Next) != methodName(p.states.Start) {
				t.Errorf("TestStart(%s): Next method in Request is not the expected Start method", test.name)
			}

			// ran closes inside the runner, before launch releases the claim, so wait for the release first.
			if waiter, ok := p.running.wait(test.id); ok {
				wctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				_, r := chans.Get(wctx, waiter)
				cancel()
				if !r.Closed() {
					t.Errorf("TestStart(%s): the run never released its claim", test.name)
				}
			}
			if p.running.claims.Len() > 0 {
				t.Errorf("TestStart(%s): did not delete the claim entry", test.name)
			}
		} else {
			// Give a brief moment to ensure runner isn't called; the wait running out is the pass.
			wctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			_, r := chans.Get(wctx, fr.ran)
			cancel()
			if r != chans.ResultCtxDone {
				t.Errorf("TestStart(%s): runner was called but should not have been", test.name)
			}
		}
	}
}

// blockingRunner records each invocation on started and blocks until release is closed. It lets a
// test deterministically hold a runPlan goroutine "in flight" while issuing a second runPlan for the
// same ID.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) Run(name string, req statemachine.Request[sm.Data], options ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	r.started <- struct{}{}
	<-r.release
	// Finish the plan, as End does, so storage records a final state before the run ends.
	req.Data.Plan.State.Set(workflow.State{Status: workflow.Completed})
	return req, nil
}

// TestRunPlan is a regression test for the "plan has a running state, but isn't in the waiters"
// panic. Two runPlan calls for the same ID must start exactly one runner: the duplicate must not
// overwrite the waiter. With the overwrite, the first goroutine to finish deleted the second's
// (overwritten) waiter entry while the second was still running, leaving storage Running with no
// waiter, which Wait reported as a TypeBug that the caller turned into a panic.
func TestRunPlan(t *testing.T) {
	t.Parallel()

	id := NewV7()
	plan := &workflow.Plan{ID: id}
	plan.State.Set(workflow.State{Status: workflow.Running})

	store := &fakeStore{m: map[uuid.UUID]*workflow.Plan{id: plan}}
	br := &blockingRunner{started: make(chan struct{}, 4), release: make(chan struct{})}

	e := &Plans{
		store:   store,
		runner:  br.Run,
		states:  &sm.States{},
		running: newRunning(),
	}

	ctx := t.Context()

	// First run starts a single runner that we hold in flight.
	e.runPlan(ctx, plan, nil)
	startCtx, startCancel := context.WithTimeout(ctx, 2*time.Second)
	_, r := chans.Get(startCtx, br.started)
	startCancel()
	if !r.OK() {
		t.Fatalf("TestRunPlan: first runner never started")
	}

	// Duplicate run for the same ID while the first is still in flight must be a no-op.
	e.runPlan(ctx, plan, nil)
	// The wait running out is the pass: no second runner started.
	dupCtx, dupCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, r = chans.Get(dupCtx, br.started)
	dupCancel()
	if r != chans.ResultCtxDone {
		t.Errorf("TestRunPlan: duplicate runPlan for the same ID started a second runner, want exactly one")
		return // On pre-fix code the clobbered waiter would crash cleanup; stop here.
	}

	// The single runner is still blocked, so its waiter is present. Wait must block and then return
	// nil once the runner completes and closes its own waiter.
	// blocked marks the point Wait has fetched the waiter and blocks on it.
	blocked := newDoneSignal(ctx)
	waitRes := result.New[struct{}]()
	context.Pool(ctx).Submit(ctx, func() {
		defer blocked.fire()
		_, err := e.Wait(blocked, id)
		waitRes.Set(struct{}{}, err)
	})

	// Let Wait fetch the waiter and block on it before the runner completes and removes it.
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, res := chans.Get(waitCtx, blocked.called); !res.Closed() {
		t.Fatalf("TestRunPlan: Wait never blocked on the run")
	}
	close(br.release)

	if _, err := waitRes.Wait(waitCtx); err != nil {
		t.Errorf("TestRunPlan: Wait after the single runner completed: got err == %v, want err == nil", err)
	}

	// release removes the claim before it closes the waiter, so it is gone once Wait has returned.
	if e.running.claims.Len() != 0 {
		t.Errorf("TestRunPlan: got claims.Len() == %d, want 0 after completion", e.running.claims.Len())
	}
}

// TestRunPlanDistinctIDsAndRecovery pins the recovery-safety guarantees of the per-ID guard added to
// runPlan: the guard must dedup only the *same* ID (so recovery still starts every Running plan), and
// a duplicate run for an already in-flight recovered plan must close its recoveryStarted channel so
// the recovery loop never hangs.
func TestRunPlanDistinctIDsAndRecovery(t *testing.T) {
	t.Parallel()

	const n = 5

	store := &fakeStore{m: map[uuid.UUID]*workflow.Plan{}}
	br := &blockingRunner{started: make(chan struct{}, n+2), release: make(chan struct{})}
	e := &Plans{
		store:   store,
		runner:  br.Run,
		states:  &sm.States{},
		running: newRunning(),
	}
	ctx := t.Context()

	// Every distinct Running plan must get its own runner; none deduped away.
	ids := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		id := NewV7()
		ids[i] = id
		p := &workflow.Plan{ID: id}
		p.State.Set(workflow.State{Status: workflow.Running})
		store.m[id] = p
		e.runPlan(ctx, p, nil)
	}
	for i := 0; i < n; i++ {
		startCtx, startCancel := context.WithTimeout(ctx, 2*time.Second)
		_, r := chans.Get(startCtx, br.started)
		startCancel()
		if !r.OK() {
			t.Fatalf("TestRunPlanDistinctIDsAndRecovery: only %d of %d runners started", i, n)
		}
	}
	if got := e.running.claims.Len(); got != n {
		t.Errorf("TestRunPlanDistinctIDsAndRecovery: got claims.Len() == %d, want %d", got, n)
	}

	// A duplicate run for an already in-flight plan must report recoveryStarted (so recover()'s wait
	// loop completes) and must not start another runner.
	dupStarted := sm.NewStarted()
	e.runPlan(ctx, store.m[ids[0]], dupStarted)
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if err := dupStarted.Wait(waitCtx); err != nil {
		t.Errorf("TestRunPlanDistinctIDsAndRecovery: duplicate runPlan did not report recoveryStarted: %s", err)
	}
	cancel()
	// The wait running out is the pass: no other runner started.
	dupCtx, dupCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, r := chans.Get(dupCtx, br.started)
	dupCancel()
	if r != chans.ResultCtxDone {
		t.Errorf("TestRunPlanDistinctIDsAndRecovery: duplicate runPlan started another runner, want none")
	}

	close(br.release) // Let the runners finish and clean up their waiters.
}

func TestWait(t *testing.T) {
	t.Parallel()

	finished := func(status workflow.Status) workflow.State {
		return workflow.State{Status: status, Start: time.Now().Add(-time.Minute), End: time.Now()}
	}

	tests := []struct {
		name string
		// stored means the Plan is in storage, with state.
		stored bool
		state  workflow.State
		// inFlight means a run for the Plan is in flight here. finishRun ends it before Wait; cancelCtx ends the caller's
		// Context instead.
		inFlight  bool
		finishRun bool
		cancelCtx bool
		// recoveryFails makes a resumed run fail to write the Plan and end, as sm.Recovery does under throttling.
		recoveryFails bool
		// noRecovery builds the Plans as WithNoRecovery does.
		noRecovery bool

		// wantRuns is how many runs Wait resumes. A resumed run records the Plan Completed.
		wantRuns int
		// wantStatus is the returned Plan's status, if it is not state's.
		wantStatus workflow.Status
		// wantReads is how many times storage is read.
		wantReads int32
		// wantPermanent means the error must wrap errors.ErrPermanent. Workstream.Wait's doc (coercion.go:~203) promises
		// callers that errors retrying cannot fix wrap it, so their retry loops stop.
		wantPermanent bool
		// wantCanceled means errors.Is(err, context.Canceled), which coercion.go's Workstream.Wait doc promises callers.
		wantCanceled bool
		wantErr      bool
	}{
		{
			name:      "Success: a run in flight here completes and Wait returns the finished plan",
			stored:    true,
			state:     finished(workflow.Completed),
			inFlight:  true,
			finishRun: true,
			wantReads: 1,
		},
		{
			name:      "Success: a Completed plan with no run here is returned",
			stored:    true,
			state:     finished(workflow.Completed),
			wantReads: 1,
		},
		{
			name:      "Success: a Failed plan with no run here is returned",
			stored:    true,
			state:     finished(workflow.Failed),
			wantReads: 1,
		},
		{
			name:      "Success: a Stopped plan with no run here is returned",
			stored:    true,
			state:     finished(workflow.Stopped),
			wantReads: 1,
		},
		{
			// Regression: a run that ended without recording a final state (for example sm.Recovery failing to write
			// the Plan) must not look finished to a caller already waiting on it, and must not be left Running with
			// nothing executing it.
			name:       "Success: a run here ends while storage still says Running, so Wait resumes it",
			stored:     true,
			state:      workflow.State{Status: workflow.Running, Start: time.Now()},
			inFlight:   true,
			finishRun:  true,
			wantRuns:   1,
			wantStatus: workflow.Completed,
			// Wait's read, Resume's read, and Wait's read after the resumed run ends.
			wantReads: 3,
		},
		{
			// Regression: Wait returned an error for a Plan Running in storage with no run here, and medbay panicked on
			// it. Wait must take the Plan over, as startup recovery does.
			name:       "Success: a Running plan with no run here is resumed and Wait returns it finished",
			stored:     true,
			state:      workflow.State{Status: workflow.Running, Start: time.Now()},
			wantRuns:   1,
			wantStatus: workflow.Completed,
			wantReads:  3,
		},
		{
			name:       "Success: a Running plan with no run here past the recovery limit is returned Failed",
			stored:     true,
			state:      workflow.State{Status: workflow.Running, Start: time.Now().Add(-time.Hour)},
			wantStatus: workflow.Failed,
			wantReads:  3,
		},
		{
			name:         "Error: the caller's context ends while a run is in flight",
			stored:       true,
			state:        workflow.State{Status: workflow.Running},
			inFlight:     true,
			cancelCtx:    true,
			wantCanceled: true,
			wantErr:      true,
		},
		{
			name:          "Error: a NotStarted plan is a permanent error",
			stored:        true,
			state:         workflow.State{Status: workflow.NotStarted},
			wantPermanent: true,
			wantErr:       true,
		},
		{
			// Regression: Wait resumed the Plan although WithNoRecovery disables recovery.
			name:          "Error: a Running plan with no run here is not resumed when recovery is disabled",
			stored:        true,
			state:         workflow.State{Status: workflow.Running, Start: time.Now()},
			noRecovery:    true,
			wantReads:     1,
			wantPermanent: true,
			wantErr:       true,
		},
		{
			name:          "Error: a Running plan with no run here whose resume fails returns the failure",
			stored:        true,
			state:         workflow.State{Status: workflow.Running, Start: time.Now()},
			recoveryFails: true,
			wantRuns:      1,
			wantErr:       true,
		},
		{
			name:          "Error: a plan that is not in storage is a permanent not-found error",
			wantPermanent: true,
			wantErr:       true,
		},
	}

	for _, test := range tests {
		id := NewV7()
		store := &fakeStore{m: map[uuid.UUID]*workflow.Plan{}}
		if test.stored {
			plan := &workflow.Plan{ID: id}
			plan.State.Set(test.state)
			store.m[id] = plan
		}
		runner := &resumeRunner{release: make(chan struct{}), complete: store.m[id]}
		if test.recoveryFails {
			// sm.Recovery reports a typed storage error.
			runner.startErr = errors.E(t.Context(), errors.CatInternal, errors.TypeStorageUpdate, errors.New("storage busy"))
		}
		close(runner.release)
		p := &Plans{
			store:         store,
			runner:        runner.Run,
			states:        &sm.States{},
			running:       newRunning(),
			maxLastUpdate: 30 * time.Minute,
			recovery:      !test.noRecovery,
		}

		ctx := t.Context()
		if test.cancelCtx {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		if test.inFlight {
			c, won := p.running.claim(id, func() {})
			if !won {
				t.Fatalf("TestWait(%s): setup claim did not win", test.name)
			}
			if test.finishRun {
				c.release()
			} else {
				t.Cleanup(c.release)
			}
		}

		got, err := p.Wait(ctx, id)
		if got, want := runner.Runs(), test.wantRuns; got != want {
			t.Errorf("TestWait(%s): got %d runs, want %d", test.name, got, want)
		}
		if got, want := errors.Is(err, errors.ErrPermanent), test.wantPermanent; got != want {
			t.Errorf("TestWait(%s): got errors.Is(err, ErrPermanent) == %v, want %v", test.name, got, want)
		}
		// coercion.go's Workstream.Wait doc promises callers errors.Is(err, context.Canceled) when ctx is canceled.
		if got, want := errors.Is(err, context.Canceled), test.wantCanceled; got != want {
			t.Errorf("TestWait(%s): got errors.Is(err, context.Canceled) == %v, want %v", test.name, got, want)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestWait(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestWait(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		wantStatus := test.state.Status
		if test.wantStatus != workflow.NotStarted {
			wantStatus = test.wantStatus
		}
		if got.ID != id || got.State.Get().Status != wantStatus {
			t.Errorf("TestWait(%s): got plan(%s) status %v, want plan(%s) status %v", test.name, got.ID, got.State.Get().Status, id, wantStatus)
		}
		// Regression: Wait read the plan from storage and Workstream.Wait read it again.
		if got := store.reads.Load(); got != test.wantReads {
			t.Errorf("TestWait(%s): got %d storage reads, want %d", test.name, got, test.wantReads)
		}
	}
}

// gatedReadStore counts Read calls and holds every Read until gate is closed, so concurrent readers of one Plan pile
// up behind the first. Read always returns the same stored Plan.
type gatedReadStore struct {
	storage.Vault

	plan  *workflow.Plan
	reads atomic.Int32
	gate  chan struct{}
}

func (s *gatedReadStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	s.reads.Add(1)
	// gate is only closed, so anything but a close means the Context ended.
	if _, res := chans.Get(ctx, s.gate); !res.Closed() {
		return nil, ctx.Err()
	}
	return s.plan, nil
}

// TestWaitConcurrent is a regression test: every Wait on a Plan read it from storage when the run ended, so N callers
// waiting on one Plan made N full store reads at once. Concurrent Waits on one Plan must share a read, and each must
// still get its own copy of the Plan, since callers may change what they get.
func TestWaitConcurrent(t *testing.T) {
	t.Parallel()

	const waiters = 32

	ctx := t.Context()
	id := NewV7()
	stored := &workflow.Plan{ID: id, Name: "plan", Blocks: []*workflow.Block{{ID: NewV7(), Key: NewV7(), Name: "block"}}}
	stored.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now().Add(-time.Minute), End: time.Now()})
	stored.RuntimeUpdate.Set(time.Now())
	store := &gatedReadStore{plan: stored, gate: make(chan struct{})}
	p := &Plans{store: store, running: newRunning()}

	c, won := p.running.claim(id, func() {})
	if !won {
		t.Fatalf("TestWaitConcurrent: setup claim did not win")
	}

	var mu sync.Mutex
	plans := make([]*workflow.Plan, 0, waiters)
	g := context.Pool(ctx).Group()
	for i := 0; i < waiters; i++ {
		g.Go(ctx, func(ctx context.Context) error {
			plan, err := p.Wait(ctx, id)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			plans = append(plans, plan)
			return nil
		})
	}

	// Ending the run sends every waiter to storage. The first Read holds at the gate; give the others time to reach
	// storage too (they all do at once without deduplication), so the shared read is the only thing keeping it at one.
	c.release()
	if _, res := chans.Get(ctx, time.After(time.Second)); res != chans.ResultOK {
		t.Fatalf("TestWaitConcurrent: the test ended before the waiters settled")
	}
	close(store.gate)

	if err := g.Wait(ctx); err != nil {
		t.Fatalf("TestWaitConcurrent: got err == %s, want err == nil", err)
	}

	// A waiter scheduled late can miss the shared read and make its own, so allow a few, far below one per waiter.
	if got := store.reads.Load(); got > waiters/4 {
		t.Errorf("TestWaitConcurrent: got %d storage reads for %d waiters, want them to share a read", got, waiters)
	}

	seen := map[*workflow.Plan]bool{stored: true}
	for _, plan := range plans {
		if seen[plan] {
			t.Errorf("TestWaitConcurrent: a waiter got a Plan another waiter or the store holds, want its own copy")
		}
		seen[plan] = true
		if diff := pretty.Compare(stored, plan); diff != "" {
			t.Errorf("TestWaitConcurrent: plan -want/+got:\n%s", diff)
		}
		if got, want := plan.State.Get().Status, workflow.Completed; got != want {
			t.Errorf("TestWaitConcurrent: got status %v, want %v", got, want)
		}
		if got, want := plan.RuntimeUpdate.Get(), stored.RuntimeUpdate.Get(); !got.Equal(want) {
			t.Errorf("TestWaitConcurrent: got RuntimeUpdate %v, want %v", got, want)
		}
	}
	// Changing one waiter's Plan must not change another's.
	plans[0].Blocks[0].Name = "changed"
	if got := plans[1].Blocks[0].Name; got != "block" {
		t.Errorf("TestWaitConcurrent: changing one waiter's Plan changed another's block name to %q", got)
	}
}

// TestWaitNoRecovery is a regression test: with recovery disabled, Wait looked for a run, then read storage, and
// returned its permanent "recovery is disabled" error when the read said Running. A Start that claimed the Plan and
// wrote it Running between the two made Wait reject a Plan running in this process. Wait must wait on that run.
func TestWaitNoRecovery(t *testing.T) {
	t.Parallel()

	id := NewV7()
	store := &slowReadStore{
		status:    workflow.Running,
		id:        id,
		claimed:   func() bool { return false },
		firstRead: make(chan struct{}),
		gate:      make(chan struct{}),
	}
	e := &Plans{store: store, running: newRunning(), maxLastUpdate: 30 * time.Minute}

	type waitResult struct {
		plan *workflow.Plan
		err  error
	}
	done := result.New[waitResult]()
	context.Pool(t.Context()).Submit(t.Context(), func() {
		plan, err := e.Wait(t.Context(), id)
		done.Set(waitResult{plan: plan, err: err}, nil)
	})
	// Wait found no run and is reading storage.
	if _, r := chans.Get(t.Context(), store.firstRead); !r.Closed() {
		t.Fatalf("TestWaitNoRecovery: Wait never read the Plan")
	}

	// As Start does: claim the Plan and launch a run while Wait's read is open, then let the read return Running.
	c, won := e.running.claim(id, func() {})
	if !won {
		t.Fatalf("TestWaitNoRecovery: setup claim did not win")
	}
	c.launched(nil)
	close(store.gate)

	// Wait must be waiting on the run, not have returned.
	waitCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	_, r := chans.Get(waitCtx, done.Done())
	cancel()
	if r.Closed() {
		got, _ := done.Wait(t.Context())
		t.Fatalf("TestWaitNoRecovery: Wait returned (err == %v) while a run was in flight, want it to wait on the run", got.err)
	}

	// The run finishes.
	store.setStatus(workflow.Completed)
	c.release()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, r := chans.Get(ctx, done.Done()); !r.Closed() {
		t.Fatalf("TestWaitNoRecovery: Wait never returned after the run ended")
	}
	got, _ := done.Wait(t.Context())
	if got.err != nil {
		t.Fatalf("TestWaitNoRecovery: got err == %s, want err == nil", got.err)
	}
	if got.plan.State.Get().Status != workflow.Completed {
		t.Errorf("TestWaitNoRecovery: got status %v, want %v", got.plan.State.Get().Status, workflow.Completed)
	}
}

// TestWaitAfterRun is a regression test: Waits on one Plan share a storage read, and a Wait that began after a run
// ended could join a read that started before the run wrote its final state. That Wait got the stale snapshot, such
// as a permanent "not started" error for a Plan that had finished. Resume aging a Plan out had the same gap: a Wait
// after it got the Running snapshot for a Plan that was now Failed. A read that started before a Plan's final state
// was written must not be shared with a Wait that begins after it.
func TestWaitAfterRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// status is the Plan's stored status before finish.
		status workflow.Status
		// age is how long before now the stored Plan last changed.
		age time.Duration
		// finish writes the Plan's final state while the early Wait's read is still open.
		finish     func(t *testing.T, e *Plans, id uuid.UUID)
		wantStatus workflow.Status
		// wantEarlyErr means the early Wait, which read the status from before finish, returns an error.
		wantEarlyErr bool
	}{
		{
			name:   "Success: a Wait after a run ends reads the Plan the run finished",
			status: workflow.NotStarted,
			finish: func(t *testing.T, e *Plans, id uuid.UUID) {
				runCtx, c, won := e.claimRun(t.Context(), id)
				if !won {
					t.Fatalf("TestWaitAfterRun: claimRun did not win")
				}
				plan := &workflow.Plan{ID: id}
				plan.State.Set(workflow.State{Status: workflow.NotStarted})
				if err := e.launch(t.Context(), runCtx, c, plan, nil); err != nil {
					t.Fatalf("TestWaitAfterRun: launch: %s", err)
				}
				if waiter, ok := e.running.wait(id); ok {
					if _, r := chans.Get(t.Context(), waiter); !r.Closed() {
						t.Fatalf("TestWaitAfterRun: the run never ended")
					}
				}
			},
			wantStatus:   workflow.Completed,
			wantEarlyErr: true,
		},
		{
			name:   "Success: a Wait after Resume ages the Plan out reads it Failed",
			status: workflow.Running,
			age:    time.Hour,
			finish: func(t *testing.T, e *Plans, id uuid.UUID) {
				if err := e.Resume(t.Context(), id); err != nil {
					t.Fatalf("TestWaitAfterRun: Resume: %s", err)
				}
			},
			wantStatus: workflow.Failed,
		},
	}

	for _, test := range tests {
		id := NewV7()
		store := &slowReadStore{
			status:       test.status,
			start:        time.Now().Add(-test.age),
			id:           id,
			claimed:      func() bool { return false },
			firstRead:    make(chan struct{}),
			gate:         make(chan struct{}),
			recordWrites: true,
		}
		e := &Plans{
			store: store,
			// The run writes the Plan's final state, as sm.End does.
			runner: func(_ string, req statemachine.Request[sm.Data], _ ...statemachine.Option) (statemachine.Request[sm.Data], error) {
				store.setStatus(workflow.Completed)
				return req, nil
			},
			states:        &sm.States{},
			running:       newRunning(),
			maxLastUpdate: 30 * time.Minute,
			// As New sets it: the early Wait resumes the Running Plan it read.
			recovery: true,
		}

		// A Wait before the Plan's final state is written reads its stored status and holds that read open.
		early := result.New[error]()
		context.Pool(t.Context()).Submit(t.Context(), func() {
			_, err := e.Wait(t.Context(), id)
			early.Set(err, nil)
		})
		if _, r := chans.Get(t.Context(), store.firstRead); !r.Closed() {
			t.Fatalf("TestWaitAfterRun(%s): the early Wait never read the Plan", test.name)
		}

		test.finish(t, e, id)

		// A Wait after that must read the final state, not wait on the read that started before it.
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		got, err := e.Wait(ctx, id)
		cancel()
		// The early Wait started before the final state was written, so its outcome comes from the status it read: an
		// error for NotStarted, while for Running it resumes the Plan, finds it Failed and returns it.
		close(store.gate)
		earlyErr, _ := early.Wait(t.Context())
		if got := earlyErr != nil; got != test.wantEarlyErr {
			t.Errorf("TestWaitAfterRun(%s): got the early Wait's err == %v, want err != nil == %v", test.name, earlyErr, test.wantEarlyErr)
		}
		if err != nil {
			t.Errorf("TestWaitAfterRun(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		if got.State.Get().Status != test.wantStatus {
			t.Errorf("TestWaitAfterRun(%s): got status %v, want %v", test.name, got.State.Get().Status, test.wantStatus)
		}
	}
}

// resumeRunner stands in for the statemachine on a recovered run: it records the run, reports that recovery has
// started (as sm.Recovery does), then holds the run in flight until release is closed. If startErr is set it reports
// that instead and ends the run at once, as sm.Recovery does when it cannot write the Plan.
type resumeRunner struct {
	mu       sync.Mutex
	runs     int
	release  chan struct{}
	startErr error
	// failRuns limits startErr to the first failRuns runs; 0 means every run.
	failRuns int
	// endEarly ends the run without reporting, as when the statemachine fails before reaching sm.Recovery.
	endEarly bool
	// holdReport holds the report until release is closed.
	holdReport bool
	// holdFailed keeps a run that reported startErr in flight until release is closed, as a real run holds its claim
	// until it ends.
	holdFailed bool
	// entered, if set, receives a value as each run begins, if it has room; it should have room for every run.
	entered chan struct{}
	// complete, if set, is the stored Plan, which a run that started records Completed before it ends, as sm.End does.
	complete *workflow.Plan
}

func (r *resumeRunner) Run(_ string, req statemachine.Request[sm.Data], _ ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	r.mu.Lock()
	r.runs++
	startErr := r.startErr
	if r.failRuns > 0 && r.runs > r.failRuns {
		startErr = nil
	}
	r.mu.Unlock()

	if r.entered != nil {
		chans.TryPut(r.entered, struct{}{})
	}
	if r.endEarly {
		return req, errors.New("statemachine failed before recovery")
	}
	if r.holdReport {
		<-r.release
	}
	if req.Data.RecoveryStarted != nil {
		req.Data.RecoveryStarted.Report(startErr)
	}
	if startErr != nil {
		if r.holdFailed {
			<-r.release
		}
		return req, startErr
	}
	<-r.release
	if r.complete != nil {
		r.complete.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now(), End: time.Now()})
	}
	return req, nil
}

// Runs returns how many runs were started.
func (r *resumeRunner) Runs() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}

func TestResume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// status is the Plan's stored status, unless notStored.
		status    workflow.Status
		notStored bool
		// inFlight means a run for the Plan is already in flight in this process.
		inFlight bool
		// cancelCtx ends the caller's Context while Resume waits for the run to report that it started.
		cancelCtx bool
		// recoveryFails makes the resumed run fail to write the Plan and end, as sm.Recovery does under throttling.
		recoveryFails bool
		// endsEarly makes the resumed run end before sm.Recovery reports.
		endsEarly bool
		// lastUpdate is how long ago the Plan was last updated.
		lastUpdate time.Duration

		wantRuns int
		// wantAgedOut means the Plan must be left Failed with FRExceedRecovery instead of run.
		wantAgedOut bool
		// wantPermanent means the error must wrap errors.ErrPermanent. Wait returns Resume's errors, and
		// Workstream.Wait's doc (coercion.go) promises callers that errors retrying cannot fix wrap it.
		wantPermanent bool
		// wantNotFound means errors.IsNotFound(err), which retry.go's resumeOrExit branches on to stop retrying.
		wantNotFound bool
		wantErr      bool
	}{
		{
			name:     "Success: a Running plan with no run in flight here is resumed",
			status:   workflow.Running,
			wantRuns: 1,
		},
		{
			// Regression: Resume used to run a Plan that startup recovery would have refused as too old.
			name:        "Success: a Running plan older than the recovery limit is marked Failed instead of resumed",
			status:      workflow.Running,
			lastUpdate:  24 * time.Hour,
			wantAgedOut: true,
		},
		{
			name:     "Success: a Running plan already in flight here is left alone",
			status:   workflow.Running,
			inFlight: true,
		},
		{
			name:   "Success: a Completed plan is not resumed",
			status: workflow.Completed,
		},
		{
			// Regression: a recovery that could not write the Plan must not look like a successful resume.
			name:          "Error: a resumed run whose recovery fails to write the Plan returns the failure",
			status:        workflow.Running,
			recoveryFails: true,
			wantErr:       true,
		},
		{
			name:      "Error: a resumed run that ends before recovery reports returns a bug instead of hanging",
			status:    workflow.Running,
			endsEarly: true,
			wantErr:   true,
		},
		{
			// Regression: the caller giving up was reported as a storage failure, although the run goes on.
			name:      "Error: the caller's context ending while Resume waits is a timeout",
			status:    workflow.Running,
			cancelCtx: true,
			wantErr:   true,
		},
		{
			name:          "Error: a NotStarted plan must be started, not resumed",
			status:        workflow.NotStarted,
			wantPermanent: true,
			wantErr:       true,
		},
		{
			// Regression: a missing plan came back as a retryable error, so a retrying caller retried forever.
			name:          "Error: a plan that is not in storage is a permanent not-found error",
			notStored:     true,
			wantPermanent: true,
			wantNotFound:  true,
			wantErr:       true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		id := NewV7()
		store := &fakeStore{m: map[uuid.UUID]*workflow.Plan{}}
		if !test.notStored {
			plan := &workflow.Plan{ID: id}
			plan.State.Set(workflow.State{Status: test.status, Start: time.Now().Add(-test.lastUpdate)})
			store.m[id] = plan
		}
		if test.cancelCtx {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}

		runner := &resumeRunner{release: make(chan struct{})}
		if test.recoveryFails {
			// sm.Recovery reports a typed storage error.
			runner.startErr = errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, errors.New("storage busy"))
		}
		runner.endEarly = test.endsEarly
		// With cancelCtx, hold the report so Resume sees the ended Context first.
		runner.holdReport = test.cancelCtx
		t.Cleanup(func() { close(runner.release) })
		e := &Plans{
			store:         store,
			runner:        runner.Run,
			states:        &sm.States{},
			running:       newRunning(),
			maxLastUpdate: 30 * time.Minute,
		}
		if test.inFlight {
			c, won := e.running.claim(id, func() {})
			if !won {
				t.Fatalf("TestResume(%s): setup claim did not win", test.name)
			}
			c.launched(nil)
		}

		err := e.Resume(ctx, id)
		if got := errors.Is(err, errors.ErrPermanent); got != test.wantPermanent {
			t.Errorf("TestResume(%s): got errors.Is(err, ErrPermanent) == %v, want %v", test.name, got, test.wantPermanent)
		}
		// retry.go's resumeOrExit branches on errors.IsNotFound to stop retrying a Plan that no longer exists.
		if got := errors.IsNotFound(err); got != test.wantNotFound {
			t.Errorf("TestResume(%s): got errors.IsNotFound(err) == %v, want %v", test.name, got, test.wantNotFound)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestResume(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestResume(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if got := runner.Runs(); got != test.wantRuns {
			t.Errorf("TestResume(%s): got %d runs, want %d", test.name, got, test.wantRuns)
		}
		if test.wantAgedOut {
			stored := store.m[id]
			if got := stored.State.Get().Status; got != workflow.Failed {
				t.Errorf("TestResume(%s): got stored status %v, want %v", test.name, got, workflow.Failed)
			}
			if stored.Reason != workflow.FRExceedRecovery {
				t.Errorf("TestResume(%s): got stored reason %v, want %v", test.name, stored.Reason, workflow.FRExceedRecovery)
			}
			if _, ok := e.running.wait(id); ok {
				t.Errorf("TestResume(%s): aged out plan still claimed, want the claim released", test.name)
			}
		}
		if test.wantRuns == 0 {
			continue
		}

		// Resume returned after the run was set up, so the Plan is owned here: Wait must block on the run, not resume
		// it again. A second Resume must not start a second run.
		if _, ok := e.running.wait(id); !ok {
			t.Errorf("TestResume(%s): resumed plan has no waiter, Wait would resume it again", test.name)
		}
		if err := e.Resume(ctx, id); err != nil {
			t.Errorf("TestResume(%s): second Resume: got err == %s, want err == nil", test.name, err)
		}
		if got := runner.Runs(); got != test.wantRuns {
			t.Errorf("TestResume(%s): after a second Resume got %d runs, want %d", test.name, got, test.wantRuns)
		}
	}
}

// slowReadStore returns a fresh copy of the stored Plan on every Read, as azblob decodes a new Plan each time. The
// first Read takes its copy, closes firstRead, and then blocks until gate is closed, like an azblob Read that saw the
// entry status and then spent a long time fetching sub-objects under throttling. Every Read records whether claimed
// reported the Plan as claimed at that moment.
type slowReadStore struct {
	storage.Vault

	mu     sync.Mutex
	status workflow.Status
	// start is the stored Plan's start time. Zero means the time of each Read.
	start     time.Time
	id        uuid.UUID
	reads     int
	unclaimed int
	claimed   func() bool
	firstRead chan struct{}
	gate      chan struct{}
	// recordWrites makes UpdatePlan store the status of the Plan it is given.
	recordWrites bool
}

func (s *slowReadStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	s.mu.Lock()
	s.reads++
	first := s.reads == 1
	if !s.claimed() {
		s.unclaimed++
	}
	start := s.start
	if start.IsZero() {
		start = time.Now()
	}
	p := &workflow.Plan{ID: s.id}
	p.State.Set(workflow.State{Status: s.status, Start: start})
	s.mu.Unlock()

	if first {
		close(s.firstRead)
		// gate is only closed, so anything but a close means the Context ended.
		if _, res := chans.Get(ctx, s.gate); !res.Closed() {
			return nil, ctx.Err()
		}
	}
	return p, nil
}

func (s *slowReadStore) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	if s.recordWrites {
		s.setStatus(plan.State.Get().Status)
	}
	return nil
}

// UpdateChanges accepts the object writes. The tests using slowReadStore check only the Plan's status.
func (s *slowReadStore) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	return nil
}

func (s *slowReadStore) setStatus(status workflow.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// TestResumeConcurrent is a regression test: Resume used to read the Plan before claiming it, so a Resume whose read
// was slow could claim the Plan after a second Resume had run it to completion, and execute its stale Running copy
// again. Resume must hold the claim whenever it reads the Plan, so a second Resume finds the claim and does nothing.
func TestResumeConcurrent(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	id := NewV7()
	e := &Plans{states: &sm.States{}, running: newRunning(), maxLastUpdate: 30 * time.Minute}
	store := &slowReadStore{
		id:        id,
		status:    workflow.Running,
		claimed:   func() bool { _, ok := e.running.wait(id); return ok },
		firstRead: make(chan struct{}),
		gate:      make(chan struct{}),
	}
	e.store = store

	var mu sync.Mutex
	runs := 0
	// Each run finishes the Plan durably before it ends, as End's writeEverything does.
	e.runner = func(_ string, req statemachine.Request[sm.Data], _ ...statemachine.Option) (statemachine.Request[sm.Data], error) {
		mu.Lock()
		runs++
		mu.Unlock()
		store.setStatus(workflow.Completed)
		req.Data.RecoveryStarted.Report(nil)
		return req, nil
	}

	// A: its read is slow.
	aDone := result.New[struct{}]()
	context.Pool(ctx).Submit(ctx, func() { aDone.Set(struct{}{}, e.Resume(ctx, id)) })
	// firstRead is only closed, so anything but a close means the Context ended.
	if _, res := chans.Get(ctx, store.firstRead); !res.Closed() {
		t.Fatalf("TestResumeConcurrent: first Resume never read the plan")
	}

	// B: resumes while A is still reading. It must find A's claim, wait for A to start the run, and not run it again.
	bDone := result.New[struct{}]()
	context.Pool(ctx).Submit(ctx, func() { bDone.Set(struct{}{}, e.Resume(ctx, id)) })

	close(store.gate)
	if _, err := aDone.Wait(ctx); err != nil {
		t.Fatalf("TestResumeConcurrent: first Resume: got err == %s, want err == nil", err)
	}
	if _, err := bDone.Wait(ctx); err != nil {
		t.Fatalf("TestResumeConcurrent: second Resume: got err == %s, want err == nil", err)
	}
	if _, err := e.Wait(ctx, id); err != nil {
		t.Fatalf("TestResumeConcurrent: Wait: got err == %s, want err == nil", err)
	}

	store.mu.Lock()
	unclaimed := store.unclaimed
	store.mu.Unlock()
	// The Wait above reads with nothing claimed, so exactly one unclaimed read is expected.
	if unclaimed != 1 {
		t.Errorf("TestResumeConcurrent: got %d reads without the claim held, want only Wait's", unclaimed)
	}
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("TestResumeConcurrent: the plan ran %d times, want 1", runs)
	}
}

// TestClaimWhileDeciding is a regression test: Start and Resume hold the claim while they read and validate the plan,
// which can take minutes against a throttled store, and a second caller that lost the claim returned nil at once, as if
// a run were starting, even when the holder then found nothing to run and let the claim go. The second caller must wait
// for the holder's decision; given a Context that has already ended, it must say it stopped waiting, not that the plan
// started.
func TestClaimWhileDeciding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// status is the stored plan's status. With the zero submit time the plan has, Start cannot start it, and
		// Resume cannot resume a NotStarted plan, so for those the first caller lets the claim go without a run. Resume
		// resumes a Running plan, so the first caller launches a run.
		status workflow.Status
		call   func(e *Plans, ctx context.Context, id uuid.UUID) error
		// liveLoser gives the second caller a Context that has not ended, so it waits for the first caller's decision.
		// Otherwise its Context has already ended.
		liveLoser bool
		// wantAErr is whether the first caller, which holds the claim, fails.
		wantAErr bool
		// wantBErr is whether the second caller, which loses the claim, fails.
		wantBErr bool
	}{
		{
			name:      "Success: a Resume that loses the claim to a Resume that launches a run reports success",
			status:    workflow.Running,
			call:      (*Plans).Resume,
			liveLoser: true,
		},
		{
			name:     "Error: a Resume that loses the claim and cannot wait for the Resume still deciding does not report success",
			status:   workflow.Running,
			call:     (*Plans).Resume,
			wantBErr: true,
		},
		{
			name:      "Error: a Resume that loses the claim to a Resume that cannot resume the plan does not report success",
			status:    workflow.NotStarted,
			call:      (*Plans).Resume,
			liveLoser: true,
			wantAErr:  true,
			wantBErr:  true,
		},
		{
			name:      "Success: a Start that loses the claim to a Start of a plan already Running reports success",
			status:    workflow.Running,
			call:      (*Plans).Start,
			liveLoser: true,
		},
		{
			name:      "Error: a Start that loses the claim to a Start that cannot start the plan does not report success",
			status:    workflow.NotStarted,
			call:      (*Plans).Start,
			liveLoser: true,
			wantAErr:  true,
			wantBErr:  true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		id := NewV7()
		runner := &resumeRunner{release: make(chan struct{})}
		t.Cleanup(func() { close(runner.release) })
		e := &Plans{runner: runner.Run, states: &sm.States{}, running: newRunning(), maxLastUpdate: 30 * time.Minute, maxSubmit: 30 * time.Minute}
		e.addValidators()
		store := &slowReadStore{
			id:        id,
			status:    test.status,
			claimed:   func() bool { return true },
			firstRead: make(chan struct{}),
			gate:      make(chan struct{}),
		}
		e.store = store

		// A: holds the claim while its read is slow.
		aDone := result.New[struct{}]()
		context.Pool(ctx).Submit(ctx, func() { aDone.Set(struct{}{}, test.call(e, ctx, id)) })
		// firstRead is only closed, so anything but a close means the Context ended.
		if _, res := chans.Get(ctx, store.firstRead); !res.Closed() {
			t.Fatalf("TestClaimWhileDeciding(%s): first caller never read the plan", test.name)
		}

		// B: loses the claim. With an ended Context it has no time to wait for A's decision; with a live one it waits
		// on A, which is released once B is under way.
		bCtx, cancel := context.WithCancel(ctx)
		if !test.liveLoser {
			cancel()
		}
		bDone := result.New[struct{}]()
		context.Pool(ctx).Submit(ctx, func() { bDone.Set(struct{}{}, test.call(e, bCtx, id)) })
		if !test.liveLoser {
			_, _ = bDone.Wait(ctx)
		}
		close(store.gate)
		_, err := bDone.Wait(ctx)
		cancel()
		_, aErr := aDone.Wait(ctx)

		switch {
		case err == nil && test.wantBErr:
			t.Errorf("TestClaimWhileDeciding(%s): got err == nil from the caller that lost the claim, want err != nil", test.name)
		case err != nil && !test.wantBErr:
			t.Errorf("TestClaimWhileDeciding(%s): got err == %s from the caller that lost the claim, want err == nil", test.name, err)
		}
		switch {
		case aErr == nil && test.wantAErr:
			t.Errorf("TestClaimWhileDeciding(%s): got err == nil from the first caller, want the plan it could not run", test.name)
		case aErr != nil && !test.wantAErr:
			t.Errorf("TestClaimWhileDeciding(%s): got err == %s from the first caller, want err == nil", test.name, aErr)
		}
	}
}

func TestValidateStartState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name:    "Error: a nil plan is invalid",
			wantErr: true,
		},
		{
			name:    "Error: a plan submitted longer ago than maxSubmit is too old to start",
			plan:    &workflow.Plan{ID: NewV7(), SubmitTime: time.Now().Add(-time.Hour)},
			wantErr: true,
		},
		{
			name: "Success: a recently submitted plan is valid to start",
			plan: &workflow.Plan{ID: NewV7(), SubmitTime: time.Now()},
		},
	}

	for _, test := range tests {
		p := &Plans{maxSubmit: 30 * time.Minute}

		err := p.validateStartState(test.plan)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateStartState(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateStartState(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidatePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "Success: an object that is not a plan is ignored",
			item: walk.Item{
				Value: &workflow.Action{},
			},
		},
		{
			name: "Error: a plan whose SubmitTime is the zero value is invalid",
			item: walk.Item{
				Value: &workflow.Plan{},
			},
			wantErr: true,
		},
		{
			name: "Error: a plan whose Reason is not FRUnknown is invalid",
			item: walk.Item{
				Value: &workflow.Plan{
					SubmitTime: time.Now(),
					Reason:     workflow.FRBlock,
				},
			},
			wantErr: true,
		},
		{
			name: "Success: a submitted plan with no Reason is valid",
			item: walk.Item{
				Value: &workflow.Plan{
					SubmitTime: time.Now(),
				},
			},
		},
	}

	for _, test := range tests {
		p := &Plans{}
		p.addValidators()

		err := p.validatePlan(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidatePlan(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidatePlan(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidateAction(t *testing.T) {
	t.Parallel()

	checkPlugin := "checkPlugin"

	reg := registry.New()
	reg.Register(&testplugins.Plugin{})
	reg.Register(&testplugins.Plugin{PlugName: checkPlugin, IsCheckPlugin: true})

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "Success: an object that is not an action is ignored",
			item: walk.Item{
				Value: &workflow.Plan{},
			},
		},
		{
			name: "Error: an action that already has Attempts is invalid",
			item: func() walk.Item {
				a := &workflow.Action{Plugin: testplugins.Name}
				a.Attempts.Set([]workflow.Attempt{{}})
				return walk.Item{Chain: []workflow.Object{&workflow.Sequence{}}, Value: a}
			}(),
			wantErr: true,
		},
		{
			name: "Error: an action whose plugin is not registered is invalid",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Sequence{}},
				Value: &workflow.Action{
					Plugin: "not here",
				},
			},
			wantErr: true,
		},
		{
			name: "Error: an action in a Checks object whose plugin is not a check plugin is invalid",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Checks{}},
				Value: &workflow.Action{
					Plugin: testplugins.Name,
				},
			},
			wantErr: true,
		},
		{
			name: "Success: an action in a Sequence with a registered plugin is valid",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Sequence{}},
				Value: &workflow.Action{
					Plugin: testplugins.Name,
				},
			},
		},
		{
			name: "Success: an action in a Checks object with a check plugin is valid",
			item: walk.Item{
				Chain: []workflow.Object{&workflow.Checks{}},
				Value: &workflow.Action{
					Plugin: checkPlugin,
				},
			},
		},
	}

	for _, test := range tests {
		p := &Plans{registry: reg}
		p.addValidators()

		err := p.validateAction(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateAction(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateAction(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

func TestValidateID(t *testing.T) {
	t.Parallel()

	id := NewV7()

	// This adds compile level checking that all the object types implement the ider interface.
	iders := []ider{
		&workflow.Plan{ID: id},
		&workflow.Checks{ID: id},
		&workflow.Block{ID: id},
		&workflow.Sequence{ID: id},
		&workflow.Action{ID: id},
	}

	for _, tIDer := range iders {
		p := &Plans{}
		p.addValidators()

		item := walk.Item{
			Value: tIDer.(workflow.Object), // Compile check that each implements workflow.Object.
		}

		err := p.validateID(item)
		if err != nil {
			t.Errorf("TestValidateID(%T): got err == %v, want err == nil", tIDer, err)
		}
	}
}

func TestValidateState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		item    walk.Item
		wantErr bool
	}{
		{
			name: "Error: an object whose Status is not NotStarted is invalid",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{Status: workflow.Running})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "Error: an object whose Start is set is invalid",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{Start: time.Now()})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "Error: an object whose End is set is invalid",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{End: time.Now()})
					return p
				}(),
			},
			wantErr: true,
		},
		{
			name: "Success: an object that has not started is valid",
			item: walk.Item{
				Value: func() *workflow.Plan {
					p := &workflow.Plan{}
					p.State.Set(workflow.State{})
					return p
				}(),
			},
		},
	}
	for _, test := range tests {
		p := &Plans{}
		p.addValidators()

		err := p.validateState(test.item)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestValidateState(%s): got err == nil, want err != nil", test.name)
		case !test.wantErr && err != nil:
			t.Errorf("TestValidateState(%s): got err == %v, want err == nil", test.name, err)
		}
	}
}

// methodName returns the name of the method of the given value.
func methodName(method any) string {
	if method == nil {
		return "<nil>"
	}
	valueOf := reflect.ValueOf(method)
	switch valueOf.Kind() {
	case reflect.Func:
		return strings.TrimSuffix(strings.TrimSuffix(runtime.FuncForPC(valueOf.Pointer()).Name(), "-fm"), "[...]")
	default:
		return "<not a function>"
	}
}

// notFoundStore always reports the plan missing, so no caller can ever start a run.
type notFoundStore struct{ storage.Vault }

func (notFoundStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	return nil, fmt.Errorf("plan(%s): %w", id, storage.ErrNotFound)
}

// TestClaimDecided is a regression test: a caller that lost the claim could be told the plan was starting when the
// holder had not decided yet (ownership was published before the decision) or had already decided to release (the
// decision was signalled before ownership was withdrawn), and a late launch report could decide a later claim on the
// same id. With a store that never has the plan, no call can ever start a run, so every concurrent Start or Resume
// must return the not-found error and none may return nil. With a store that has every plan Running, every concurrent
// Resume must return nil and exactly one run may start per plan.
func TestClaimDecided(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*Plans, context.Context, uuid.UUID) error
		// running makes every plan Running in storage; otherwise no plan is in storage.
		running bool
		wantErr bool
	}{
		{
			name:    "Success: every concurrent Resume on a Running plan reports no error and one run starts per plan",
			call:    (*Plans).Resume,
			running: true,
		},
		{
			name:    "Error: every concurrent Start on a missing plan reports not found",
			call:    (*Plans).Start,
			wantErr: true,
		},
		{
			name:    "Error: every concurrent Resume on a missing plan reports not found",
			call:    (*Plans).Resume,
			wantErr: true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		runner := &resumeRunner{release: make(chan struct{})}
		t.Cleanup(func() { close(runner.release) })
		var store storage.Vault = notFoundStore{}
		if test.running {
			store = &scriptedStore{reads: []scriptedRead{{status: workflow.Running}}}
		}
		e := &Plans{store: store, runner: runner.Run, states: &sm.States{}, running: newRunning(), maxLastUpdate: 30 * time.Minute, maxSubmit: 30 * time.Minute}
		e.addValidators()

		const ids, callers = 32, 100
		var nils, notFound atomic.Int64
		g := context.Pool(ctx).Group()
		for i := 0; i < ids; i++ {
			id := NewV7()
			for j := 0; j < callers; j++ {
				g.Go(ctx, func(ctx context.Context) error {
					err := test.call(e, ctx, id)
					switch {
					case err == nil:
						nils.Add(1)
					// retryResume's resumeOrExit branches on errors.IsNotFound to stop retrying a plan that is gone.
					case errors.IsNotFound(err):
						notFound.Add(1)
					}
					return nil
				})
			}
		}
		if err := g.Wait(ctx); err != nil {
			t.Errorf("TestClaimDecided(%s): got err == %s from the group, want err == nil", test.name, err)
			continue
		}
		wantNils, wantNotFound, wantClaims, wantRuns := int64(0), int64(ids*callers), 0, 0
		if !test.wantErr {
			// Every run is held in flight, so each plan keeps its one claim.
			wantNils, wantNotFound, wantClaims, wantRuns = ids*callers, 0, ids, ids
		}
		if got := nils.Load(); got != wantNils {
			t.Errorf("TestClaimDecided(%s): got %d calls returning nil, want %d", test.name, got, wantNils)
		}
		if got := notFound.Load(); got != wantNotFound {
			t.Errorf("TestClaimDecided(%s): got %d not-found errors, want %d", test.name, got, wantNotFound)
		}
		if got := e.running.claims.Len(); got != wantClaims {
			t.Errorf("TestClaimDecided(%s): got claims.Len() == %d after all calls, want %d", test.name, got, wantClaims)
		}
		if got := runner.Runs(); got != wantRuns {
			t.Errorf("TestClaimDecided(%s): got %d runs, want %d", test.name, got, wantRuns)
		}
	}
}

// poolRunner stands in for the statemachine: it submits a job to the pool on the run's Context, as the statemachine's
// sequences do, and sets got to whether that job ran within a bound.
type poolRunner struct {
	got *result.Value[bool]
}

func (r poolRunner) Run(_ string, req statemachine.Request[sm.Data], _ ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	ctx, cancel := context.WithTimeout(req.Ctx, 2*time.Second)
	defer cancel()
	ran := make(chan struct{}, 1)
	ok := context.Pool(req.Ctx).Submit(ctx, func() { ran <- struct{}{} })
	if ok {
		_, res := chans.Get(ctx, ran)
		ok = res.OK()
	}
	r.got.Set(ok, nil)
	return req, nil
}

// TestLaunchPool is a regression test: a run's Context kept the pool of the Context given to Start. If the caller had
// attached a Limited pool, the run's own work shared its slots, so a run started while they were all held could not
// make progress. A run must use the default pool.
func TestLaunchPool(t *testing.T) {
	t.Parallel()

	id := NewV7()
	plan := &workflow.Plan{ID: id, SubmitTime: time.Now()}
	plan.State.Set(workflow.State{Status: workflow.NotStarted})

	runner := poolRunner{got: result.New[bool]()}
	e := &Plans{
		store:     &fakeStore{m: map[uuid.UUID]*workflow.Plan{id: plan}},
		runner:    runner.Run,
		states:    &sm.States{},
		running:   newRunning(),
		maxSubmit: 30 * time.Minute,
	}
	e.addValidators()

	// A Limited pool whose only slot is held for the whole test.
	limited := context.Pool(t.Context()).Limited(t.Context(), "TestLaunchPool", 1)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	if !limited.Submit(t.Context(), func() { <-hold }) {
		t.Fatalf("TestLaunchPool: could not take the limited pool's slot")
	}

	if err := e.Start(context.SetPool(t.Context(), limited), id); err != nil {
		t.Fatalf("TestLaunchPool: got err == %s, want err == nil", err)
	}

	wctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ran, err := runner.got.Wait(wctx)
	if err != nil {
		t.Fatalf("TestLaunchPool: the run never reported: %s", err)
	}
	if !ran {
		t.Errorf("TestLaunchPool: got the run's work blocked on the caller's limited pool, want it run on the default pool")
	}
}

// keyRecorder is a Context that records the keys looked up with Value.
type keyRecorder struct {
	context.Context

	keys []any
}

func (k *keyRecorder) Value(key any) any {
	k.keys = append(k.keys, key)
	return k.Context.Value(key)
}

// loggerCtx is a Context that carries logger under the key context.Log looks up.
type loggerCtx struct {
	context.Context

	key    any
	logger *slog.Logger
}

func (l loggerCtx) Value(key any) any {
	if key == l.key {
		return l.logger
	}
	return l.Context.Value(key)
}

// withLogger returns ctx carrying logger for context.Log, without touching the process-wide default logger.
func withLogger(ctx context.Context, logger *slog.Logger) context.Context {
	rec := &keyRecorder{Context: ctx}
	context.Log(rec)
	return loggerCtx{Context: ctx, key: rec.keys[0], logger: logger}
}

// ctxHandler is a slog.Handler that sets got to the error of the Context the first error-level record about the
// Plan id was logged with.
type ctxHandler struct {
	id   string
	once *sync.Once
	got  *result.Value[error]
}

func (h ctxHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level != slog.LevelError {
		return nil
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "id" && a.Value.String() == h.id {
			h.once.Do(func() { h.got.Set(ctx.Err(), nil) })
			return false
		}
		return true
	})
	return nil
}

func (h ctxHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h ctxHandler) WithGroup(string) slog.Handler      { return h }

// failRunner stands in for the statemachine: it waits until proceed is closed, then fails the run.
type failRunner struct {
	proceed chan struct{}
}

func (r failRunner) Run(_ string, req statemachine.Request[sm.Data], _ ...statemachine.Option) (statemachine.Request[sm.Data], error) {
	<-r.proceed
	return req, errors.New("run failed")
}

// TestLaunchContext is a regression test: a run logged its outcome with the Context given to Start, which ends long
// before a Plan does, instead of the run's own Context, which carries the same values but stays live for the run. A
// handler that honours the Context then saw a cancelled one and could drop the record.
func TestLaunchContext(t *testing.T) {
	t.Parallel()

	id := NewV7()
	plan := &workflow.Plan{ID: id, SubmitTime: time.Now()}
	plan.State.Set(workflow.State{Status: workflow.NotStarted})

	runner := failRunner{proceed: make(chan struct{})}
	e := &Plans{
		store:     &fakeStore{m: map[uuid.UUID]*workflow.Plan{id: plan}},
		runner:    runner.Run,
		states:    &sm.States{},
		running:   newRunning(),
		maxSubmit: 30 * time.Minute,
	}
	e.addValidators()

	h := ctxHandler{id: id.String(), once: &sync.Once{}, got: result.New[error]()}
	ctx, cancel := context.WithCancel(withLogger(t.Context(), slog.New(h)))
	if err := e.Start(ctx, id); err != nil {
		cancel()
		t.Fatalf("TestLaunchContext: got err == %s, want err == nil", err)
	}
	// The caller is done with its Context once Start returns; the run goes on.
	cancel()
	close(runner.proceed)

	wctx, wcancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer wcancel()
	logged, err := h.got.Wait(wctx)
	if err != nil {
		t.Fatalf("TestLaunchContext: the run's failure was never logged: %s", err)
	}
	if logged != nil {
		t.Errorf("TestLaunchContext: got the run's failure logged with an ended Context (%s), want the run's live Context", logged)
	}
}

// doneSignal is a Context that closes called the first time its Done method is called, which is the point a waiter
// begins to block on it. fire closes called too, for a caller that returns without blocking.
type doneSignal struct {
	context.Context

	called chan struct{}
	once   *sync.Once
}

func newDoneSignal(ctx context.Context) doneSignal {
	return doneSignal{Context: ctx, called: make(chan struct{}), once: &sync.Once{}}
}

func (d doneSignal) Done() <-chan struct{} {
	d.fire()
	return d.Context.Done()
}

func (d doneSignal) fire() {
	d.once.Do(func() { close(d.called) })
}
