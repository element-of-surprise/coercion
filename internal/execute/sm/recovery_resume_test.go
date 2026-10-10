package sm

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/durable"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// resumed is what a recovered run did: how many times each action ran by name, what it left stored and its error.
type resumed struct {
	ran    map[string]int
	stored *workflow.Plan
	err    error
}

// runRecovered stores plan and runs the executor from Recovery on the stored copy, as a restart does. Every action
// completes, except those named in fail, which fail. The store checks that no parent is written as finished while a
// stored descendant is still Running.
func runRecovered(t *testing.T, plan *workflow.Plan, fail ...string) resumed {
	t.Helper()
	return runRecoveredRetried(t, plan, workflow.OTUnknown, fail...)
}

// runRecoveredRetried is runRecovered, but the first write of an object of type failType fails. The first Recovery
// ends on that failure, as a run does, and the executor then runs from Recovery again on what it stored, as a restart
// does. With workflow.OTUnknown no write fails and Recovery runs once.
func runRecoveredRetried(t *testing.T, plan *workflow.Plan, failType workflow.ObjectType, fail ...string) resumed {
	t.Helper()

	store := &settlementStore{Store: durable.New(t.Context(), plan, failType), t: t, plan: plan}
	var mu sync.Mutex
	ran := map[string]int{}
	s := &States{
		store: store,
		testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
			// runAction does not run an action that already completed.
			if a.State.Get().Status == workflow.Completed {
				return nil
			}
			mu.Lock()
			ran[a.Name]++
			mu.Unlock()

			state := workflow.State{Status: workflow.Completed, Start: time.Now(), End: time.Now()}
			// Each run finishes an attempt, as the action runner does.
			attempt := workflow.Attempt{Start: state.Start, End: state.End}
			var err error
			if slices.Contains(fail, a.Name) {
				state.Status = workflow.Failed
				attempt.Err = &plugins.Error{Message: "action failed"}
				err = errors.New("action failed")
			}
			a.Attempts.Append(attempt)
			a.State.Set(state)
			if uErr := updater.UpdateAction(ctx, a); uErr != nil {
				return uErr
			}
			return err
		},
	}

	if failType != workflow.OTUnknown {
		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("runRecovered: Read: %s", err)
		}
		if req := s.Recovery(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: stored}}); req.Err == nil {
			t.Fatalf("runRecovered: first Recovery: got err == nil, want the injected %v write failure", failType)
		}
	}

	stored, err := store.Read(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("runRecovered: Read: %s", err)
	}
	_, runErr := statemachine.Run("runRecovered", statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: stored}, Next: s.Recovery})
	final, err := store.Read(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("runRecovered: Read: %s", err)
	}
	// A finished Plan is never run again, so anything it stores as Running would stay that way for good.
	if status := final.State.Get().Status; status != workflow.Running {
		for item := range walk.RunningObjects(final) {
			t.Errorf("runRecovered: the %v Plan stores %v(%s) as Running", status, item.Value.Type(), item.Value.(interface{ GetID() uuid.UUID }).GetID())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	return resumed{ran: ran, stored: final, err: runErr}
}

// retryingAction returns a Running action named name whose last attempt failed with a retryable error and that has
// retries left, as an action is when the crash came between its attempts.
func retryingAction(name string, at time.Time) *workflow.Action {
	a := settlementAction(name, workflow.Running, at)
	a.Retries = 3
	a.Attempts.Set([]workflow.Attempt{{Start: at, End: at, Err: &plugins.Error{Message: "retryable"}}})
	return a
}

// recoveryChecks returns Checks with an ID, the given status and actions.
func recoveryChecks(status workflow.Status, actions ...*workflow.Action) *workflow.Checks {
	c := &workflow.Checks{ID: workflow.NewV7(), Actions: actions}
	state := workflow.State{Status: status}
	if status != workflow.NotStarted {
		state.Start = time.Now().Add(-time.Minute)
	}
	if status == workflow.Completed || status == workflow.Failed {
		state.End = time.Now().Add(-time.Minute)
	}
	c.State.Set(state)
	return c
}

// recoveryBatch returns a DeferBatch with an ID, the given status and actions.
func recoveryBatch(when workflow.WhenDeferred, failElement bool, status workflow.Status, actions ...*workflow.Action) *workflow.DeferBatch {
	b := &workflow.DeferBatch{Sequence: workflow.Sequence{ID: workflow.NewV7(), Actions: actions}, When: when, FailElement: failElement}
	state := workflow.State{Status: status}
	if status != workflow.NotStarted {
		state.Start = time.Now().Add(-time.Minute)
	}
	if status == workflow.Completed || status == workflow.Failed {
		state.End = time.Now().Add(-time.Minute)
	}
	b.State.Set(state)
	return b
}

// recoveryDA returns a DeferredActions with an ID, the given status and batches.
func recoveryDA(status workflow.Status, batches ...*workflow.DeferBatch) *workflow.DeferredActions {
	da := &workflow.DeferredActions{ID: workflow.NewV7(), DeferredBatches: batches}
	state := workflow.State{Status: status}
	if status != workflow.NotStarted {
		state.Start = time.Now().Add(-time.Minute)
	}
	da.State.Set(state)
	return da
}

// completedBlock returns a Completed block with one Completed sequence.
func completedBlock() *workflow.Block {
	start := time.Now().Add(-time.Minute)
	seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{settlementAction("block action", workflow.Completed, start)}}
	seq.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start})
	b := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}}
	b.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start})
	return b
}

