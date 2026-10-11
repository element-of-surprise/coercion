package sm

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/durable"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// interruptedBlockPlan returns a Running Plan whose one sequence has two actions that each finished an attempt, but
// were never written as finished. Recovery changes the actions, the sequence and the block.
func interruptedBlockPlan() *workflow.Plan {
	start := time.Now().Add(-time.Minute)
	a1 := &workflow.Action{ID: workflow.NewV7()}
	a1.State.Set(workflow.State{Status: workflow.Running, Start: start})
	a1.Attempts.Set([]workflow.Attempt{{Start: start, End: time.Now()}})
	a2 := &workflow.Action{ID: workflow.NewV7()}
	a2.State.Set(workflow.State{Status: workflow.Running, Start: start})
	a2.Attempts.Set([]workflow.Attempt{{Start: start, End: time.Now()}})
	seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{a1, a2}}
	seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
	block := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}}
	block.State.Set(workflow.State{Status: workflow.Running, Start: start})
	plan := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{block}}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// interruptedDeferredPlan returns a Running Plan with a finished block and a deferred batch whose action finished an
// attempt, but was never written as finished. Recovery changes the action, the batch and the deferred actions.
func interruptedDeferredPlan() *workflow.Plan {
	start := time.Now().Add(-time.Minute)
	block := &workflow.Block{ID: workflow.NewV7()}
	block.State.Set(workflow.State{Status: workflow.Completed, Start: start, End: start})
	action := &workflow.Action{ID: workflow.NewV7()}
	action.State.Set(workflow.State{Status: workflow.Running, Start: start})
	action.Attempts.Set([]workflow.Attempt{{Start: start, End: time.Now()}})
	batch := &workflow.DeferBatch{Sequence: workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{action}}}
	batch.State.Set(workflow.State{Status: workflow.Running, Start: start})
	da := &workflow.DeferredActions{ID: workflow.NewV7(), DeferredBatches: []*workflow.DeferBatch{batch}}
	da.State.Set(workflow.State{Status: workflow.Running, Start: start})
	plan := &workflow.Plan{ID: workflow.NewV7(), Blocks: []*workflow.Block{block}, DeferredActions: da}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// recoverStored runs Recovery on what store holds, as a restart or Resume does.
func recoverStored(t *testing.T, store *durable.Store, id uuid.UUID) error {
	t.Helper()

	plan, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatalf("recoverStored: Read: %s", err)
	}
	s := &States{store: store}
	req := s.Recovery(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan, RecoveryStarted: NewStarted()}})
	return req.Err
}

// TestRecoveryRetry is a regression test. Recovery works out objects' final states and writes them; when a write in
// the middle failed and the recovery was retried, a parent already stored as final was skipped and its children were
// never fixed, leaving finished parents over Running children for good. Recovery now writes each object after its
// descendants. After one failed write and a retry, storage must hold exactly what a recovery that never failed would.
func TestRecoveryRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan func() *workflow.Plan
		// failType is the object type whose first write fails.
		failType workflow.ObjectType
	}{
		{
			name:     "Success: bypass plan converges after its action write fails",
			plan:     interruptedBypassPlan,
			failType: workflow.OTAction,
		},
		{
			name:     "Success: bypass plan converges after its checks write fails",
			plan:     interruptedBypassPlan,
			failType: workflow.OTCheck,
		},
		{
			name:     "Success: block plan converges after an action write fails",
			plan:     interruptedBlockPlan,
			failType: workflow.OTAction,
		},
		{
			name:     "Success: block plan converges after its sequence write fails",
			plan:     interruptedBlockPlan,
			failType: workflow.OTSequence,
		},
		{
			name:     "Success: deferred plan converges after its action write fails",
			plan:     interruptedDeferredPlan,
			failType: workflow.OTAction,
		},
		{
			name:     "Success: deferred plan converges after its batch write fails",
			plan:     interruptedDeferredPlan,
			failType: workflow.OTBatch,
		},
		{
			name:     "Success: deferred plan converges after its deferred actions write fails",
			plan:     interruptedDeferredPlan,
			failType: workflow.OTDeferredActions,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		plan := test.plan()

		reference := durable.New(ctx, plan, workflow.OTUnknown)
		if err := recoverStored(t, reference, plan.ID); err != nil {
			t.Errorf("TestRecoveryRetry(%s): reference recovery: got err == %s, want err == nil", test.name, err)
			continue
		}

		store := durable.New(ctx, plan, test.failType)
		if err := recoverStored(t, store, plan.ID); err == nil {
			t.Errorf("TestRecoveryRetry(%s): first recovery: got err == nil, want the injected write failure", test.name)
			continue
		}
		if err := recoverStored(t, store, plan.ID); err != nil {
			t.Errorf("TestRecoveryRetry(%s): retried recovery: got err == %s, want err == nil", test.name, err)
			continue
		}

		if diff := pretty.Compare(reference.Statuses(), store.Statuses()); diff != "" {
			t.Errorf("TestRecoveryRetry(%s): stored statuses after the retry -want +got:\n%s", test.name, diff)
		}
	}
}

