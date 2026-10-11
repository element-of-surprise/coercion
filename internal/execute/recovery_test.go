package execute

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/durable"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/google/uuid"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

// searchStore is a storage.Vault whose Search returns results, or err. With closeEarly set, Search instead closes the
// stream without sending anything, as a producer that stops when its Context ends.
type searchStore struct {
	storage.Vault

	results    []storage.ListResult
	err        error
	closeEarly bool
	searched   *bool
}

func (s searchStore) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	*s.searched = true
	if s.err != nil {
		return nil, s.err
	}
	if s.closeEarly {
		ch := make(chan storage.Stream[storage.ListResult])
		close(ch)
		return ch, nil
	}
	ch := make(chan storage.Stream[storage.ListResult], len(s.results))
	for _, lr := range s.results {
		ch <- storage.Stream[storage.ListResult]{Result: lr}
	}
	close(ch)
	return ch, nil
}

// snapshotStore is a searchStore that also implements storage.RecoveredRunning.
type snapshotStore struct {
	searchStore

	snapshot []storage.ListResult
	ok       bool
}

func (s snapshotStore) RecoveredRunning() ([]storage.ListResult, bool) {
	return s.snapshot, s.ok
}

func TestRecoverStart(t *testing.T) {
	t.Parallel()

	snapID := NewV7()
	searchID := NewV7()
	fromSnapshot := []storage.ListResult{{ID: snapID}}
	fromSearch := []storage.ListResult{{ID: searchID}}

	tests := []struct {
		name string
		// snapshot means the store implements storage.RecoveredRunning.
		snapshot  bool
		snapOK    bool
		searchErr error
		// cancelled ends the Context before recovery starts; the store's Search then closes its stream early.
		cancelled bool

		wantIDs      []uuid.UUID
		wantSearched bool
		wantErr      bool
	}{
		{
			name:     "Success: running plans handed over by the store's Recovery are used without a Search",
			snapshot: true,
			snapOK:   true,
			wantIDs:  []uuid.UUID{snapID},
		},
		{
			name:         "Success: a store with nothing handed over falls back to Search",
			snapshot:     true,
			wantIDs:      []uuid.UUID{searchID},
			wantSearched: true,
		},
		{
			name:         "Success: a store without RecoveredRunning uses Search",
			wantIDs:      []uuid.UUID{searchID},
			wantSearched: true,
		},
		{
			name:         "Error: a failed Search fails recovery",
			searchErr:    errors.New("search failed"),
			wantSearched: true,
			wantErr:      true,
		},
		{
			// Regression: a Search stream that closed early because the Context ended was taken as the full list, so
			// recovery went on with a truncated list and left the missing Running plans with nothing running them.
			name:         "Error: a Search stream cut short by the Context ending fails recovery",
			cancelled:    true,
			wantSearched: true,
			wantErr:      true,
		},
	}

	for _, test := range tests {
		searched := false
		ss := searchStore{results: fromSearch, err: test.searchErr, closeEarly: test.cancelled, searched: &searched}
		var store storage.Vault = ss
		if test.snapshot {
			store = snapshotStore{searchStore: ss, snapshot: fromSnapshot, ok: test.snapOK}
		}

		ctx, cancel := context.WithCancel(t.Context())
		if test.cancelled {
			cancel()
		}
		r := &recover{store: store}
		req := r.start(statemachine.Request[recoverData]{Ctx: ctx})
		cancel()

		if searched != test.wantSearched {
			t.Errorf("TestRecoverStart(%s): got Search called == %v, want %v", test.name, searched, test.wantSearched)
		}
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestRecoverStart(%s): got err == nil, want err != nil", test.name)
			continue
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestRecoverStart(%s): got err == %s, want err == nil", test.name, req.Err)
			continue
		case req.Err != nil:
			continue
		}

		var ids []uuid.UUID
		for _, res := range req.Data.searchResults {
			ids = append(ids, res.Result.ID)
		}
		if diff := pretty.Compare(test.wantIDs, ids); diff != "" {
			t.Errorf("TestRecoverStart(%s): plan IDs -want +got:\n%s", test.name, diff)
		}
	}
}

// writeOrderStore records the order of object and Plan writes. Like a real store, a write fails with the Context's
// error once the Context has ended. If failAction is set, UpdateAction returns it.
type writeOrderStore struct {
	storage.Vault

	writes     []string
	failAction error
}

