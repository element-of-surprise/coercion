package sm

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/gostdlib/base/statemachine"
	"github.com/kylelemons/godebug/pretty"
)

// updatePlanStore is a storage.Vault that records the order of writes. UpdatePlan returns err.
type updatePlanStore struct {
	storage.Vault

	err    error
	writes *[]string
}

func (u updatePlanStore) record(w string) {
	if u.writes != nil {
		*u.writes = append(*u.writes, w)
	}
}

func (u updatePlanStore) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	u.record("plan:" + plan.State.Get().Status.String())
	return u.err
}

func (u updatePlanStore) UpdateChecks(ctx context.Context, c *workflow.Checks) error {
	u.record("checks:" + c.State.Get().Status.String())
	return nil
}

func (u updatePlanStore) UpdateAction(ctx context.Context, a *workflow.Action) error {
	u.record("action:" + a.State.Get().Status.String())
	return nil
}

// UpdateChanges writes the changed objects one at a time through the other Update methods, so the writes are recorded
// in the order a Vault that cannot group writes makes them.
func (u updatePlanStore) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	return storage.WriteChanges(ctx, u, plan, before)
}

// interruptedBypassPlan returns a Running Plan whose bypass check's action finished successfully, but whose action and
// checks were never written as finished: the process stopped between the attempt ending and the terminal writes.
func interruptedBypassPlan() *workflow.Plan {
	start := time.Now().Add(-time.Minute)
	action := &workflow.Action{ID: workflow.NewV7()}
	action.State.Set(workflow.State{Status: workflow.Running, Start: start})
	action.Attempts.Set([]workflow.Attempt{{Start: start, End: time.Now()}})
	bypass := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{action}}
	bypass.State.Set(workflow.State{Status: workflow.Running, Start: start})
	plan := &workflow.Plan{ID: workflow.NewV7(), BypassChecks: bypass}
	plan.State.Set(workflow.State{Status: workflow.Running, Start: start})
	return plan
}

// TestWriteEverything is a regression test: the final write used to store the Plan before its objects, so a Plan that
// finished could be stored over objects that had not. Each object must be written after its descendants, and the Plan
// last.
func TestWriteEverything(t *testing.T) {
	t.Parallel()

	plan := interruptedBypassPlan()
	var writes []string
	s := &States{store: updatePlanStore{writes: &writes}}
	s.writeEverything(t.Context(), plan)

	want := []string{"action:Running", "checks:Running", "plan:Running"}
	if diff := pretty.Compare(want, writes); diff != "" {
		t.Errorf("TestWriteEverything: writes -want +got:\n%s", diff)
	}
}

func TestRecovery(t *testing.T) {
	t.Parallel()

	runningPlan := func() *workflow.Plan {
		plan := &workflow.Plan{ID: workflow.NewV7()}
		plan.State.Set(workflow.State{Status: workflow.Running})
		return plan
	}

	tests := []struct {
		name      string
		plan      func() *workflow.Plan
		updateErr error
		// wantWrites is the order of the writes Recovery makes.
		wantWrites []string
		wantNext   bool
		wantErr    bool
	}{
		{
			// Regression: Recovery stored the Plan as Completed before End worked out its final state and reason, so a
			// crash between them kept that outcome for good. Only End writes a finished Plan.
			name:       "Success: a Running plan with nothing left to run is written as still Running and goes to End",
			plan:       runningPlan,
			wantWrites: []string{"plan:Running"},
			wantNext:   true,
		},
		{
			// Regression: Recovery settled objects in memory and wrote only the Plan (then wrote them parents first),
			// so a torn or retried write could leave a finished parent over Running children for good. Each changed
			// object must be written after its descendants, and the Plan last.
			name:       "Success: objects Recovery settles are written after their descendants, and the Plan last",
			plan:       interruptedBypassPlan,
			wantWrites: []string{"action:Completed", "checks:Completed", "plan:Running"},
			wantNext:   true,
		},
		{
			// Before the fix this was log.Fatalf, which exited the process while storage was being throttled.
			name:       "Error: a failed Plan write stops the run with an error instead of exiting the process",
			plan:       runningPlan,
			updateErr:  errors.New("storage busy"),
			wantWrites: []string{"plan:Running"},
			wantErr:    true,
		},
	}

	for _, test := range tests {
		plan := test.plan()
		started := NewStarted()

		var writes []string
		s := &States{store: updatePlanStore{err: test.updateErr, writes: &writes}}
		req := s.Recovery(
			statemachine.Request[Data]{
				Ctx:  t.Context(),
				Data: Data{Plan: plan, RecoveryStarted: started},
			},
		)

		// Recovery reports before it returns, so this must not block. It must carry the write failure, so Resume does
		// not mistake a failed recovery for a started run.
		waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		startErr := started.Wait(waitCtx)
		cancel()
		if (startErr != nil) != test.wantErr {
			t.Errorf("TestRecovery(%s): got RecoveryStarted err == %v, want err != nil == %v", test.name, startErr, test.wantErr)
		}
		if diff := pretty.Compare(test.wantWrites, writes); diff != "" {
			t.Errorf("TestRecovery(%s): writes -want +got:\n%s", test.name, diff)
		}
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestRecovery(%s): got err == nil, want err != nil", test.name)
			continue
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestRecovery(%s): got err == %s, want err == nil", test.name, req.Err)
			continue
		case req.Err != nil:
			if req.Next != nil {
				t.Errorf("TestRecovery(%s): got a next state after a failed write, want the run to stop", test.name)
			}
			continue
		}
		if (req.Next != nil) != test.wantNext {
			t.Errorf("TestRecovery(%s): got next state set == %v, want %v", test.name, req.Next != nil, test.wantNext)
		}
	}
}
