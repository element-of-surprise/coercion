package sm

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/durable"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

type storedParent interface {
	workflow.Object
	GetID() uuid.UUID
}

// settlementStore checks durable children at each terminal parent write, not just after End rewrites everything.
type settlementStore struct {
	*durable.Store
	t    *testing.T
	plan *workflow.Plan
}

func (s *settlementStore) checkChildren(ctx context.Context, parent storedParent) {
	s.t.Helper()
	if !isCompleted(parent) {
		return
	}
	// This runs on pool goroutines, where t.Fatal must not be called.
	stored, err := s.Read(ctx, s.plan.ID)
	if err != nil {
		s.t.Errorf("settlementStore.checkChildren: Read: %s", err)
		return
	}
	for item := range walk.RunningObjects(stored) {
		for _, ancestor := range item.Chain {
			if ancestor.(storedParent).GetID() == parent.GetID() {
				s.t.Errorf("settlementStore.checkChildren: writing terminal %s while stored descendant %s is still Running", parent.Type(), item.Value.Type())
			}
		}
	}
}

func (s *settlementStore) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	s.checkChildren(ctx, seq)
	return s.Store.UpdateSequence(ctx, seq)
}

func (s *settlementStore) UpdateBlock(ctx context.Context, b *workflow.Block) error {
	s.checkChildren(ctx, b)
	return s.Store.UpdateBlock(ctx, b)
}

func (s *settlementStore) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	s.checkChildren(ctx, b)
	return s.Store.UpdateDeferBatch(ctx, b)
}

func (s *settlementStore) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	s.checkChildren(ctx, da)
	return s.Store.UpdateDeferredActions(ctx, da)
}

func (s *settlementStore) UpdatePlan(ctx context.Context, p *workflow.Plan) error {
	s.checkChildren(ctx, p)
	return s.Store.UpdatePlan(ctx, p)
}

func (s *settlementStore) UpdateChanges(ctx context.Context, p *workflow.Plan, before changes.Snapshot) error {
	return storage.WriteChanges(ctx, s, p, before)
}

func settlementAction(name string, status workflow.Status, at time.Time) *workflow.Action {
	a := &workflow.Action{ID: workflow.NewV7(), Name: name}
	state := workflow.State{Status: status}
	if status != workflow.NotStarted {
		state.Start = at
	}
	if status == workflow.Completed || status == workflow.Failed {
		state.End = at
	}
	a.State.Set(state)
	return a
}

// TestRecoveryPartialDeferredBatch checks that a deferred batch interrupted part way resumes with only its remaining
// action, after the first recovery attempt fails on a write and is retried as a restart would.
func TestRecoveryPartialDeferredBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// failType is the object type whose first write fails, failing the first recovery attempt.
		failType workflow.ObjectType
		wantErr  bool
	}{
		{
			name:     "Success: recovery with no failed write resumes the remaining action of the batch",
			failType: workflow.OTUnknown,
		},
		{
			name:     "Error: recovery whose action write fails is retried and resumes the remaining action of the batch",
			failType: workflow.OTAction,
			wantErr:  true,
		},
		{
			name:     "Error: recovery whose DeferredActions write fails is retried and resumes the remaining action of the batch",
			failType: workflow.OTDeferredActions,
			wantErr:  true,
		},
		{
			name:     "Error: recovery whose Plan write fails is retried and resumes the remaining action of the batch",
			failType: workflow.OTPlan,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		start := time.Now().Add(-time.Minute)
		plan := failedChecksPlan()
		first := settlementAction("completed", workflow.Completed, start)
		next := settlementAction("remaining", workflow.Running, start)
		batch := recoveryBatch(workflow.OnFailure, false, workflow.Running, first, next)
		plan.DeferredActions = recoveryDA(workflow.Running, batch)
		plan.DeferredChecks = recoveryChecks(workflow.Running, settlementAction("deferred check", workflow.Running, start))

		store := &settlementStore{Store: durable.New(t.Context(), plan, test.failType), t: t, plan: plan}
		var mu sync.Mutex
		ran := map[string]int{}
		s := &States{
			store: store,
			testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
				mu.Lock()
				ran[a.Name]++
				mu.Unlock()
				a.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: time.Now()})
				return updater.UpdateAction(ctx, a)
			},
		}

		recoverStored := func() statemachine.Request[Data] {
			stored, err := store.Read(t.Context(), plan.ID)
			if err != nil {
				t.Fatalf("TestRecoveryPartialDeferredBatch(%s): Read: %s", test.name, err)
			}
			return s.Recovery(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: stored}})
		}

		req := recoverStored()
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): first recovery: got err == nil, want err != nil", test.name)
			continue
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): first recovery: got err == %s, want err == nil", test.name, req.Err)
			continue
		case req.Err != nil:
			// A failed write ends the run; the restart recovers again from what is stored.
			req = recoverStored()
			if req.Err != nil {
				t.Errorf("TestRecoveryPartialDeferredBatch(%s): retried recovery: got err == %s, want err == nil", test.name, req.Err)
				continue
			}
		}
		if req.Data.stopHeartbeat != nil {
			t.Cleanup(req.Data.stopHeartbeat)
		}

		durablePlan, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRecoveryPartialDeferredBatch(%s): Read: %s", test.name, err)
		}
		if got := durablePlan.GetState().Status; got != workflow.Running {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): got stored Plan status %v after recovery, want Running until its deferred work finishes", test.name, got)
		}
		if _, err := statemachine.Run("TestRecoveryPartialDeferredBatch", req); err == nil {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): got run err == nil, want the Plan's PreChecks failure", test.name)
		}

		mu.Lock()
		if diff := pretty.Compare(map[string]int{"remaining": 1, "deferred check": 1}, ran); diff != "" {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): action runs -want +got:\n%s", test.name, diff)
		}
		mu.Unlock()
		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRecoveryPartialDeferredBatch(%s): Read: %s", test.name, err)
		}
		if stored.GetState().Status != workflow.Failed || stored.Reason != workflow.FRPreCheck {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): got Plan %v / %v, want Failed / FRPreCheck", test.name, stored.GetState().Status, stored.Reason)
		}
		if got := stored.DeferredActions.DeferredBatches[0].GetState().Status; got != workflow.Completed {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): got deferred batch status %v, want Completed", test.name, got)
		}
		if got := stored.DeferredActions.DeferredBatches[0].Actions[0].GetState(); got != first.GetState() {
			t.Errorf("TestRecoveryPartialDeferredBatch(%s): completed action was modified: got %v, want %v", test.name, got, first.GetState())
		}
	}
}