// failedChecksPlan returns a Running Plan whose PreChecks failed while its ContChecks and a block are still Running,
// as when the process stopped right after the PreChecks failure was written.
func failedChecksPlan() *workflow.Plan {
	start := time.Now().Add(-time.Minute)
	failedAction := &workflow.Action{ID: workflow.NewV7()}
	failedAction.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: time.Now()})
	pre := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{failedAction}}
	pre.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: time.Now()})

	contAction := &workflow.Action{ID: workflow.NewV7()}
	contAction.State.Set(workflow.State{Status: workflow.Running, Start: start})
	cont := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{contAction}}
	cont.State.Set(workflow.State{Status: workflow.Running, Start: start})

	action := &workflow.Action{ID: workflow.NewV7()}
	action.State.Set(workflow.State{Status: workflow.Running, Start: start})
	seq := &workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{action}}
	seq.State.Set(workflow.State{Status: workflow.Running, Start: start})
	block := &workflow.Block{ID: workflow.NewV7(), Sequences: []*workflow.Sequence{seq}}
	block.State.Set(workflow.State{Status: workflow.Running, Start: start})

	plan := &workflow.Plan{ID: workflow.NewV7(), PreChecks: pre, ContChecks: cont, Blocks: []*workflow.Block{block}}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// failedContChecksPlan returns a Running Plan whose ContChecks are still Running over a failed action, with a block
// still Running after them.
func failedContChecksPlan() *workflow.Plan {
	plan := failedChecksPlan()
	plan.PreChecks = nil
	failedAction := &workflow.Action{ID: workflow.NewV7()}
	failedAction.State.Set(workflow.State{Status: workflow.Failed, Start: time.Now().Add(-time.Minute), End: time.Now()})
	plan.ContChecks.Actions = []*workflow.Action{failedAction}
	return plan
}

// TestRecoveryFinishedPlan is a regression test: when fixPlan decided a Plan failed (a failed PreChecks or ContChecks,
// or a stopped block) it returned before settling the objects after that point, and End did not settle them either,
// so storage kept a Failed Plan over Running checks and blocks for good. Nothing may be stored Running under a Plan
// that recovery and the states after it finished.
func TestRecoveryFinishedPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan func() *workflow.Plan
	}{
		{
			name: "Success: a Plan whose PreChecks failed is stored with no object still Running",
			plan: failedChecksPlan,
		},
		{
			name: "Success: a Plan whose ContChecks failed is stored with its blocks no longer Running",
			plan: failedContChecksPlan,
		},
	}

	for _, test := range tests {
		plan := test.plan()
		store := durable.New(t.Context(), plan, workflow.OTUnknown)

		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRecoveryFinishedPlan(%s): Read: %s", test.name, err)
		}
		s := &States{
			store: store,
			// Recovery runs an action the crash cut mid-attempt again when the Plan's ContChecks had failed.
			testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
				a.State.Set(workflow.State{Status: workflow.Completed, Start: time.Now(), End: time.Now()})
				return updater.UpdateAction(ctx, a)
			},
		}
		req := s.Recovery(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: stored, RecoveryStarted: NewStarted()}})
		if req.Err != nil {
			t.Errorf("TestRecoveryFinishedPlan(%s): Recovery: got err == %s, want err == nil", test.name, req.Err)
			continue
		}
		if req.Next != nil {
			// The Plan fails, so the run's error is expected and not what this test checks.
			_, _ = statemachine.Run("TestRecoveryFinishedPlan", req)
		}

		for key, status := range store.Statuses() {
			if status == workflow.Running.String() {
				t.Errorf("TestRecoveryFinishedPlan(%s): stored object %s is still Running", test.name, key)
			}
		}
	}
}