// runningPlan returns a Running Plan with blocks.
func runningPlan(blocks ...*workflow.Block) *workflow.Plan {
	p := &workflow.Plan{ID: workflow.NewV7(), Blocks: blocks}
	p.State.Set(workflow.State{Status: workflow.Running, Start: time.Now().Add(-time.Minute)})
	return p
}

// TestRecoveryBlocksDone is a regression test. A Running Plan whose blocks had all completed, but whose deferred work
// had not, was recovered through PlanBypassChecks and PlanStartContChecks, which started a new ContChecks loop after
// its blocks were done. A failing pass then picked the OnFailure batches, abandoned a half done OnSuccess batch and
// failed a Plan that had succeeded. Once every block is done, no new ContChecks loop may start: recovery must go on to
// the PostChecks and deferred work, judged on what is stored. A pass the crash cut short is run again once, to
// completion, where a live run drains it (before the PostChecks), and its result counts as the live one would.
func TestRecoveryBlocksDone(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-time.Minute)

	// cutPass returns a Plan whose blocks are done and whose ContChecks pass was cut short by the crash, before the
	// PostChecks and deferred work started.
	cutPass := func() *workflow.Plan {
		p := runningPlan(completedBlock())
		p.ContChecks = recoveryChecks(workflow.Running, settlementAction("cont check", workflow.Running, start))
		p.PostChecks = recoveryChecks(workflow.NotStarted, settlementAction("post check", workflow.NotStarted, start))
		p.DeferredActions = recoveryDA(
			workflow.NotStarted,
			recoveryBatch(workflow.OnSuccess, false, workflow.NotStarted, settlementAction("success", workflow.NotStarted, start)),
			recoveryBatch(workflow.OnFailure, false, workflow.NotStarted, settlementAction("failure", workflow.NotStarted, start)),
		)
		return p
	}

	// partial returns a Running batch whose first action completed and whose second was interrupted.
	partial := func(when workflow.WhenDeferred) *workflow.DeferBatch {
		return recoveryBatch(when, false, workflow.Running, settlementAction("completed", workflow.Completed, start), settlementAction("interrupted", workflow.Running, start))
	}
	// failedBatch returns a FailElement batch that failed before the crash.
	failedBatch := func(when workflow.WhenDeferred) *workflow.DeferBatch {
		return recoveryBatch(when, true, workflow.Failed, settlementAction("failed", workflow.Failed, start))
	}
	// doneBatch returns a FailElement batch that completed before the crash.
	doneBatch := func(when workflow.WhenDeferred) *workflow.DeferBatch {
		return recoveryBatch(when, true, workflow.Completed, settlementAction("done", workflow.Completed, start))
	}

	tests := []struct {
		name string
		plan func() *workflow.Plan
		// fail names the actions that fail when they run. The ContChecks' action is "cont check".
		fail []string
		// wantRan is how many times each action ran, by name.
		wantRan     map[string]int
		wantBatches []workflow.Status
		// wantDA is the DeferredActions status, checked when the Plan has DeferredActions.
		wantDA     workflow.Status
		wantCont   workflow.Status
		wantPost   workflow.Status
		wantStatus workflow.Status
		wantReason workflow.FailureReason
		wantErr    bool
	}{
		{
			name: "Success: a Plan with completed ContChecks finishes its OnSuccess batch without a new ContChecks pass",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.ContChecks = recoveryChecks(workflow.Completed, settlementAction("cont check", workflow.Completed, start))
				p.PostChecks = recoveryChecks(workflow.Completed, settlementAction("post check", workflow.Completed, start))
				p.DeferredActions = recoveryDA(
					workflow.Running,
					recoveryBatch(workflow.OnSuccess, false, workflow.Running, settlementAction("success done", workflow.Completed, start), settlementAction("success next", workflow.NotStarted, start)),
					recoveryBatch(workflow.OnFailure, false, workflow.NotStarted, settlementAction("failure", workflow.NotStarted, start)),
				)
				return p
			},
			fail:        []string{"cont check"},
			wantRan:     map[string]int{"success next": 1},
			wantBatches: []workflow.Status{workflow.Completed, workflow.NotStarted},
			wantDA:      workflow.Completed,
			wantCont:    workflow.Completed,
			wantPost:    workflow.Completed,
			wantStatus:  workflow.Completed,
		},
		{
			name:        "Success: a ContChecks pass cut short by the crash runs again once, passes, and the PostChecks and OnSuccess batch run",
			plan:        cutPass,
			wantRan:     map[string]int{"cont check": 1, "post check": 1, "success": 1},
			wantBatches: []workflow.Status{workflow.Completed, workflow.NotStarted},
			wantDA:      workflow.Completed,
			wantCont:    workflow.Completed,
			wantPost:    workflow.Completed,
			wantStatus:  workflow.Completed,
		},
		{
			name:        "Error: a ContChecks pass cut short by the crash runs again once and fails, so the Plan fails and runs its OnFailure batch",
			plan:        cutPass,
			fail:        []string{"cont check"},
			wantRan:     map[string]int{"cont check": 1, "failure": 1},
			wantBatches: []workflow.Status{workflow.NotStarted, workflow.Completed},
			wantDA:      workflow.Completed,
			wantCont:    workflow.Failed,
			wantPost:    workflow.NotStarted,
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRContCheck,
			wantErr:     true,
		},
		{
			name: "Success: a ContChecks pass cut short by the crash on a Plan with no PostChecks or deferred work runs again once before the Plan completes",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.ContChecks = recoveryChecks(workflow.Running, settlementAction("cont check", workflow.Running, start))
				return p
			},
			wantRan:    map[string]int{"cont check": 1},
			wantCont:   workflow.Completed,
			wantStatus: workflow.Completed,
		},
		{
			name: "Error: a ContChecks pass cut short by the crash on a Plan with no PostChecks or deferred work runs again once and fails the Plan",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.ContChecks = recoveryChecks(workflow.Running, settlementAction("cont check", workflow.Running, start))
				return p
			},
			fail:       []string{"cont check"},
			wantRan:    map[string]int{"cont check": 1},
			wantCont:   workflow.Failed,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRContCheck,
			wantErr:    true,
		},
		{
			name: "Error: a ContChecks pass that failed before the crash fails the Plan and runs its OnFailure batch",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.ContChecks = recoveryChecks(workflow.Running, settlementAction("cont check", workflow.Failed, start))
				p.PostChecks = recoveryChecks(workflow.Completed, settlementAction("post check", workflow.Completed, start))
				p.DeferredActions = recoveryDA(
					workflow.NotStarted,
					recoveryBatch(workflow.OnSuccess, false, workflow.NotStarted, settlementAction("success", workflow.NotStarted, start)),
					recoveryBatch(workflow.OnFailure, false, workflow.NotStarted, settlementAction("failure", workflow.NotStarted, start)),
				)
				return p
			},
			wantRan:     map[string]int{"failure": 1},
			wantBatches: []workflow.Status{workflow.NotStarted, workflow.Completed},
			wantDA:      workflow.Completed,
			wantCont:    workflow.Failed,
			wantPost:    workflow.Completed,
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRContCheck,
			wantErr:     true,
		},
		{
			// Regression: the failed sibling failed the DeferredChecks, which are not run again, but the action between its
			// retries was left Running, so the finished Plan stored it Running.
			name: "Error: Plan DeferredChecks with a failed action settle an action between its retries Failed and fail the Plan",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.ContChecks = recoveryChecks(workflow.Completed, settlementAction("cont check", workflow.Completed, start))
				p.PostChecks = recoveryChecks(workflow.Completed, settlementAction("post check", workflow.Completed, start))
				p.DeferredChecks = recoveryChecks(workflow.Running, settlementAction("deferred failed", workflow.Failed, start), retryingAction("deferred retrying", start))
				return p
			},
			wantCont:   workflow.Completed,
			wantPost:   workflow.Completed,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredCheck,
			wantErr:    true,
		},
		// Regression for the rows below: recovery marked the DeferredActions Failed as soon as one FailElement batch
		// had failed, while another batch was still part way through. Failed DeferredActions are not resumed, so the
		// unfinished batch was settled Failed and its remaining actions never ran, though a live run attempts every
		// batch regardless of the others' failures. DeferredActions with a batch left to run must resume it.
		{
			name: "Success: a completed Plan resumes its interrupted batch beside a completed one and completes",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.DeferredActions = recoveryDA(workflow.Running, doneBatch(workflow.OnSuccess), partial(workflow.OnSuccess))
				p.DeferredChecks = recoveryChecks(workflow.NotStarted, settlementAction("deferred check", workflow.NotStarted, start))
				return p
			},
			wantRan:     map[string]int{"interrupted": 1, "deferred check": 1},
			wantBatches: []workflow.Status{workflow.Completed, workflow.Completed},
			wantDA:      workflow.Completed,
			wantStatus:  workflow.Completed,
		},
		{
			name: "Success: a completed Plan whose batches had all finished completes",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.DeferredActions = recoveryDA(workflow.Running, doneBatch(workflow.OnSuccess), doneBatch(workflow.OnSuccess))
				p.DeferredChecks = recoveryChecks(workflow.NotStarted, settlementAction("deferred check", workflow.NotStarted, start))
				return p
			},
			wantRan:     map[string]int{"deferred check": 1},
			wantBatches: []workflow.Status{workflow.Completed, workflow.Completed},
			wantDA:      workflow.Completed,
			wantStatus:  workflow.Completed,
		},
		{
			name: "Error: a failed FailElement batch does not stop an interrupted batch from finishing",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.DeferredActions = recoveryDA(workflow.Running, failedBatch(workflow.OnSuccess), partial(workflow.OnSuccess))
				p.DeferredChecks = recoveryChecks(workflow.NotStarted, settlementAction("deferred check", workflow.NotStarted, start))
				return p
			},
			wantRan:     map[string]int{"interrupted": 1, "deferred check": 1},
			wantBatches: []workflow.Status{workflow.Failed, workflow.Completed},
			wantDA:      workflow.Failed,
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRDeferredAction,
			wantErr:     true,
		},
		{
			name: "Error: a failed FailElement batch with every other batch finished fails the DeferredActions",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.DeferredActions = recoveryDA(workflow.Running, failedBatch(workflow.OnSuccess), doneBatch(workflow.OnSuccess))
				p.DeferredChecks = recoveryChecks(workflow.NotStarted, settlementAction("deferred check", workflow.NotStarted, start))
				return p
			},
			wantRan:     map[string]int{"deferred check": 1},
			wantBatches: []workflow.Status{workflow.Failed, workflow.Completed},
			wantDA:      workflow.Failed,
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRDeferredAction,
			wantErr:     true,
		},
	}

	for _, test := range tests {
		got := runRecovered(t, test.plan(), test.fail...)
		switch {
		case got.err == nil && test.wantErr:
			t.Errorf("TestRecoveryBlocksDone(%s): got err == nil, want err != nil", test.name)
		case got.err != nil && !test.wantErr:
			t.Errorf("TestRecoveryBlocksDone(%s): got err == %s, want err == nil", test.name, got.err)
		}

		if diff := pretty.Compare(test.wantRan, got.ran); diff != "" {
			t.Errorf("TestRecoveryBlocksDone(%s): action runs -want +got:\n%s", test.name, diff)
		}
		var batches []workflow.Status
		if got.stored.DeferredActions != nil {
			for _, b := range got.stored.DeferredActions.DeferredBatches {
				batches = append(batches, b.GetState().Status)
			}
		}
		if diff := pretty.Compare(test.wantBatches, batches); diff != "" {
			t.Errorf("TestRecoveryBlocksDone(%s): deferred batch statuses -want +got:\n%s", test.name, diff)
		}
		if got.stored.DeferredActions != nil {
			if got := got.stored.DeferredActions.GetState().Status; got != test.wantDA {
				t.Errorf("TestRecoveryBlocksDone(%s): got DeferredActions status %v, want %v", test.name, got, test.wantDA)
			}
		}
		if got.stored.ContChecks != nil {
			if got := got.stored.ContChecks.GetState().Status; got != test.wantCont {
				t.Errorf("TestRecoveryBlocksDone(%s): got ContChecks status %v, want %v", test.name, got, test.wantCont)
			}
		}
		if got.stored.PostChecks != nil {
			if got := got.stored.PostChecks.GetState().Status; got != test.wantPost {
				t.Errorf("TestRecoveryBlocksDone(%s): got PostChecks status %v, want %v", test.name, got, test.wantPost)
			}
		}
		if got := got.stored.GetState().Status; got != test.wantStatus {
			t.Errorf("TestRecoveryBlocksDone(%s): got Plan status %v, want %v", test.name, got, test.wantStatus)
		}
		if got.stored.Reason != test.wantReason {
			t.Errorf("TestRecoveryBlocksDone(%s): got Plan reason %v, want %v", test.name, got.stored.Reason, test.wantReason)
		}
	}
}