func (w *writeOrderStore) UpdatePlan(ctx context.Context, p *workflow.Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.writes = append(w.writes, "plan:"+p.State.Get().Status.String())
	return nil
}

func (w *writeOrderStore) UpdateBlock(ctx context.Context, b *workflow.Block) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.writes = append(w.writes, "block:"+b.State.Get().Status.String())
	return nil
}

func (w *writeOrderStore) UpdateSequence(ctx context.Context, s *workflow.Sequence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.writes = append(w.writes, "sequence:"+s.State.Get().Status.String())
	return nil
}

func (w *writeOrderStore) UpdateAction(ctx context.Context, a *workflow.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.failAction != nil {
		return w.failAction
	}
	w.writes = append(w.writes, "action:"+a.State.Get().Status.String())
	return nil
}

// UpdateChanges writes the changed objects one at a time through the other Update methods, so the writes are recorded
// in the order a Vault that cannot group writes makes them.
func (w *writeOrderStore) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	return storage.WriteChanges(ctx, w, plan, before)
}

// TestAgedOut is a regression test: aging a Plan out used to flip its running objects to Failed in memory and
// write only the Plan. If that write was torn, the Plan read back from storage had Failed status but Running objects.
// The flipped objects must be written, each after its descendants, and the Plan last. A failed write also used to fail
// all of startup recovery, and so New on every restart; the Plan must instead be left to the background retry.
func TestAgedOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failAction error
		// cancelled ends the Context before the plan is aged out.
		cancelled bool

		wantWrites []string
		// wantUnrecovered means the Plan is left to the background retry.
		wantUnrecovered bool
		wantErr         bool
	}{
		{
			name:       "Success: running objects are written as Failed, each after its descendants, then the Plan",
			wantWrites: []string{"action:Failed", "sequence:Failed", "block:Failed", "plan:Failed"},
		},
		{
			name:            "Success: a failed object write stops before its parents or the Plan and leaves the Plan to the retry",
			failAction:      errors.New("storage busy"),
			wantWrites:      nil,
			wantUnrecovered: true,
		},
		{
			// Regression: a write cut off because New's Context ended was counted as storage failing and the plan left
			// to the background retry.
			name:       "Error: a write cut off by the Context ending fails recovery instead of counting as a storage failure",
			cancelled:  true,
			wantWrites: nil,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		running := workflow.State{Status: workflow.Running, Start: time.Now().Add(-time.Hour)}
		done := workflow.State{Status: workflow.Completed, Start: time.Now().Add(-time.Hour), End: time.Now().Add(-time.Hour)}

		action := &workflow.Action{ID: NewV7()}
		action.State.Set(running)
		doneAction := &workflow.Action{ID: NewV7()}
		doneAction.State.Set(done)
		seq := &workflow.Sequence{ID: NewV7(), Actions: []*workflow.Action{doneAction, action}}
		seq.State.Set(running)
		block := &workflow.Block{ID: NewV7(), Sequences: []*workflow.Sequence{seq}}
		block.State.Set(running)
		plan := &workflow.Plan{ID: NewV7(), Blocks: []*workflow.Block{block}}
		plan.State.Set(running)

		ctx, cancel := context.WithCancel(t.Context())
		if test.cancelled {
			cancel()
		}
		store := &writeOrderStore{failAction: test.failAction}
		r := &recover{store: store}
		req := r.agedOut(statemachine.Request[recoverData]{Ctx: ctx, Data: recoverData{agedOut: []*workflow.Plan{plan}}})
		cancel()

		if diff := pretty.Compare(test.wantWrites, store.writes); diff != "" {
			t.Errorf("TestAgedOut(%s): writes -want +got:\n%s", test.name, diff)
		}
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestAgedOut(%s): got err == nil, want err != nil", test.name)
			continue
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestAgedOut(%s): got err == %s, want err == nil", test.name, req.Err)
			continue
		case req.Err != nil:
			continue
		}
		if got := len(req.Data.unrecovered) == 1 && req.Data.unrecovered[0].id == plan.ID; got != test.wantUnrecovered {
			t.Errorf("TestAgedOut(%s): got plan left to the retry == %v, want %v", test.name, got, test.wantUnrecovered)
		}
		if plan.Reason != workflow.FRExceedRecovery {
			t.Errorf("TestAgedOut(%s): got plan reason %v, want %v", test.name, plan.Reason, workflow.FRExceedRecovery)
		}
	}
}