// TestRecoveryDeferred is a regression test. A recovered Plan that fixPlan settled to Failed went straight to End,
// so its OnFailure deferred batches never ran, though a live run always runs them. And fixPlan returned for failed
// PreChecks before it fixed the DeferredActions, so an action that had finished before the crash, but was still
// stored Running, was not settled from its attempt and ran again when the deferred work resumed.
func TestRecoveryDeferred(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-time.Minute)

	tests := []struct {
		name string
		plan func() *workflow.Plan
		// wantRan is how many times each deferred action ran, by name.
		wantRan map[string]int
		// wantBatches are the deferred batch statuses, in order.
		wantBatches []workflow.Status
		wantStatus  workflow.Status
		wantReason  workflow.FailureReason
		wantErr     bool
	}{
		{
			name: "Success: a Plan whose block completed runs its OnSuccess deferred batch and completes",
			plan: func() *workflow.Plan {
				block := newBlockWithState(&workflow.State{Status: workflow.Completed, Start: start, End: start})
				p := &workflow.Plan{
					ID:              workflow.NewV7(),
					Blocks:          []*workflow.Block{block},
					DeferredActions: newDA(newDABatch(workflow.OnSuccess, false, "onSuccess"), newDABatch(workflow.OnFailure, false, "onFailure")),
				}
				p.State.Set(workflow.State{Status: workflow.Running, Start: start})
				return p
			},
			wantRan:     map[string]int{"onSuccess": 1},
			wantBatches: []workflow.Status{workflow.Completed, workflow.NotStarted},
			wantStatus:  workflow.Completed,
		},
		{
			name: "Error: a Plan whose block failed before the crash runs its OnFailure deferred batch",
			plan: func() *workflow.Plan {
				block := newBlockWithState(&workflow.State{Status: workflow.Failed, Start: start, End: start})
				p := &workflow.Plan{
					ID:              workflow.NewV7(),
					PostChecks:      newChecksWithState(&workflow.State{}),
					Blocks:          []*workflow.Block{block},
					DeferredActions: newDA(newDABatch(workflow.OnSuccess, false, "onSuccess"), newDABatch(workflow.OnFailure, false, "onFailure")),
				}
				p.State.Set(workflow.State{Status: workflow.Running, Start: start})
				return p
			},
			wantRan:     map[string]int{"onFailure": 1},
			wantBatches: []workflow.Status{workflow.NotStarted, workflow.Completed},
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRBlock,
			wantErr:     true,
		},
		{
			name: "Error: a Plan whose PreChecks failed settles its finished deferred action without running it again",
			plan: func() *workflow.Plan {
				failedAction := &workflow.Action{ID: workflow.NewV7()}
				failedAction.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: start})
				pre := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{failedAction}}
				pre.State.Set(workflow.State{Status: workflow.Failed, Start: start, End: start})

				batch := newDABatch(workflow.OnFailure, false, "onFailure")
				batch.State.Set(workflow.State{Status: workflow.Running, Start: start})
				// The action finished its attempt before the crash, but was still stored Running.
				batch.Actions[0].State.Set(workflow.State{Status: workflow.Running, Start: start})
				batch.Actions[0].Attempts.Set([]workflow.Attempt{{Start: start, End: start}})
				da := newDA(batch)
				da.State.Set(workflow.State{Status: workflow.Running, Start: start})

				// DeferredChecks still to run keep the Plan Running after recovery, so its deferred work resumes.
				deferred := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{{ID: workflow.NewV7(), Name: "deferredCheck"}}}
				deferred.State.Set(workflow.State{})
				deferred.Actions[0].State.Set(workflow.State{})

				p := &workflow.Plan{
					ID:              workflow.NewV7(),
					PreChecks:       pre,
					Blocks:          []*workflow.Block{newBlockWithState(&workflow.State{})},
					DeferredActions: da,
					DeferredChecks:  deferred,
				}
				p.State.Set(workflow.State{Status: workflow.Running, Start: start})
				return p
			},
			wantRan:     map[string]int{"deferredCheck": 1},
			wantBatches: []workflow.Status{workflow.Completed},
			wantStatus:  workflow.Failed,
			wantReason:  workflow.FRPreCheck,
			wantErr:     true,
		},
	}

	for _, test := range tests {
		plan := test.plan()

		var ranMu sync.Mutex
		ran := map[string]int{}
		store := &fakeUpdater{}
		s := &States{
			store: store,
			testActionRunner: func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
				ranMu.Lock()
				defer ranMu.Unlock()
				ran[action.Name]++
				return nil
			},
		}

		req := statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan}, Next: s.Recovery}
		_, err := statemachine.Run("TestRecoveryDeferred", req)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRecoveryDeferred(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestRecoveryDeferred(%s): got err == %s, want err == nil", test.name, err)
		}

		if got := plan.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestRecoveryDeferred(%s): got Plan status %v, want %v", test.name, got, test.wantStatus)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestRecoveryDeferred(%s): got Plan reason %v, want %v", test.name, plan.Reason, test.wantReason)
		}
		ranMu.Lock()
		if diff := pretty.Compare(test.wantRan, ran); diff != "" {
			t.Errorf("TestRecoveryDeferred(%s): deferred action runs -want +got:\n%s", test.name, diff)
		}
		ranMu.Unlock()
		var batches []workflow.Status
		for _, b := range plan.DeferredActions.DeferredBatches {
			batches = append(batches, b.State.Get().Status)
		}
		if diff := pretty.Compare(test.wantBatches, batches); diff != "" {
			t.Errorf("TestRecoveryDeferred(%s): deferred batch statuses -want +got:\n%s", test.name, diff)
		}
		// Recovery only resumes Running Plans, so a Plan stored as finished before its deferred work is done would
		// lose that work to a second crash. Only the last Plan write may be final.
		store.lock.Lock()
		for i, p := range store.plans[:max(len(store.plans)-1, 0)] {
			if st := p.State.Get().Status; st != workflow.Running {
				t.Errorf("TestRecoveryDeferred(%s): Plan write %d of %d stored status %v before the deferred work finished, want Running", test.name, i+1, len(store.plans), st)
			}
		}
		store.lock.Unlock()
	}
}