// TestRecoveryFailedBlockDeferredChecks is a regression test. A block whose checks failed is written Failed before its
// DeferredChecks run. After a crash while they were Running or not yet started, recovery skipped the Failed block, the
// Plan failed, and the DeferredChecks were settled Failed or left NotStarted, never run, though a live run always runs
// a failed block's DeferredChecks. Recovery must run them.
func TestRecoveryFailedBlockDeferredChecks(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-time.Minute)

	// block returns a block with one sequence in seqStatus, the block in status, failed PreChecks when preFailed and
	// DeferredChecks in deferredStatus whose action is "block deferred check".
	block := func(status, seqStatus workflow.Status, preFailed bool, deferredStatus workflow.Status) *workflow.Block {
		seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{settlementAction("block action", seqStatus, start)}}
		seqState := workflow.State{Status: seqStatus}
		if seqStatus == workflow.Completed {
			seqState = workflow.State{Status: seqStatus, Start: start, End: start}
		}
		seq.State.Set(seqState)
		b := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}}
		b.State.Set(workflow.State{Status: status, Start: start})
		if preFailed {
			b.PreChecks = recoveryChecks(workflow.Failed, settlementAction("block pre check", workflow.Failed, start))
		}
		b.DeferredChecks = recoveryChecks(deferredStatus, settlementAction("block deferred check", deferredStatus, start))
		return b
	}

	tests := []struct {
		name         string
		plan         func() *workflow.Plan
		wantRan      map[string]int
		wantDeferred workflow.Status
		wantBlock    workflow.Status
		wantStatus   workflow.Status
		wantReason   workflow.FailureReason
		wantErr      bool
	}{
		{
			name: "Success: a Running block whose sequence completed runs its interrupted DeferredChecks and completes",
			plan: func() *workflow.Plan {
				return runningPlan(block(workflow.Running, workflow.Completed, false, workflow.Running))
			},
			wantRan:      map[string]int{"block deferred check": 1},
			wantDeferred: workflow.Completed,
			wantBlock:    workflow.Completed,
			wantStatus:   workflow.Completed,
		},
		{
			name: "Error: a Failed block runs its interrupted DeferredChecks before the Plan fails",
			plan: func() *workflow.Plan {
				return runningPlan(block(workflow.Failed, workflow.NotStarted, true, workflow.Running))
			},
			wantRan:      map[string]int{"block deferred check": 1},
			wantDeferred: workflow.Completed,
			wantBlock:    workflow.Failed,
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRBlock,
			wantErr:      true,
		},
		{
			name: "Error: a Failed block runs its DeferredChecks that had not started before the Plan fails",
			plan: func() *workflow.Plan {
				return runningPlan(block(workflow.Failed, workflow.NotStarted, true, workflow.NotStarted))
			},
			wantRan:      map[string]int{"block deferred check": 1},
			wantDeferred: workflow.Completed,
			wantBlock:    workflow.Failed,
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRBlock,
			wantErr:      true,
		},
		{
			name: "Error: a Running block whose PreChecks failed runs its DeferredChecks before the Plan fails",
			plan: func() *workflow.Plan {
				return runningPlan(block(workflow.Running, workflow.NotStarted, true, workflow.NotStarted))
			},
			wantRan:      map[string]int{"block deferred check": 1},
			wantDeferred: workflow.Completed,
			wantBlock:    workflow.Failed,
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRBlock,
			wantErr:      true,
		},
	}

	for _, test := range tests {
		got := runRecovered(t, test.plan())
		switch {
		case got.err == nil && test.wantErr:
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got err == nil, want err != nil", test.name)
		case got.err != nil && !test.wantErr:
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got err == %s, want err == nil", test.name, got.err)
		}

		if diff := pretty.Compare(test.wantRan, got.ran); diff != "" {
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): action runs -want +got:\n%s", test.name, diff)
		}
		b := got.stored.Blocks[0]
		if got := b.DeferredChecks.GetState().Status; got != test.wantDeferred {
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got block DeferredChecks status %v, want %v", test.name, got, test.wantDeferred)
		}
		if got := b.GetState().Status; got != test.wantBlock {
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got block status %v, want %v", test.name, got, test.wantBlock)
		}
		if got := got.stored.GetState().Status; got != test.wantStatus {
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got Plan status %v, want %v", test.name, got, test.wantStatus)
		}
		if got.stored.Reason != test.wantReason {
			t.Errorf("TestRecoveryFailedBlockDeferredChecks(%s): got Plan reason %v, want %v", test.name, got.stored.Reason, test.wantReason)
		}
	}
}