// TestRejectedRecoveredSequences checks that a recovered Running sequence that something keeps from resuming is
// settled Failed with an end time, leaving its completed and never-started actions alone, and that one nothing
// rejects resumes.
func TestRejectedRecoveredSequences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// priorFailed puts a Failed sequence before the recovered one, exceeding the tolerated failures.
		priorFailed bool
		// planContErr and blockContErr leave a failure waiting from the Plan's or the block's ContChecks.
		planContErr  bool
		blockContErr bool
		// preChecks and contChecks, when set, give the block PreChecks or ContChecks with one action of that name;
		// "error" fails them.
		preChecks  string
		contChecks string
		wantRan    map[string]int
		// wantStatuses are the recovered sequence's, its completed, interrupted and unstarted actions' and the block's.
		wantStatuses []workflow.Status
		wantErr      bool
	}{
		{
			name:         "Success: with nothing rejecting it, the interrupted action runs once and the sequence and block complete",
			wantRan:      map[string]int{"interrupted": 1, "unstarted": 1},
			wantStatuses: []workflow.Status{workflow.Completed, workflow.Completed, workflow.Completed, workflow.Completed, workflow.Completed},
		},
		{
			name:         "Error: exceeded tolerated failures settle the recovered sequence Failed",
			priorFailed:  true,
			wantRan:      map[string]int{},
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Failed, workflow.NotStarted, workflow.Failed},
			wantErr:      true,
		},
		{
			name:         "Error: failed Plan ContChecks settle the recovered sequence Failed",
			planContErr:  true,
			wantRan:      map[string]int{},
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Failed, workflow.NotStarted, workflow.Failed},
			wantErr:      true,
		},
		{
			name:         "Error: failed block ContChecks settle the recovered sequence Failed",
			blockContErr: true,
			wantRan:      map[string]int{},
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Failed, workflow.NotStarted, workflow.Failed},
			wantErr:      true,
		},
		{
			name:         "Error: failed block PreChecks settle the recovered sequence Failed",
			preChecks:    "error",
			wantRan:      map[string]int{},
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Failed, workflow.NotStarted, workflow.Failed},
			wantErr:      true,
		},
		{
			name:         "Error: block ContChecks that fail their first run with the PreChecks settle the recovered sequence Failed",
			preChecks:    "pass",
			contChecks:   "error",
			wantRan:      map[string]int{},
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Failed, workflow.NotStarted, workflow.Failed},
			wantErr:      true,
		},
	}

	checks := func(name string) *workflow.Checks {
		if name == "" {
			return nil
		}
		c := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{{ID: workflow.NewV7(), Name: name}}}
		c.State.Set(workflow.State{})
		c.Actions[0].State.Set(workflow.State{})
		return c
	}

	for _, test := range tests {
		start := time.Now().Add(-time.Minute)
		done := settlementAction("completed", workflow.Completed, start)
		running := settlementAction("interrupted", workflow.Running, start)
		unstarted := settlementAction("unstarted", workflow.NotStarted, start)
		doneState := done.GetState()
		seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{done, running, unstarted}}
		seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
		b := &workflow.Block{ID: workflow.NewV7(), Concurrency: 1, Sequences: []*workflow.Sequence{seq}, PreChecks: checks(test.preChecks), ContChecks: checks(test.contChecks)}
		b.State.Set(workflow.State{Status: workflow.Running, Start: start})
		if test.priorFailed {
			failed := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{settlementAction("failed", workflow.Failed, start)}}
			failed.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: start})
			b.Sequences = append([]*workflow.Sequence{failed}, b.Sequences...)
		}
		plan := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{b}}
		plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
		data := Data{
			Plan: plan, recovered: true, contCheckResult: make(chan error, 1),
			blocks: []block{{block: b, contCheckResult: make(chan error, 1)}},
		}
		if test.planContErr {
			data.contCheckResult <- errors.New("Plan checks failed")
		}
		if test.blockContErr {
			data.blocks[0].contCheckResult <- errors.New("Block checks failed")
		}

		store := &settlementStore{Store: durable.New(t.Context(), plan, workflow.OTUnknown), t: t, plan: plan}
		var mu sync.Mutex
		ran := map[string]int{}
		s := &States{
			store: store,
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				if checks.Actions[0].Name == "error" {
					checks.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: start})
					return errors.New("checks failed")
				}
				checks.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start})
				return nil
			},
			testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
				// runAction does not run an action that already completed.
				if a.State.Get().Status == workflow.Completed {
					return nil
				}
				mu.Lock()
				ran[a.Name]++
				mu.Unlock()
				a.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: time.Now()})
				return updater.UpdateAction(ctx, a)
			},
		}
		next := s.ExecuteSequences
		if b.PreChecks != nil {
			next = s.BlockPreChecks
		}

		_, err := statemachine.Run("TestRejectedRecoveredSequences", statemachine.Request[Data]{Ctx: t.Context(), Data: data, Next: next})
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRejectedRecoveredSequences(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestRejectedRecoveredSequences(%s): got err == %s, want err == nil", test.name, err)
		}

		mu.Lock()
		if diff := pretty.Compare(test.wantRan, ran); diff != "" {
			t.Errorf("TestRejectedRecoveredSequences(%s): action runs -want +got:\n%s", test.name, diff)
		}
		mu.Unlock()
		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRejectedRecoveredSequences(%s): Read: %s", test.name, err)
		}
		gotBlock := stored.Blocks[0]
		gotSeq := gotBlock.Sequences[len(gotBlock.Sequences)-1]
		var statuses []workflow.Status
		for _, obj := range []walk.Stateful{gotSeq, gotSeq.Actions[0], gotSeq.Actions[1], gotSeq.Actions[2], gotBlock} {
			statuses = append(statuses, obj.GetState().Status)
		}
		if diff := pretty.Compare(test.wantStatuses, statuses); diff != "" {
			t.Errorf("TestRejectedRecoveredSequences(%s): statuses of sequence, actions and block -want +got:\n%s", test.name, diff)
		}
		for _, obj := range []walk.Stateful{gotSeq, gotSeq.Actions[1]} {
			if obj.GetState().End.IsZero() {
				t.Errorf("TestRejectedRecoveredSequences(%s): got %v with no end time, want one", test.name, obj.GetState())
			}
		}
		if got := gotSeq.Actions[0].GetState(); got != doneState {
			t.Errorf("TestRejectedRecoveredSequences(%s): completed action state changed to %v, want %v", test.name, got, doneState)
		}
	}
}