// TestRecoveryCrashBeforeEnd is a regression test. Recovery writes the Plan it settled, then End works out the Plan's
// final state and reason and writes them again. When fixPlan settled the Plan as finished, Recovery stored it as
// Completed or Failed with no reason, though End may decide otherwise: a failed FailElement deferred batch fails a
// Plan whose blocks completed, and failed PreChecks give the reason FRPreCheck. Recovery only resumes Running Plans,
// so a crash (or a fatal storage error in End's write) between the two left that wrong final Plan stored for good.
// After a crash right after Recovery returns and a restart, storage must hold what a run without the crash stores.
func TestRecoveryCrashBeforeEnd(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-time.Minute)

	// outcome is how the tests show a stored Plan's final state and reason.
	outcome := func(p *workflow.Plan) string {
		return p.State.Get().Status.String() + " / " + p.Reason.String()
	}

	tests := []struct {
		name       string
		plan       func() *workflow.Plan
		wantStatus workflow.Status
		wantReason workflow.FailureReason
		wantErr    bool
	}{
		{
			name:       "Success: a Plan whose blocks completed with nothing left to run is stored Completed after a crash",
			plan:       func() *workflow.Plan { return runningPlan(completedBlock()) },
			wantStatus: workflow.Completed,
		},
		{
			name: "Error: a Plan whose FailElement deferred batch failed is stored Failed with FRDeferredAction after a crash",
			plan: func() *workflow.Plan {
				p := runningPlan(completedBlock())
				p.DeferredActions = recoveryDA(
					workflow.Running,
					recoveryBatch(workflow.OnSuccess, true, workflow.Failed, settlementAction("failed cleanup", workflow.Failed, start)),
				)
				return p
			},
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name:       "Error: a Plan whose PreChecks failed is stored Failed with FRPreCheck after a crash",
			plan:       failedChecksPlan,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPreCheck,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		plan := test.plan()
		want := test.wantStatus.String() + " / " + test.wantReason.String()

		// A run without the crash, which pins down what storage must end up holding.
		live := runRecovered(t, plan)
		switch {
		case live.err == nil && test.wantErr:
			t.Errorf("TestRecoveryCrashBeforeEnd(%s): got err == nil, want err != nil", test.name)
			continue
		case live.err != nil && !test.wantErr:
			t.Errorf("TestRecoveryCrashBeforeEnd(%s): got err == %s, want err == nil", test.name, live.err)
			continue
		}
		if got := outcome(live.stored); got != want {
			t.Errorf("TestRecoveryCrashBeforeEnd(%s): run without a crash stored %s, want %s", test.name, got, want)
			continue
		}

		// The same Plan, but the process exits after Recovery returns and before req.Next runs.
		store := durable.New(t.Context(), plan, workflow.OTUnknown)
		stored, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRecoveryCrashBeforeEnd(%s): Read: %s", test.name, err)
		}
		s := &States{store: store}
		req := s.Recovery(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: stored, RecoveryStarted: NewStarted()}})
		if req.Data.stopHeartbeat != nil {
			// The crash ends the heartbeat with the process.
			req.Data.stopHeartbeat()
		}
		if req.Err != nil {
			t.Errorf("TestRecoveryCrashBeforeEnd(%s): Recovery: got err == %s, want err == nil", test.name, req.Err)
			continue
		}
		crashed, err := store.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestRecoveryCrashBeforeEnd(%s): Read: %s", test.name, err)
		}
		// A restart recovers only Plans stored as Running; a Plan stored as finished keeps what is stored.
		if crashed.State.Get().Status == workflow.Running {
			crashed = runRecovered(t, crashed).stored
		}
		if got := outcome(crashed); got != want {
			t.Errorf("TestRecoveryCrashBeforeEnd(%s): after a crash between Recovery and End, storage holds %s, want %s", test.name, got, want)
		}
	}
}
