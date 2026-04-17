package sm

import (
	"fmt"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/statemachine"
)

// newDABatch returns a DeferBatch with the given When, FailElement and one
// action whose name determines fake-runner success/failure.
func newDABatch(when workflow.WhenDeferred, failElement bool, actionName string) *workflow.DeferBatch {
	b := &workflow.DeferBatch{When: when, FailElement: failElement}
	b.Name = "batch"
	b.Descr = "batch"
	b.Actions = []*workflow.Action{{Name: actionName}}
	b.State.Set(workflow.State{})
	for _, a := range b.Actions {
		a.State.Set(workflow.State{})
	}
	return b
}

func newDA(batches ...*workflow.DeferBatch) *workflow.DeferredActions {
	da := &workflow.DeferredActions{DeferredBatches: batches}
	da.State.Set(workflow.State{})
	return da
}

func TestPlanDeferredChecksRouting(t *testing.T) {
	t.Parallel()

	states := &States{} // name-only probe

	tests := []struct {
		name          string
		plan          *workflow.Plan
		wantNextState statemachine.State[Data]
	}{
		{
			name: "Success: DeferredChecks nil still routes to End",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				return p
			}(),
			wantNextState: states.End,
		},
		{
			name: "Success: DeferredChecks set routes to End",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				dc := &workflow.Checks{}
				dc.State.Set(workflow.State{})
				p.DeferredChecks = dc
				return p
			}(),
			wantNextState: states.End,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx:  t.Context(),
			Data: Data{Plan: test.plan},
		}

		s := &States{
			store: &fakeUpdater{},
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return nil
			},
		}
		req = s.PlanDeferredChecks(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestPlanDeferredChecksRouting(%s): got next = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestPlanDeferredActions(t *testing.T) {
	t.Parallel()

	states := &States{}

	// wantBatch describes the expected terminal state of one batch in
	// DeferredBatches and the terminal state of each action inside.
	type wantBatch struct {
		status  workflow.Status
		actions []workflow.Status
	}

	tests := []struct {
		name          string
		plan          *workflow.Plan
		reqErr        error
		wantNextState statemachine.State[Data]
		wantDAStatus  workflow.Status
		wantBatches   []wantBatch
	}{
		{
			name: "Success: DeferredActions nil is a no-op",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.NotStarted,
		},
		{
			name: "Success: Plan succeeded runs OnSuccess; OnFailure stays NotStarted",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.OnFailure, false, "untouched"),
					newDABatch(workflow.OnSuccess, false, "ok"),
				)
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.NotStarted, actions: []workflow.Status{workflow.NotStarted}},
				{status: workflow.Completed, actions: []workflow.Status{workflow.Completed}},
			},
		},
		{
			name: "Success: Plan failed via block Failed runs OnFailure; OnSuccess stays NotStarted",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				b := &workflow.Block{}
				b.State.Set(workflow.State{Status: workflow.Failed})
				p.Blocks = []*workflow.Block{b}
				p.DeferredActions = newDA(
					newDABatch(workflow.OnFailure, false, "ok"),
					newDABatch(workflow.OnSuccess, false, "untouched"),
				)
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.Completed, actions: []workflow.Status{workflow.Completed}},
				{status: workflow.NotStarted, actions: []workflow.Status{workflow.NotStarted}},
			},
		},
		{
			name: "Success: Plan failed via req.Data.err runs OnFailure; OnSuccess stays NotStarted",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.OnFailure, false, "ok"),
					newDABatch(workflow.OnSuccess, false, "untouched"),
				)
				return p
			}(),
			reqErr:        fmt.Errorf("boom"),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.Completed, actions: []workflow.Status{workflow.Completed}},
				{status: workflow.NotStarted, actions: []workflow.Status{workflow.NotStarted}},
			},
		},
		{
			name: "Success: Always batch runs regardless of plan outcome (success)",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.Always, false, "ok"),
				)
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.Completed, actions: []workflow.Status{workflow.Completed}},
			},
		},
		{
			name: "Success: Always batch runs regardless of plan outcome (failure)",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.Always, false, "ok"),
				)
				return p
			}(),
			reqErr:        fmt.Errorf("boom"),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.Completed, actions: []workflow.Status{workflow.Completed}},
			},
		},
		{
			name: "Error: FailElement=true batch fails marks DA Failed; action is Failed",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.OnSuccess, true, "error"),
				)
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Failed,
			wantBatches: []wantBatch{
				{status: workflow.Failed, actions: []workflow.Status{workflow.Failed}},
			},
		},
		{
			name: "Success: FailElement=false batch fails leaves DA Completed; batch is Failed",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				p.DeferredActions = newDA(
					newDABatch(workflow.OnSuccess, false, "error"),
				)
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.Failed, actions: []workflow.Status{workflow.Failed}},
			},
		},
		{
			name: "Success: recovered terminal DA is a no-op; batch stays NotStarted",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				da := newDA(newDABatch(workflow.OnSuccess, false, "ok"))
				da.State.Set(workflow.State{Status: workflow.Completed})
				p.DeferredActions = da
				return p
			}(),
			wantNextState: states.PlanDeferredChecks,
			wantDAStatus:  workflow.Completed,
			wantBatches: []wantBatch{
				{status: workflow.NotStarted, actions: []workflow.Status{workflow.NotStarted}},
			},
		},
	}

	checkBatches := func(t *testing.T, testName string, batches []*workflow.DeferBatch, want []wantBatch) {
		t.Helper()
		if len(want) == 0 {
			return
		}
		if len(batches) != len(want) {
			t.Errorf("TestPlanDeferredActions(%s): batch count = %d, want %d", testName, len(batches), len(want))
			return
		}
		for i, b := range batches {
			if got := b.State.Get().Status; got != want[i].status {
				t.Errorf("TestPlanDeferredActions(%s): batch[%d] status = %v, want %v", testName, i, got, want[i].status)
			}
			if len(b.Actions) != len(want[i].actions) {
				t.Errorf("TestPlanDeferredActions(%s): batch[%d] action count = %d, want %d", testName, i, len(b.Actions), len(want[i].actions))
				continue
			}
			for j, a := range b.Actions {
				if got := a.State.Get().Status; got != want[i].actions[j] {
					t.Errorf("TestPlanDeferredActions(%s): batch[%d].Actions[%d] status = %v, want %v", testName, i, j, got, want[i].actions[j])
				}
			}
		}
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx:  t.Context(),
			Data: Data{Plan: test.plan, err: test.reqErr},
		}

		s := &States{
			store: &fakeUpdater{},
			testActionRunner: func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
				state := action.State.Get()
				state.Status = workflow.Completed
				if action.Name == "error" {
					state.Status = workflow.Failed
					action.State.Set(state)
					return fmt.Errorf("error")
				}
				action.State.Set(state)
				return nil
			},
		}
		req = s.PlanDeferredActions(req)

		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestPlanDeferredActions(%s): got next = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if test.plan.DeferredActions == nil {
			continue
		}
		if got := test.plan.DeferredActions.State.Get().Status; got != test.wantDAStatus {
			t.Errorf("TestPlanDeferredActions(%s): got DA status = %v, want %v", test.name, got, test.wantDAStatus)
		}
		checkBatches(t, test.name, test.plan.DeferredActions.DeferredBatches, test.wantBatches)
	}
}

func TestRunDeferBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		batch      *workflow.DeferBatch
		wantStatus workflow.Status
	}{
		{
			name: "Success: all actions succeed",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{})
				b.Actions = []*workflow.Action{{Name: "ok1"}, {Name: "ok2"}}
				for _, a := range b.Actions {
					a.State.Set(workflow.State{})
				}
				return b
			}(),
			wantStatus: workflow.Completed,
		},
		{
			name: "Error: first action fails stops batch as Failed",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{})
				b.Actions = []*workflow.Action{{Name: "error"}, {Name: "ok"}}
				for _, a := range b.Actions {
					a.State.Set(workflow.State{})
				}
				return b
			}(),
			wantStatus: workflow.Failed,
		},
		{
			name: "Success: empty actions completes",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{})
				return b
			}(),
			wantStatus: workflow.Completed,
		},
		{
			name: "Success: recovered Completed batch is a no-op",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{Status: workflow.Completed})
				b.Actions = []*workflow.Action{{Name: "error"}}
				for _, a := range b.Actions {
					a.State.Set(workflow.State{Status: workflow.Completed})
				}
				return b
			}(),
			wantStatus: workflow.Completed,
		},
		{
			name: "Success: recovered pre-stopped action stops batch",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{})
				a1 := &workflow.Action{Name: "ok"}
				a1.State.Set(workflow.State{Status: workflow.Stopped})
				b.Actions = []*workflow.Action{a1}
				return b
			}(),
			wantStatus: workflow.Stopped,
		},
		{
			name: "Error: recovered pre-failed action fails batch",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.Name = "b"
				b.Descr = "b"
				b.State.Set(workflow.State{})
				a1 := &workflow.Action{Name: "ok"}
				a1.State.Set(workflow.State{Status: workflow.Failed})
				a2 := &workflow.Action{Name: "ok"}
				a2.State.Set(workflow.State{})
				b.Actions = []*workflow.Action{a1, a2}
				return b
			}(),
			wantStatus: workflow.Failed,
		},
	}

	for _, test := range tests {
		s := &States{
			store:            &fakeUpdater{},
			testActionRunner: fakeActionRunner,
		}
		s.runDeferBatch(t.Context(), test.batch)
		if got := test.batch.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestRunDeferBatch(%s): got status = %v, want %v", test.name, got, test.wantStatus)
		}
	}
}

