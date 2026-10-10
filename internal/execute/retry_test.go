package execute

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// scriptedRead is what one scriptedStore.Read returns: err if set, otherwise the Plan with status. A read with block
// set closes the store's entered channel and waits for its gate to close before it returns.
type scriptedRead struct {
	status workflow.Status
	err    error
	block  bool
}

// scriptedStore is a storage.Vault that hands id and extra to startup recovery as Running Plans. Each Read returns
// the next entry of reads, repeating the last one.
type scriptedStore struct {
	storage.Vault

	id    uuid.UUID
	extra []uuid.UUID
	mu    sync.Mutex
	reads []scriptedRead
	n     int
	// entered and gate are used by a read with block set.
	entered chan struct{}
	gate    chan struct{}
	// honorCtx makes Read fail with the Context's error once the Context has ended, as a real store does. It is off
	// by default because TestResumeOrExit runs attempts on a Context already past its deadline and relies on the
	// scripted reads going through regardless.
	honorCtx bool
}

func (s *scriptedStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	if s.honorCtx {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	r := s.reads[min(s.n, len(s.reads)-1)]
	s.n++
	s.mu.Unlock()

	if r.block {
		close(s.entered)
		<-s.gate
	}
	if r.err != nil {
		return nil, r.err
	}
	plan := &workflow.Plan{ID: id}
	plan.State.Set(workflow.State{Status: r.status, Start: time.Now()})
	return plan, nil
}

func (s *scriptedStore) UpdatePlan(ctx context.Context, plan *workflow.Plan) error { return nil }

func (s *scriptedStore) RecoveredRunning() ([]storage.ListResult, bool) {
	results := []storage.ListResult{{ID: s.id}}
	for _, id := range s.extra {
		results = append(results, storage.ListResult{ID: id})
	}
	return results, true
}

func TestRestartBy(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name       string
		lastUpdate time.Time
		want       time.Time
	}{
		{
			name:       "Success: a recently updated plan restarts once half the limit has passed since its last update",
			lastUpdate: now.Add(-5 * time.Minute),
			want:       now.Add(10 * time.Minute),
		},
		{
			name:       "Success: a plan already past half the limit restarts a minute from now, not at once",
			lastUpdate: now.Add(-20 * time.Minute),
			want:       now.Add(minRestartDelay),
		},
	}

	for _, test := range tests {
		if got := restartBy(test.lastUpdate, 30*time.Minute, now); !got.Equal(test.want) {
			t.Errorf("TestRestartBy(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestResumeOrExit(t *testing.T) {
	t.Parallel()

	// busy is storage that is still failing after the store ran out of retries, which marks the error permanent.
	busy := scriptedRead{err: fmt.Errorf("storage busy: %w", errors.ErrPermanent)}
	running := []scriptedRead{{status: workflow.Running}}
	inconsistent := scriptedRead{err: errors.E(t.Context(), errors.CatInternal, errors.TypeStorageInconsistent, errors.New("missing a sub-object"))}

	tests := []struct {
		name  string
		reads []scriptedRead
		// pastRestart puts the restart time in the past; restartSoon puts it a second away, which leaves the attempt
		// ample time to launch its run before the restart time even under -race.
		pastRestart bool
		restartSoon bool
		// startedRun means a run from Start holds the Plan's claim.
		startedRun bool
		// failedRun means a recovered run reported that it could not start but has not ended, so it still holds the
		// Plan's claim.
		failedRun bool
		// The recovered runs this attempt launches: failStart reports that the run could not start, holdReport holds
		// the report and holdFailed holds a failed run in flight, each until the runs are released.
		failStart  bool
		holdReport bool
		holdFailed bool
		// releaseAfter, if set, releases held runs once the attempt's run has begun and the restart time has passed, and
		// then this much longer, so the attempt reaches its restart check while the run still holds the Plan's claim.
		// Release is driven by those events, not by a fixed delay from the start of the attempt.
		releaseAfter time.Duration
		// While the restart check's storage read (the read with block set) is in progress, concurrentResume has
		// another caller Resume the Plan, and cancelDuringRead cancels the attempt's Context.
		concurrentResume bool
		cancelDuringRead bool

		wantExit bool
		wantErr  bool
	}{
		{
			name:  "Success: a plan that resumes needs no more attempts",
			reads: running,
		},
		{
			name:  "Success: a plan that no longer exists needs no more attempts",
			reads: []scriptedRead{{err: storage.ErrNotFound}},
		},
		{
			name:    "Error: a plan that fails to resume before its restart time is attempted again",
			reads:   []scriptedRead{busy},
			wantErr: true,
		},
		{
			// Regression: Resume saw the failed run's claim as a run in flight and returned nil, which ended the
			// attempts while the run was about to end and leave the Plan stranded again.
			name:       "Error: a plan whose recovered run failed to start but still holds its claim is attempted again",
			reads:      running,
			failedRun:  true,
			failStart:  true,
			holdFailed: true,
			wantErr:    true,
		},
		{
			name:  "Success: a plan whose storage is inconsistent needs no more attempts, since retrying cannot fix it",
			reads: []scriptedRead{inconsistent},
		},
		{
			name:        "Success: a plan whose storage is inconsistent at its restart time does not exit the process",
			reads:       []scriptedRead{busy, inconsistent},
			pastRestart: true,
		},
		{
			name:        "Success: a plan still stranded at its restart time exits the process",
			reads:       []scriptedRead{busy},
			pastRestart: true,
			wantExit:    true,
		},
		{
			name:        "Success: a plan a started run holds at its restart time does not exit the process",
			reads:       []scriptedRead{busy},
			pastRestart: true,
			startedRun:  true,
		},
		{
			name:        "Success: a plan that finished by its restart time does not exit the process",
			reads:       []scriptedRead{busy, {status: workflow.Completed}},
			pastRestart: true,
		},
		{
			// Regression: at the restart time a claim was taken as a run executing the Plan, so the attempts ended
			// while this attempt's recovery was still setting up; it then failed and let the claim go, stranding the
			// Plan with nothing left to retry it or exit.
			name:         "Success: a plan whose recovery is still setting up at its restart time exits the process once it fails",
			reads:        running,
			restartSoon:  true,
			failStart:    true,
			holdReport:   true,
			releaseAfter: 200 * time.Millisecond,
			wantExit:     true,
		},
		{
			// Regression: as above, for a recovery that had reported failure but not yet let its claim go.
			name:         "Success: a plan whose recovery failed but still holds its claim at its restart time exits the process",
			reads:        running,
			pastRestart:  true,
			failStart:    true,
			holdFailed:   true,
			releaseAfter: 200 * time.Millisecond,
			wantExit:     true,
		},
		{
			// Regression: the restart check read storage without holding the claim, so a caller could Resume the Plan
			// during the read; storage then said Running because of that healthy run, and the process exited.
			name:             "Success: a plan another caller resumes during the restart check cannot start before the process exits",
			reads:            []scriptedRead{busy, {status: workflow.Running, block: true}, {status: workflow.Running}},
			pastRestart:      true,
			concurrentResume: true,
			wantExit:         true,
		},
		{
			// Regression: a storage read ended by shutdown was taken as a stranded Plan, so shutting down exited 1.
			name:             "Success: a plan whose restart check is interrupted by shutdown does not exit the process",
			reads:            []scriptedRead{busy, {status: workflow.Running, block: true}},
			pastRestart:      true,
			cancelDuringRead: true,
		},
		{
			name:         "Success: a plan whose recovery is still setting up at its restart time and then starts does not exit the process",
			reads:        running,
			restartSoon:  true,
			holdReport:   true,
			releaseAfter: 200 * time.Millisecond,
		},
	}

	for _, test := range tests {
		id := NewV7()
		runner := &resumeRunner{release: make(chan struct{}), holdReport: test.holdReport, holdFailed: test.holdFailed, entered: make(chan struct{}, 4)}
		if test.failStart {
			runner.startErr = errors.New("storage busy")
		}
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(runner.release) }) }
		t.Cleanup(release)
		var exitCode atomic.Int32
		// resumed is set when another caller's Resume returns nil; resumedBeforeExit records whether that happened
		// before the process exited.
		var resumed, resumedBeforeExit atomic.Bool
		store := &scriptedStore{id: id, reads: test.reads, entered: make(chan struct{}), gate: make(chan struct{})}
		e := &Plans{
			store:         store,
			runner:        runner.Run,
			states:        &sm.States{},
			running:       newRunning(),
			maxLastUpdate: 30 * time.Minute,
			exit: func(code int) {
				resumedBeforeExit.Store(resumed.Load())
				exitCode.Store(int32(code))
			},
		}
		restartAt := time.Now().Add(time.Hour)
		switch {
		case test.pastRestart:
			restartAt = time.Now().Add(-time.Second)
		case test.restartSoon:
			restartAt = time.Now().Add(time.Second)
		}
		var held *claim
		if test.startedRun {
			held, _ = e.running.claim(id, func() {})
			held.launched(nil)
		}
		if test.failedRun {
			plan, err := e.store.Read(t.Context(), id)
			if err != nil {
				t.Fatalf("TestResumeOrExit(%s): Read: %s", test.name, err)
			}
			started := sm.NewStarted()
			if err := e.runPlan(t.Context(), plan, started); err != nil {
				t.Fatalf("TestResumeOrExit(%s): runPlan: %s", test.name, err)
			}
			if err := started.Wait(t.Context()); err == nil {
				t.Fatalf("TestResumeOrExit(%s): the recovered run got err == nil, want its start to fail", test.name)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		g := context.Pool(t.Context()).Group()
		if test.releaseAfter > 0 {
			g.Go(t.Context(), func(ctx context.Context) error {
				defer release()
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if _, res := chans.Get(ctx, runner.entered); !res.OK() {
					return fmt.Errorf("the attempt's run never began: %w", ctx.Err())
				}
				// time.After never fires early, so the restart time has passed when it does.
				if _, res := chans.Get(ctx, time.After(time.Until(restartAt)+test.releaseAfter)); res != chans.ResultOK {
					return fmt.Errorf("the restart time never passed: %w", ctx.Err())
				}
				return nil
			})
		}
		if test.concurrentResume || test.cancelDuringRead {
			g.Go(t.Context(), func(ctx context.Context) error {
				// entered is only closed, so anything but a close means the Context ended.
				if _, res := chans.Get(ctx, store.entered); !res.Closed() {
					close(store.gate)
					return ctx.Err()
				}
				if test.cancelDuringRead {
					cancel()
				}
				if test.concurrentResume {
					rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
					// blocked marks the point the Resume blocks on its Context, waiting on the restart check's claim, or
					// returns.
					blocked := newDoneSignal(rctx)
					g.Go(ctx, func(ctx context.Context) error {
						defer rcancel()
						defer blocked.fire()
						if err := e.Resume(blocked, id); err == nil {
							resumed.Store(true)
						}
						return nil
					})
					// called is only closed, so anything but a close means the Context ended.
					if _, res := chans.Get(rctx, blocked.called); !res.Closed() {
						close(store.gate)
						return fmt.Errorf("the other caller's Resume never blocked or returned: %w", rctx.Err())
					}
				}
				close(store.gate)
				return nil
			})
		}

		err := e.resumeOrExit(ctx, id, restartAt)
		if held != nil {
			held.release()
		}
		if err := g.Wait(t.Context()); err != nil {
			t.Fatalf("TestResumeOrExit(%s): helpers: %s", test.name, err)
		}
		cancel()
		if resumedBeforeExit.Load() {
			t.Errorf("TestResumeOrExit(%s): another caller resumed the plan before the process exited, want it held off until the exit", test.name)
		}

		if gotExit := exitCode.Load() == 1; gotExit != test.wantExit {
			t.Errorf("TestResumeOrExit(%s): got process exited with code 1 == %v, want %v", test.name, gotExit, test.wantExit)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestResumeOrExit(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestResumeOrExit(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			// retryResume (retry.go) runs resumeOrExit under context.Tasks(ctx).Run with e.retryBoff, and that
			// Backoff's Retry (github.com/Azure/retry/exponential) stops retrying once errors.Is(err, ErrPermanent).
			// A storage error that ran out of retries is permanent, so resumeOrExit passing it on would end the attempts.
			if errors.Is(err, errors.ErrPermanent) {
				t.Errorf("TestResumeOrExit(%s): got a permanent error, want one that is attempted again", test.name)
			}
			continue
		}
	}
}

// TestRecover is a regression test: when startup recovery could not write a recovered Plan (storage was throttled), it
// used to log the failure and leave the Plan Running in storage with nothing running it, until a caller happened to
// Resume it. A few failed starts must be retried until the Plans run; more than maxRetriedAtStartup exit the process.
func TestRecover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// plans is how many Plans startup recovery finds; each one's first start fails.
		plans int
		// unreadable instead makes the first read of the (single) Plan fail; its start does not fail.
		unreadable bool
		// cancelDuringRetry, with unreadable, cancels the Context given to recover while the first background retry is
		// reading storage, and makes that read fail too.
		cancelDuringRetry bool

		wantRuns int
		wantExit bool
	}{
		{
			name:     "Success: a plan that fails to start is retried until it runs",
			plans:    1,
			wantRuns: 2,
		},
		{
			name:     "Success: as many failed plans as the limit are each retried until they run",
			plans:    maxRetriedAtStartup,
			wantRuns: 2 * maxRetriedAtStartup,
		},
		{
			// Regression: a plan recovery could not read failed New, and so every restart.
			name:       "Success: a plan recovery cannot read is retried until it runs",
			plans:      1,
			unreadable: true,
			wantRuns:   1,
		},
		{
			// Regression: the retries ran on the Context given to New, so when that Context ended (an init timeout)
			// they stopped and the Plan was left Running in storage with nothing to retry it or exit.
			name:              "Success: a plan recovery cannot read is retried until it runs after the Context given to recover ends",
			plans:             1,
			unreadable:        true,
			cancelDuringRetry: true,
			wantRuns:          1,
		},
		{
			name:     "Success: more failed plans than the limit exit the process without retrying",
			plans:    maxRetriedAtStartup + 1,
			wantRuns: maxRetriedAtStartup + 1,
			wantExit: true,
		},
	}

	for _, test := range tests {
		store := &scriptedStore{id: NewV7(), reads: []scriptedRead{{status: workflow.Running}}}
		if test.unreadable {
			store.reads = []scriptedRead{{err: errors.New("storage busy")}, {status: workflow.Running}}
		}
		if test.cancelDuringRetry {
			busy := errors.New("storage busy")
			store.reads = []scriptedRead{{err: busy}, {err: busy, block: true}, {status: workflow.Running}}
			store.entered, store.gate = make(chan struct{}), make(chan struct{})
		}
		ids := []uuid.UUID{store.id}
		for len(ids) < test.plans {
			store.extra = append(store.extra, NewV7())
			ids = append(ids, store.extra[len(store.extra)-1])
		}
		runner := &resumeRunner{release: make(chan struct{}), entered: make(chan struct{}, 2*test.wantRuns)}
		if !test.unreadable {
			runner.startErr, runner.failRuns = errors.New("storage busy"), test.plans
		}
		t.Cleanup(func() { close(runner.release) })
		var exited atomic.Bool
		e := &Plans{
			store:         store,
			runner:        runner.Run,
			states:        &sm.States{},
			running:       newRunning(),
			maxLastUpdate: 30 * time.Minute,
			retryBoff: exponential.Must(exponential.New(exponential.WithPolicy(exponential.Policy{
				InitialInterval:     time.Millisecond,
				Multiplier:          2,
				RandomizationFactor: 0.5,
				MaxInterval:         10 * time.Millisecond,
			}))),
			exit: func(int) { exited.Store(true) },
		}

		ctx, cancel := context.WithCancel(t.Context())
		if err := e.recover(ctx); err != nil {
			cancel()
			t.Errorf("TestRecover(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		if test.cancelDuringRetry {
			// entered is only closed, so anything but a close means the Context ended.
			if _, res := chans.Get(t.Context(), store.entered); !res.Closed() {
				cancel()
				t.Fatalf("TestRecover(%s): the test ended before the background retry read storage", test.name)
			}
			cancel()
			close(store.gate)
		}

		// recover exits before it queues any retry, and only after every recovered run has begun and reported, so on
		// exit the runs are already final. Otherwise wait for the retried runs to begin.
		wctx, wcancel := context.WithTimeout(t.Context(), 10*time.Second)
		for i := 0; i < test.wantRuns; i++ {
			if _, res := chans.Get(wctx, runner.entered); !res.OK() {
				break
			}
		}
		wcancel()
		if got := runner.Runs(); got != test.wantRuns {
			t.Errorf("TestRecover(%s): got %d runs, want %d", test.name, got, test.wantRuns)
		}
		if got := exited.Load(); got != test.wantExit {
			t.Errorf("TestRecover(%s): got process exited == %v, want %v", test.name, got, test.wantExit)
		}
		cancel()
		if test.wantExit {
			continue
		}
		for _, id := range ids {
			if _, ok := e.running.wait(id); !ok {
				t.Errorf("TestRecover(%s): got no run in flight for plan(%s), want the retried run", test.name, id)
			}
		}
	}
}

// TestRecoverContextEnds is a regression test: startup recovery waited for each recovered Plan to report that it
// started on the Context given to New. When that Context ended (an init deadline) while recoveries were still setting
// up on slow storage, every such Plan was counted as having failed to start and queued for retry, and with more than
// maxRetriedAtStartup of them the process exited. The runs are detached from that Context, so the waits must be too.
func TestRecoverContextEnds(t *testing.T) {
	t.Parallel()

	plans := maxRetriedAtStartup + 1
	store := &scriptedStore{id: NewV7(), reads: []scriptedRead{{status: workflow.Running}}}
	for len(store.extra) < plans-1 {
		store.extra = append(store.extra, NewV7())
	}
	runner := &resumeRunner{release: make(chan struct{}), holdReport: true, entered: make(chan struct{}, plans)}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	t.Cleanup(release)
	var exited atomic.Bool
	e := &Plans{
		store:         store,
		runner:        runner.Run,
		states:        &sm.States{},
		running:       newRunning(),
		maxLastUpdate: 30 * time.Minute,
		retryBoff:     exponential.Must(exponential.New(exponential.WithPolicy(retryPolicy()))),
		exit:          func(int) { exited.Store(true) },
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recovered := make(chan struct{})
	g := context.Pool(t.Context()).Group()
	g.Go(t.Context(), func(ctx context.Context) error {
		// Every recovery is setting up and holding its report. End the Context given to recover then.
		for i := 0; i < plans; i++ {
			if _, res := chans.Get(ctx, runner.entered); !res.OK() {
				return fmt.Errorf("only %d of %d recoveries began: %w", i, plans, context.Cause(ctx))
			}
		}
		cancel()
		// Hold the reports until recover returns, which it does at once if it gives up on its waits when its Context
		// ends. If it waits on, as it must, it cannot return before the reports; let them go after a bound.
		wctx, wcancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer wcancel()
		chans.Get(wctx, recovered)
		release()
		return nil
	})

	err := e.recover(ctx)
	close(recovered)
	if gerr := g.Wait(t.Context()); gerr != nil {
		t.Fatalf("TestRecoverContextEnds: helper: %s", gerr)
	}
	if err != nil {
		t.Fatalf("TestRecoverContextEnds: got err == %s, want err == nil", err)
	}
	if exited.Load() {
		t.Errorf("TestRecoverContextEnds: got the process exited because recovered plans did not start, want the recoveries waited for")
	}
	if got := runner.Runs(); got != plans {
		t.Errorf("TestRecoverContextEnds: got %d runs, want %d", got, plans)
	}
}