// TestFetchPlans is a regression test: a Running plan that could not be read failed all of startup recovery, and so
// New on every restart. It must be skipped, or left to the background retry if storage is only failing for now.
func TestFetchPlans(t *testing.T) {
	t.Parallel()

	inconsistent := errors.E(t.Context(), errors.CatInternal, errors.TypeStorageInconsistent, errors.New("missing a sub-object"))

	tests := []struct {
		name string
		read scriptedRead
		// cancelled ends the Context before the plans are read.
		cancelled bool

		wantRecovered   bool
		wantUnrecovered bool
		wantErr         bool
	}{
		{
			name:          "Success: a readable plan is recovered",
			read:          scriptedRead{status: workflow.Running},
			wantRecovered: true,
		},
		{
			name: "Success: a plan that no longer exists is skipped",
			read: scriptedRead{err: storage.ErrNotFound},
		},
		{
			name: "Success: a plan whose storage is inconsistent is skipped, since retrying cannot fix it",
			read: scriptedRead{err: inconsistent},
		},
		{
			name:            "Success: a plan that cannot be read for now is left to the background retry",
			read:            scriptedRead{err: errors.New("storage busy")},
			wantUnrecovered: true,
		},
		{
			// Regression: a read cut off because New's Context ended was counted as storage failing and the plan left
			// to the background retry, which could push recovery over maxRetriedAtStartup and exit the process.
			name:      "Error: a read cut off by the Context ending fails recovery instead of counting as a storage failure",
			read:      scriptedRead{status: workflow.Running},
			cancelled: true,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		id := NewV7()
		ctx, cancel := context.WithCancel(t.Context())
		if test.cancelled {
			cancel()
		}
		r := &recover{maxAge: 30 * time.Minute, store: &scriptedStore{id: id, reads: []scriptedRead{test.read}, honorCtx: true}}
		req := r.fetchPlans(statemachine.Request[recoverData]{
			Ctx:  ctx,
			Data: recoverData{searchResults: []storage.Stream[storage.ListResult]{{Result: storage.ListResult{ID: id}}}},
		})
		cancel()

		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestFetchPlans(%s): got err == nil, want err != nil", test.name)
			continue
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestFetchPlans(%s): got err == %s, want err == nil", test.name, req.Err)
			continue
		case req.Err != nil:
			continue
		}
		if got := len(req.Data.plans) == 1 && req.Data.plans[0].ID == id; got != test.wantRecovered {
			t.Errorf("TestFetchPlans(%s): got plan recovered == %v, want %v", test.name, got, test.wantRecovered)
		}
		if got := len(req.Data.unrecovered) == 1 && req.Data.unrecovered[0].id == id; got != test.wantUnrecovered {
			t.Errorf("TestFetchPlans(%s): got plan left to the retry == %v, want %v", test.name, got, test.wantUnrecovered)
		}
	}
}