func TestRunDeferredActions(t *testing.T) {
	t.Parallel()

	// stateTrackingRunner sets action state on completion/failure so the
	// test can assert per-action terminal status in addition to per-batch.
	stateTrackingRunner := func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
		state := action.State.Get()
		if action.Name == "error" {
			state.Status = workflow.Failed
			action.State.Set(state)
			return fmt.Errorf("error")
		}
		state.Status = workflow.Completed
		action.State.Set(state)
		return nil
	}

	tests := []struct {
		name              string
		batches           []*workflow.DeferBatch
		wantTripped       bool
		wantBatchStatuses []workflow.Status
	}{
		{
			name:        "Success: no batches",
			wantTripped: false,
		},
		{
			name: "Success: all succeed",
			batches: []*workflow.DeferBatch{
				newDABatch(workflow.OnSuccess, false, "ok"),
				newDABatch(workflow.OnSuccess, true, "ok"),
			},
			wantTripped:       false,
			wantBatchStatuses: []workflow.Status{workflow.Completed, workflow.Completed},
		},
		{
			name: "Success: FailElement=false batch fails does not trip; other batches complete",
			batches: []*workflow.DeferBatch{
				newDABatch(workflow.OnSuccess, false, "error"),
				newDABatch(workflow.OnSuccess, false, "ok"),
			},
			wantTripped:       false,
			wantBatchStatuses: []workflow.Status{workflow.Failed, workflow.Completed},
		},
		{
			name: "Error: FailElement=true batch fails trips; all batches still reach terminal",
			batches: []*workflow.DeferBatch{
				newDABatch(workflow.OnSuccess, false, "ok"),
				newDABatch(workflow.OnSuccess, true, "error"),
			},
			wantTripped:       true,
			wantBatchStatuses: []workflow.Status{workflow.Completed, workflow.Failed},
		},
		{
			// Batches execute in parallel: a FailElement=true failure must
			// not short-circuit the other batches. All three must reach a
			// terminal status regardless of ordering.
			name: "Error: mixed batches all reach terminal when FailElement trips",
			batches: []*workflow.DeferBatch{
				newDABatch(workflow.OnSuccess, false, "error"),
				newDABatch(workflow.OnSuccess, true, "error"),
				newDABatch(workflow.OnSuccess, false, "ok"),
			},
			wantTripped:       true,
			wantBatchStatuses: []workflow.Status{workflow.Failed, workflow.Failed, workflow.Completed},
		},
	}

	for _, test := range tests {
		s := &States{
			store:            &fakeUpdater{},
			testActionRunner: stateTrackingRunner,
		}
		got := s.runDeferredActions(t.Context(), test.batches)
		if got != test.wantTripped {
			t.Errorf("TestRunDeferredActions(%s): got tripped = %v, want %v", test.name, got, test.wantTripped)
		}
		if len(test.batches) != len(test.wantBatchStatuses) {
			continue
		}
		for i, b := range test.batches {
			if got := b.State.Get().Status; got != test.wantBatchStatuses[i] {
				t.Errorf("TestRunDeferredActions(%s): batch[%d] status = %v, want %v", test.name, i, got, test.wantBatchStatuses[i])
			}
			// Each batch has exactly one action in this fixture; it must match
			// the batch's terminal status (Completed or Failed).
			if len(b.Actions) != 1 {
				continue
			}
			wantAction := test.wantBatchStatuses[i]
			if got := b.Actions[0].State.Get().Status; got != wantAction {
				t.Errorf("TestRunDeferredActions(%s): batch[%d].Actions[0] status = %v, want %v", test.name, i, got, wantAction)
			}
		}
	}
}