// TestFailSequencesRetry checks that failSequences settles a block's Running sequences and actions Failed, keeping an
// action's own end time and leaving the continuous checks alone, and that a retry after a failed write finishes it.
func TestFailSequencesRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// failType is the object type whose first write fails.
		failType workflow.ObjectType
		wantErr  bool
	}{
		{
			name:     "Success: with no failed write the sequences are settled at once",
			failType: workflow.OTUnknown,
		},
		{
			name:     "Error: a failed action write leaves the block Running and a retry settles the sequences",
			failType: workflow.OTAction,
			wantErr:  true,
		},
		{
			name:     "Error: a failed sequence write leaves the block Running and a retry settles the sequences",
			failType: workflow.OTSequence,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		start := time.Now().Add(-time.Minute)
		oldEnd := start.Add(time.Second)
		action := settlementAction("interrupted", workflow.Running, start)
		action.SetState(workflow.State{Status: workflow.Running, Start: start, End: oldEnd})
		seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{action}}
		seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
		contAction := settlementAction("continuous check", workflow.Running, start)
		cont := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{contAction}}
		cont.State.Set(workflow.State{Status: workflow.Running, Start: start})
		b := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}, ContChecks: cont}
		b.State.Set(workflow.State{Status: workflow.Running, Start: start})
		plan := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{b}}
		plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
		store := &settlementStore{Store: durable.New(t.Context(), plan, test.failType), t: t, plan: plan}
		s := &States{store: store}

		err := s.failSequences(t.Context(), b)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestFailSequencesRetry(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestFailSequencesRetry(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			stored, err := store.Read(t.Context(), plan.ID)
			if err != nil {
				t.Fatalf("TestFailSequencesRetry(%s): Read: %s", test.name, err)
			}
			if got := stored.Blocks[0].GetState().Status; got != workflow.Running {
				t.Errorf("TestFailSequencesRetry(%s): got stored block status %v after the failed write, want Running", test.name, got)
			}
			// A production write failure exits the run; the retry reads a new copy, not the modified failed write.
			if err := s.failSequences(t.Context(), stored.Blocks[0]); err != nil {
				t.Errorf("TestFailSequencesRetry(%s): retry: got err == %s, want err == nil", test.name, err)
				continue
			}
		}

		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestFailSequencesRetry(%s): Read: %s", test.name, err)
		}
		got := stored.Blocks[0]
		if state := got.Sequences[0].GetState(); state.Status != workflow.Failed || state.End.IsZero() {
			t.Errorf("TestFailSequencesRetry(%s): got sequence state %v, want Failed with an end time", test.name, state)
		}
		if state := got.Sequences[0].Actions[0].GetState(); state.Status != workflow.Failed || !state.End.Equal(oldEnd) {
			t.Errorf("TestFailSequencesRetry(%s): got action state %v, want Failed with its original end time", test.name, state)
		}
		if got.ContChecks.GetState().Status != workflow.Running || got.ContChecks.Actions[0].GetState().Status != workflow.Running {
			t.Errorf("TestFailSequencesRetry(%s): sequence settlement changed the continuous checks", test.name)
		}
	}
}