// stalePlan returns a Running Plan whose pre-check action is mid-attempt, all last touched an hour ago.
func stalePlan() *workflow.Plan {
	start := time.Now().Add(-time.Hour)
	action := &workflow.Action{ID: NewV7()}
	action.State.Set(workflow.State{Status: workflow.Running, Start: start})
	action.Attempts.Set([]workflow.Attempt{{Start: start}})
	pre := &workflow.Checks{ID: NewV7(), Actions: []*workflow.Action{action}}
	pre.State.Set(workflow.State{Status: workflow.Running, Start: start})
	plan := &workflow.Plan{ID: NewV7(), PreChecks: pre}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// staleBlockPlan returns a Running Plan whose block, sequence and action are all Running, last touched an hour ago.
func staleBlockPlan() *workflow.Plan {
	start := time.Now().Add(-time.Hour)
	action := &workflow.Action{ID: NewV7()}
	action.State.Set(workflow.State{Status: workflow.Running, Start: start})
	action.Attempts.Set([]workflow.Attempt{{Start: start}})
	seq := &workflow.Sequence{ID: NewV7(), Actions: []*workflow.Action{action}}
	seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
	block := &workflow.Block{ID: NewV7(), Sequences: []*workflow.Sequence{seq}}
	block.State.Set(workflow.State{Status: workflow.Running, Start: start})
	plan := &workflow.Plan{ID: NewV7(), Blocks: []*workflow.Block{block}}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// recoverStored does what startup recovery does with what store holds: ages the Plan out if it is past the limit,
// otherwise recovers it with sm.Recovery.
func recoverStored(t *testing.T, store *durable.Store, id uuid.UUID) error {
	t.Helper()

	plan, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatalf("recoverStored: Read: %s", err)
	}
	if isAgedOut(t.Context(), plan, 30*time.Minute, time.Now()) {
		return ageOut(t.Context(), store, plan)
	}
	states, err := sm.New(t.Context(), store, registry.New(), 30*time.Minute)
	if err != nil {
		t.Fatalf("recoverStored: sm.New: %s", err)
	}
	req := states.Recovery(statemachine.Request[sm.Data]{Ctx: t.Context(), Data: sm.Data{Plan: plan, RecoveryStarted: sm.NewStarted()}})
	return req.Err
}

// resumeStored does what Plans.Resume does with what store holds. It reports an error if a run was launched, since
// a Plan past the limit must be aged out, not run.
func resumeStored(t *testing.T, store *durable.Store, id uuid.UUID) error {
	t.Helper()

	runner := &resumeRunner{release: make(chan struct{})}
	t.Cleanup(func() { close(runner.release) })
	e := &Plans{store: store, runner: runner.Run, states: &sm.States{}, running: newRunning(), maxLastUpdate: 30 * time.Minute}
	if err := e.Resume(t.Context(), id); err != nil {
		return err
	}
	if runner.Runs() != 0 {
		return errors.New("Resume launched a run for a Plan past the recovery limit")
	}
	return nil
}

// TestAgeOutRetry is a regression test for two ways an interrupted age-out could end up recovering a stale Plan:
// ageOut wrote a Plan's objects parents first, so a written parent hid children a retry never fixed; and the objects it
// wrote ended at the current time, so after a failed write the Plan no longer looked stale and the retry went through
// normal recovery, which left the Plan Running (or Failed without FRExceedRecovery) and resumed execution. Objects are
// now written after their descendants and end at the time the Plan was last known alive, so a retry ages it out again.
func TestAgeOutRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		plan     func() *workflow.Plan
		failType workflow.ObjectType
		// resume retries through Plans.Resume instead of the way startup recovery does.
		resume bool
	}{
		{name: "Success: aging out converges after the action write fails", plan: stalePlan, failType: workflow.OTAction},
		{name: "Success: aging out converges after the checks write fails", plan: stalePlan, failType: workflow.OTCheck},
		{name: "Success: aging out converges after the plan write fails", plan: stalePlan, failType: workflow.OTPlan},
		{name: "Success: aging out a block converges after the sequence write fails", plan: staleBlockPlan, failType: workflow.OTSequence},
		{name: "Success: aging out a block converges after the block write fails", plan: staleBlockPlan, failType: workflow.OTBlock},
		{name: "Success: Resume ages out a block after the sequence write fails", plan: staleBlockPlan, failType: workflow.OTSequence, resume: true},
	}

	for _, test := range tests {
		plan := test.plan()
		retry := recoverStored
		if test.resume {
			retry = resumeStored
		}

		reference := durable.New(t.Context(), plan, workflow.OTUnknown)
		if err := retry(t, reference, plan.ID); err != nil {
			t.Errorf("TestAgeOutRetry(%s): reference ageOut: got err == %s, want err == nil", test.name, err)
			continue
		}

		store := durable.New(t.Context(), plan, test.failType)
		if err := retry(t, store, plan.ID); err == nil {
			t.Errorf("TestAgeOutRetry(%s): first ageOut: got err == nil, want the injected write failure", test.name)
			continue
		}
		if err := retry(t, store, plan.ID); err != nil {
			t.Errorf("TestAgeOutRetry(%s): retried ageOut: got err == %s, want err == nil", test.name, err)
			continue
		}

		if diff := pretty.Compare(reference.Statuses(), store.Statuses()); diff != "" {
			t.Errorf("TestAgeOutRetry(%s): stored statuses after the retry -want +got:\n%s", test.name, diff)
		}
		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestAgeOutRetry(%s): Read: %s", test.name, err)
		}
		if stored.Reason != workflow.FRExceedRecovery {
			t.Errorf("TestAgeOutRetry(%s): got stored reason %v, want %v", test.name, stored.Reason, workflow.FRExceedRecovery)
		}
	}
}