// contFailedShape describes the Running Plan built by contFailedPlan.
type contFailedShape struct {
	// contFailed gives the Plan ContChecks whose pass failed before the crash. Otherwise the Plan has no ContChecks.
	contFailed bool
	// aStatus is the status of action "a", the action the first sequence was running at the crash.
	aStatus workflow.Status
	// aAttempts are the attempts stored for "a".
	aAttempts []workflow.Attempt
	// aRetries is how many times "a" may be retried.
	aRetries int
	// next adds action "a next", NotStarted, after "a" in the first sequence.
	next bool
	// other adds a second sequence, NotStarted, holding action "other".
	other bool
	// blockCont gives the block ContChecks with these actions.
	blockCont []*workflow.Action
	// blockPost gives the block PostChecks with these actions.
	blockPost []*workflow.Action
}

// contFailedPlan returns a Running Plan with one Running block built from shape, and deferred batches "success"
// (OnSuccess), "failure" (OnFailure) and "always" (Always).
func contFailedPlan(shape contFailedShape) *workflow.Plan {
	start := time.Now().Add(-time.Minute)

	a := settlementAction("a", shape.aStatus, start)
	a.Attempts.Set(shape.aAttempts)
	a.Retries = shape.aRetries
	seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{a}}
	if shape.next {
		seq.Actions = append(seq.Actions, settlementAction("a next", workflow.NotStarted, start))
	}
	seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
	b := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}, Concurrency: 1}
	if shape.other {
		other := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{settlementAction("other", workflow.NotStarted, start)}}
		b.Sequences = append(b.Sequences, other)
	}
	if shape.blockCont != nil {
		b.ContChecks = recoveryChecks(workflow.Running, shape.blockCont...)
	}
	if shape.blockPost != nil {
		b.PostChecks = recoveryChecks(workflow.Running, shape.blockPost...)
	}
	b.State.Set(workflow.State{Status: workflow.Running, Start: start})

	p := runningPlan(b)
	if shape.contFailed {
		p.ContChecks = recoveryChecks(workflow.Failed, settlementAction("plan cont", workflow.Failed, start))
	}
	p.DeferredActions = recoveryDA(
		workflow.NotStarted,
		recoveryBatch(workflow.OnSuccess, false, workflow.NotStarted, settlementAction("success", workflow.NotStarted, start)),
		recoveryBatch(workflow.OnFailure, false, workflow.NotStarted, settlementAction("failure", workflow.NotStarted, start)),
		recoveryBatch(workflow.Always, false, workflow.NotStarted, settlementAction("always", workflow.NotStarted, start)),
	)
	return p
}