// TestRecoveryRejectedSequence checks that a recovered Running sequence is settled Failed when the block's earlier
// failures already exceed what it tolerates, and resumes when they do not.
func TestRecoveryRejectedSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tolerated  int
		wantRan    map[string]int
		wantSeq    workflow.Status
		wantBlock  workflow.Status
		wantStatus workflow.Status
		wantErr    bool
	}{
		{
			name:       "Success: a failure the block tolerates lets the recovered sequence resume and complete",
			tolerated:  1,
			wantRan:    map[string]int{"interrupted": 1},
			wantSeq:    workflow.Completed,
			wantBlock:  workflow.Completed,
			wantStatus: workflow.Completed,
		},
		{
			name:       "Error: a failure beyond what the block tolerates settles the recovered sequence Failed",
			tolerated:  0,
			wantRan:    map[string]int{},
			wantSeq:    workflow.Failed,
			wantBlock:  workflow.Failed,
			wantStatus: workflow.Failed,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		start := time.Now().Add(-time.Minute)
		done := settlementAction("completed", workflow.Completed, start)
		interrupted := settlementAction("interrupted", workflow.Running, start)
		seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{done, interrupted}}
		seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
		failed := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{settlementAction("failed", workflow.Failed, start)}}
		failed.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: start})
		b := &workflow.Block{ID: workflow.NewV7(), Concurrency: 2, ToleratedFailures: test.tolerated, Sequences: []*workflow.Sequence{failed, seq}}
		b.State.Set(workflow.State{Status: workflow.Running, Start: start})

		got := runRecovered(t, runningPlan(b))
		switch {
		case got.err == nil && test.wantErr:
			t.Errorf("TestRecoveryRejectedSequence(%s): got err == nil, want err != nil", test.name)
		case got.err != nil && !test.wantErr:
			t.Errorf("TestRecoveryRejectedSequence(%s): got err == %s, want err == nil", test.name, got.err)
		}

		if diff := pretty.Compare(test.wantRan, got.ran); diff != "" {
			t.Errorf("TestRecoveryRejectedSequence(%s): action runs -want +got:\n%s", test.name, diff)
		}
		if state := got.stored.Blocks[0].Sequences[1].GetState(); state.Status != test.wantSeq || state.End.IsZero() {
			t.Errorf("TestRecoveryRejectedSequence(%s): got recovered sequence state %v, want %v with an end time", test.name, state, test.wantSeq)
		}
		if state := got.stored.Blocks[0].GetState(); state.Status != test.wantBlock {
			t.Errorf("TestRecoveryRejectedSequence(%s): got block status %v, want %v", test.name, state.Status, test.wantBlock)
		}
		if state := got.stored.GetState(); state.Status != test.wantStatus {
			t.Errorf("TestRecoveryRejectedSequence(%s): got Plan status %v, want %v", test.name, state.Status, test.wantStatus)
		}
	}
}