func TestExamineDeferredActions(t *testing.T) {
	t.Parallel()

	f := finalStates{}

	tests := []struct {
		name       string
		da         *workflow.DeferredActions
		wantReason workflow.FailureReason
		wantErr    bool
	}{
		{
			name:       "Success: nil DeferredActions is a pass",
			da:         nil,
			wantReason: workflow.FRUnknown,
		},
		{
			name: "Success: NotStarted is a pass",
			da: func() *workflow.DeferredActions {
				d := &workflow.DeferredActions{}
				d.State.Set(workflow.State{Status: workflow.NotStarted})
				return d
			}(),
			wantReason: workflow.FRUnknown,
		},
		{
			name: "Success: Completed is a pass",
			da: func() *workflow.DeferredActions {
				d := &workflow.DeferredActions{}
				d.State.Set(workflow.State{Status: workflow.Completed})
				return d
			}(),
			wantReason: workflow.FRUnknown,
		},
		{
			name: "Error: Failed returns FRDeferredAction",
			da: func() *workflow.DeferredActions {
				d := &workflow.DeferredActions{}
				d.State.Set(workflow.State{Status: workflow.Failed})
				return d
			}(),
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		r, err := f.examineDeferredActions(test.da)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExamineDeferredActions(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestExamineDeferredActions(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		if r != test.wantReason {
			t.Errorf("TestExamineDeferredActions(%s): got reason = %v, want %v", test.name, r, test.wantReason)
		}
	}
}

func TestFinalStatesDeferredActionsFailure(t *testing.T) {
	t.Parallel()

	f := finalStates{}

	plan := &workflow.Plan{}
	plan.State.Set(workflow.State{Status: workflow.Running})
	plan.PreChecks = newChecksWithState(&workflow.State{Status: workflow.Completed})
	plan.ContChecks = newChecksWithState(&workflow.State{Status: workflow.Completed})
	plan.PostChecks = newChecksWithState(&workflow.State{Status: workflow.Completed})
	plan.DeferredChecks = newChecksWithState(&workflow.State{Status: workflow.Completed})
	da := &workflow.DeferredActions{}
	da.State.Set(workflow.State{Status: workflow.Failed})
	plan.DeferredActions = da

	req := f.planChecks(statemachine.Request[Data]{Data: Data{Plan: plan}})
	if req.Err == nil {
		t.Fatalf("TestFinalStatesDeferredActionsFailure: got req.Err == nil, want non-nil")
	}
	if plan.Reason != workflow.FRDeferredAction {
		t.Errorf("TestFinalStatesDeferredActionsFailure: got reason = %v, want %v", plan.Reason, workflow.FRDeferredAction)
	}
	if plan.State.Get().Status != workflow.Failed {
		t.Errorf("TestFinalStatesDeferredActionsFailure: got status = %v, want %v", plan.State.Get().Status, workflow.Failed)
	}
	if methodName(req.Next) != methodName(f.end) {
		t.Errorf("TestFinalStatesDeferredActionsFailure: got next = %v, want finalStates.end", methodName(req.Next))
	}
}

func TestFixDeferBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		batch *workflow.DeferBatch
		want  workflow.Status
	}{
		{
			name: "Success: not running: no change",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.State.Set(workflow.State{Status: workflow.Completed})
				return b
			}(),
			want: workflow.Completed,
		},
		{
			name: "Success: running, all actions NotStarted resets to NotStarted",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.State.Set(workflow.State{Status: workflow.Running})
				a1 := &workflow.Action{}
				a1.State.Set(workflow.State{Status: workflow.NotStarted})
				b.Actions = []*workflow.Action{a1}
				return b
			}(),
			want: workflow.NotStarted,
		},
		{
			name: "Success: running with one action Stopped marks batch Stopped",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.State.Set(workflow.State{Status: workflow.Running})
				a1 := &workflow.Action{}
				a1.State.Set(workflow.State{Status: workflow.Stopped})
				a2 := &workflow.Action{}
				a2.State.Set(workflow.State{Status: workflow.Running})
				b.Actions = []*workflow.Action{a1, a2}
				return b
			}(),
			want: workflow.Stopped,
		},
		{
			name: "Success: running with all actions Completed marks batch Completed",
			batch: func() *workflow.DeferBatch {
				b := &workflow.DeferBatch{}
				b.State.Set(workflow.State{Status: workflow.Running})
				a1 := &workflow.Action{}
				a1.State.Set(workflow.State{Status: workflow.Completed})
				b.Actions = []*workflow.Action{a1}
				return b
			}(),
			want: workflow.Completed,
		},
	}

	for _, test := range tests {
		fixDeferBatch(test.batch)
		if got := test.batch.State.Get().Status; got != test.want {
			t.Errorf("TestFixDeferBatch(%s): got status = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestFixDeferredActions(t *testing.T) {
	t.Parallel()

	runningDA := func(batches ...*workflow.DeferBatch) *workflow.DeferredActions {
		da := &workflow.DeferredActions{DeferredBatches: batches}
		da.State.Set(workflow.State{Status: workflow.Running})
		return da
	}
	terminalBatch := func(status workflow.Status, failElement bool) *workflow.DeferBatch {
		b := &workflow.DeferBatch{When: workflow.OnSuccess, FailElement: failElement}
		b.State.Set(workflow.State{Status: status})
		return b
	}

	tests := []struct {
		name string
		da   *workflow.DeferredActions
		want workflow.Status
	}{
		{
			name: "Success: nil is a no-op",
			da:   nil,
			want: workflow.NotStarted, // unchanged; for nil we just check no panic
		},
		{
			name: "Success: not running: no change",
			da: func() *workflow.DeferredActions {
				d := &workflow.DeferredActions{}
				d.State.Set(workflow.State{Status: workflow.Completed})
				return d
			}(),
			want: workflow.Completed,
		},
		{
			name: "Success: running with all batches Completed marks DA Completed",
			da:   runningDA(terminalBatch(workflow.Completed, false)),
			want: workflow.Completed,
		},
		{
			name: "Success: running with any batch Stopped marks DA Stopped",
			da:   runningDA(terminalBatch(workflow.Stopped, false), terminalBatch(workflow.Completed, false)),
			want: workflow.Stopped,
		},
		{
			name: "Error: running with FailElement=true batch Failed marks DA Failed",
			da:   runningDA(terminalBatch(workflow.Failed, true)),
			want: workflow.Failed,
		},
		{
			name: "Success: running with FailElement=false batch Failed marks DA Completed",
			da:   runningDA(terminalBatch(workflow.Failed, false)),
			want: workflow.Completed,
		},
		{
			name: "Success: running with no batches started resets DA to NotStarted",
			da:   runningDA(terminalBatch(workflow.NotStarted, false)),
			want: workflow.NotStarted,
		},
		{
			name: "Success: mixed Completed batches marks DA Completed",
			da: runningDA(
				terminalBatch(workflow.Completed, true),
				terminalBatch(workflow.Completed, false),
			),
			want: workflow.Completed,
		},
		{
			name: "Error: mixed Completed and FailElement Failed marks DA Failed",
			da: runningDA(
				terminalBatch(workflow.Completed, true),
				terminalBatch(workflow.Failed, true),
			),
			want: workflow.Failed,
		},
	}

	for _, test := range tests {
		s := &States{}
		s.fixDeferredActions(test.da)
		if test.da == nil {
			continue
		}
		if got := test.da.State.Get().Status; got != test.want {
			t.Errorf("TestFixDeferredActions(%s): got status = %v, want %v", test.name, got, test.want)
		}
	}
}