// TestRecoveryContChecksFailed is a regression test. A Plan whose ContChecks had failed was recovered by settling every
// Running object Failed by its status alone. An action that had finished its attempt successfully, but was still
// stored Running, became Failed with no error, and one the crash cut mid-attempt was Failed with a dangling attempt.
// Recovery must settle each action Running at the crash from its attempts, run one cut mid-attempt again to
// completion, and then stop: no further action, sequence or block starts. The block ContChecks are not run again and
// keep their last outcome. The block PostChecks always run to completion once the block's sequences are done.
func TestRecoveryContChecksFailed(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-time.Minute)
	finished := []workflow.Attempt{{Start: start, End: start}}
	dangling := []workflow.Attempt{{Start: start}}
	retryable := []workflow.Attempt{{Start: start, End: start, Err: &plugins.Error{Message: "retryable"}}}
	permanent := []workflow.Attempt{{Start: start, End: start, Err: &plugins.Error{Message: "permanent", Permanent: true}}}

	tests := []struct {
		name  string
		shape contFailedShape
		// fail names the actions that fail when they run.
		fail []string
		// failType fails the first write of an object of this type, so the first Recovery ends on it and a second
		// runs on what it stored, as a restart does.
		failType workflow.ObjectType
		// wantRan is how many times each action ran, by name.
		wantRan map[string]int
		// wantActions are the statuses of actions by name.
		wantActions map[string]workflow.Status
		// wantAttempts is how many attempts "a" ends with. Each must have finished.
		wantAttempts  int
		wantSeqs      []workflow.Status
		wantBlockCont workflow.Status
		wantBlockPost workflow.Status
		wantBlock     workflow.Status
		wantBatches   []workflow.Status
		wantStatus    workflow.Status
		wantReason    workflow.FailureReason
		wantErr       bool
	}{
		{
			name:         "Success: with no failed Plan ContChecks, an action that finished before the crash completes and the rest of the block runs",
			shape:        contFailedShape{aStatus: workflow.Running, aAttempts: finished, next: true, other: true},
			wantRan:      map[string]int{"a next": 1, "other": 1, "success": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.Completed, "other": workflow.Completed},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Completed, workflow.Completed},
			wantBlock:    workflow.Completed,
			wantBatches:  []workflow.Status{workflow.Completed, workflow.NotStarted, workflow.Completed},
			wantStatus:   workflow.Completed,
		},
		{
			name:         "Error: an action whose attempt succeeded before the crash completes without running again and nothing after it starts",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: finished, next: true, other: true},
			wantRan:      map[string]int{"failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			name:         "Error: the last action of the only sequence, whose attempt succeeded before the crash, completes its sequence and block",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: finished},
			wantRan:      map[string]int{"failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Completed},
			wantBlock:    workflow.Completed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			name:         "Error: an action cut mid-attempt runs again once to completion and nothing after it starts",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: dangling, next: true, other: true},
			wantRan:      map[string]int{"a": 1, "failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			// Regression: the first Recovery stored the cut action NotStarted before it ran again. When that Recovery
			// ended on a failed write, the next one took the action for one never started and did not run it.
			name:         "Error: an action cut mid-attempt still runs again once to completion when the first Recovery fails on its Plan write",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: dangling, next: true, other: true},
			failType:     workflow.OTPlan,
			wantRan:      map[string]int{"a": 1, "failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			name:         "Error: an action between its retries still runs again once to completion when the first Recovery fails on its Plan write",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: retryable, aRetries: 3, next: true, other: true},
			failType:     workflow.OTPlan,
			wantRan:      map[string]int{"a": 1, "failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 2,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			// The failed Plan ContChecks are the recovery under test, which every row below the first shares; the one
			// input wrong against the row before is the second failure this row adds.
			name:         "Error: an action cut mid-attempt runs again once and fails, and nothing after it starts",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: dangling, next: true, other: true},
			fail:         []string{"a"},
			wantRan:      map[string]int{"a": 1, "failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Failed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			name:         "Success: with no failed Plan ContChecks, an action whose attempt failed before the crash with retries left is retried and the rest of the block runs",
			shape:        contFailedShape{aStatus: workflow.Running, aAttempts: retryable, aRetries: 3, next: true, other: true},
			wantRan:      map[string]int{"a": 1, "a next": 1, "other": 1, "success": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.Completed, "other": workflow.Completed},
			wantAttempts: 2,
			wantSeqs:     []workflow.Status{workflow.Completed, workflow.Completed},
			wantBlock:    workflow.Completed,
			wantBatches:  []workflow.Status{workflow.Completed, workflow.NotStarted, workflow.Completed},
			wantStatus:   workflow.Completed,
		},
		{
			name:         "Error: with no failed Plan ContChecks, an action whose attempt failed before the crash with no retries left is not retried",
			shape:        contFailedShape{aStatus: workflow.Running, aAttempts: retryable, next: true, other: true},
			wantRan:      map[string]int{"failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Failed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRBlock,
			wantErr:      true,
		},
		{
			name:         "Error: with no failed Plan ContChecks, an action whose attempt failed permanently before the crash is not retried",
			shape:        contFailedShape{aStatus: workflow.Running, aAttempts: permanent, aRetries: 3, next: true, other: true},
			wantRan:      map[string]int{"failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Failed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 1,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRBlock,
			wantErr:      true,
		},
		{
			name:         "Error: an action whose attempt failed before the crash with retries left is retried once to completion and nothing after it starts",
			shape:        contFailedShape{contFailed: true, aStatus: workflow.Running, aAttempts: retryable, aRetries: 3, next: true, other: true},
			wantRan:      map[string]int{"a": 1, "failure": 1, "always": 1},
			wantActions:  map[string]workflow.Status{"a": workflow.Completed, "a next": workflow.NotStarted, "other": workflow.NotStarted},
			wantAttempts: 2,
			wantSeqs:     []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantBlock:    workflow.Failed,
			wantBatches:  []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:   workflow.Failed,
			wantReason:   workflow.FRContCheck,
			wantErr:      true,
		},
		{
			name: "Error: a block ContChecks pass cut with no failure is not run again and keeps the passing outcome",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockCont: []*workflow.Action{settlementAction("block cont done", workflow.Completed, start), settlementAction("block cont cut", workflow.Running, start)},
			},
			wantRan:       map[string]int{"failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block cont done": workflow.Completed, "block cont cut": workflow.NotStarted},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockCont: workflow.Completed,
			wantBlock:     workflow.Completed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
		{
			// The failed Plan ContChecks are the recovery under test, which every row below the first shares; the one
			// input wrong against the row before is the second failure this row adds.
			name: "Error: a block ContChecks pass cut after an action failed is not run again and fails the block",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockCont: []*workflow.Action{settlementAction("block cont failed", workflow.Failed, start), settlementAction("block cont cut", workflow.Running, start)},
			},
			wantRan:       map[string]int{"failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block cont failed": workflow.Failed},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockCont: workflow.Failed,
			wantBlock:     workflow.Failed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
		{
			// The failed Plan ContChecks are the recovery under test, which every row below the first shares; the one
			// input wrong against the row before is the second failure this row adds.
			// Regression: the failed sibling failed the ContChecks, but the action between its retries was left Running
			// in a block that ends Failed, so the finished Plan stored it Running.
			name: "Error: a block ContChecks pass with a failed action settles an action between its retries Failed",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockCont: []*workflow.Action{settlementAction("block cont failed", workflow.Failed, start), retryingAction("block cont retrying", start)},
			},
			wantRan:       map[string]int{"failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block cont failed": workflow.Failed, "block cont retrying": workflow.Failed},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockCont: workflow.Failed,
			wantBlock:     workflow.Failed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
		{
			// Same shape as the row above, for the block PostChecks.
			name: "Error: a block PostChecks pass with a failed action settles an action between its retries Failed",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockPost: []*workflow.Action{settlementAction("block post failed", workflow.Failed, start), retryingAction("block post retrying", start)},
			},
			wantRan:       map[string]int{"failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block post failed": workflow.Failed, "block post retrying": workflow.Failed},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockPost: workflow.Failed,
			wantBlock:     workflow.Failed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
		{
			name: "Error: a block PostChecks pass cut by the crash runs again once to completion",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockPost: []*workflow.Action{settlementAction("block post", workflow.Running, start)},
			},
			wantRan:       map[string]int{"block post": 1, "failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block post": workflow.Completed},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockPost: workflow.Completed,
			wantBlock:     workflow.Completed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
		{
			// The failed Plan ContChecks are the recovery under test, which every row below the first shares; the one
			// input wrong against the row before is the second failure this row adds.
			name: "Error: a block PostChecks pass cut by the crash runs again once and fails the block",
			shape: contFailedShape{
				contFailed: true, aStatus: workflow.Running, aAttempts: finished,
				blockPost: []*workflow.Action{settlementAction("block post", workflow.Running, start)},
			},
			fail:          []string{"block post"},
			wantRan:       map[string]int{"block post": 1, "failure": 1, "always": 1},
			wantActions:   map[string]workflow.Status{"a": workflow.Completed, "block post": workflow.Failed},
			wantAttempts:  1,
			wantSeqs:      []workflow.Status{workflow.Completed},
			wantBlockPost: workflow.Failed,
			wantBlock:     workflow.Failed,
			wantBatches:   []workflow.Status{workflow.NotStarted, workflow.Completed, workflow.Completed},
			wantStatus:    workflow.Failed,
			wantReason:    workflow.FRContCheck,
			wantErr:       true,
		},
	}

	for _, test := range tests {
		got := runRecoveredRetried(t, contFailedPlan(test.shape), test.failType, test.fail...)
		switch {
		case got.err == nil && test.wantErr:
			t.Errorf("TestRecoveryContChecksFailed(%s): got err == nil, want err != nil", test.name)
		case got.err != nil && !test.wantErr:
			t.Errorf("TestRecoveryContChecksFailed(%s): got err == %s, want err == nil", test.name, got.err)
		}

		if diff := pretty.Compare(test.wantRan, got.ran); diff != "" {
			t.Errorf("TestRecoveryContChecksFailed(%s): action runs -want +got:\n%s", test.name, diff)
		}

		gotActions := map[string]workflow.Status{}
		var a *workflow.Action
		for item := range walk.Plan(got.stored) {
			action, ok := item.Value.(*workflow.Action)
			if !ok {
				continue
			}
			if action.Name == "a" {
				a = action
			}
			if _, ok := test.wantActions[action.Name]; ok {
				gotActions[action.Name] = action.State.Get().Status
			}
		}
		if diff := pretty.Compare(test.wantActions, gotActions); diff != "" {
			t.Errorf("TestRecoveryContChecksFailed(%s): action statuses -want +got:\n%s", test.name, diff)
		}
		attempts := a.Attempts.Get()
		if len(attempts) != test.wantAttempts {
			t.Errorf("TestRecoveryContChecksFailed(%s): got %d attempts on action a, want %d", test.name, len(attempts), test.wantAttempts)
		}
		for i, attempt := range attempts {
			if attempt.End.IsZero() {
				t.Errorf("TestRecoveryContChecksFailed(%s): attempt %d of action a never finished", test.name, i)
			}
		}
		if a.State.Get().Status == workflow.Failed && (len(attempts) == 0 || attempts[len(attempts)-1].Err == nil) {
			t.Errorf("TestRecoveryContChecksFailed(%s): action a is Failed with no attempt error", test.name)
		}

		b := got.stored.Blocks[0]
		var seqs []workflow.Status
		for _, seq := range b.Sequences {
			seqs = append(seqs, seq.State.Get().Status)
		}
		if diff := pretty.Compare(test.wantSeqs, seqs); diff != "" {
			t.Errorf("TestRecoveryContChecksFailed(%s): sequence statuses -want +got:\n%s", test.name, diff)
		}
		if b.ContChecks != nil {
			if got := b.ContChecks.GetState().Status; got != test.wantBlockCont {
				t.Errorf("TestRecoveryContChecksFailed(%s): got block ContChecks status %v, want %v", test.name, got, test.wantBlockCont)
			}
		}
		if b.PostChecks != nil {
			if got := b.PostChecks.GetState().Status; got != test.wantBlockPost {
				t.Errorf("TestRecoveryContChecksFailed(%s): got block PostChecks status %v, want %v", test.name, got, test.wantBlockPost)
			}
		}
		if got := b.GetState().Status; got != test.wantBlock {
			t.Errorf("TestRecoveryContChecksFailed(%s): got block status %v, want %v", test.name, got, test.wantBlock)
		}

		var batches []workflow.Status
		for _, batch := range got.stored.DeferredActions.DeferredBatches {
			batches = append(batches, batch.GetState().Status)
		}
		if diff := pretty.Compare(test.wantBatches, batches); diff != "" {
			t.Errorf("TestRecoveryContChecksFailed(%s): deferred batch statuses -want +got:\n%s", test.name, diff)
		}
		if got := got.stored.GetState().Status; got != test.wantStatus {
			t.Errorf("TestRecoveryContChecksFailed(%s): got Plan status %v, want %v", test.name, got, test.wantStatus)
		}
		if got.stored.Reason != test.wantReason {
			t.Errorf("TestRecoveryContChecksFailed(%s): got Plan reason %v, want %v", test.name, got.stored.Reason, test.wantReason)
		}
	}
}
