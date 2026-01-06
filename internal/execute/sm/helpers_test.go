package sm

import (
	"testing"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/kylelemons/godebug/pretty"
)

func TestRunPreChecks(t *testing.T) {
	t.Parallel()

	actionSuccess := &workflow.Action{Name: "success"}
	actionError := &workflow.Action{Name: "error"}

	tests := []struct {
		name       string
		preChecks  *workflow.Checks
		contChecks *workflow.Checks
		wantErr    bool
	}{
		{
			name: "Success: No prechecks or contchecks",
		},
		{
			name:       "Error: PreChecks fail but ContChecks succeed",
			preChecks:  &workflow.Checks{Actions: []*workflow.Action{actionError}},
			contChecks: &workflow.Checks{Actions: []*workflow.Action{actionSuccess}},
			wantErr:    true,
		},
		{
			name:       "Error: PreChecks succeed but ContChecks fail",
			preChecks:  &workflow.Checks{Actions: []*workflow.Action{actionSuccess}},
			contChecks: &workflow.Checks{Actions: []*workflow.Action{actionError}},
			wantErr:    true,
		},
		{
			name:       "Error: PreChecks fail and ContChecks fail",
			preChecks:  &workflow.Checks{Actions: []*workflow.Action{actionError}},
			contChecks: &workflow.Checks{Actions: []*workflow.Action{actionError}},
			wantErr:    true,
		},
		{
			name:       "Success: PreChecks succeed and ContChecks succeed",
			preChecks:  &workflow.Checks{Actions: []*workflow.Action{actionSuccess}},
			contChecks: &workflow.Checks{Actions: []*workflow.Action{actionSuccess}},
		},
	}

	for _, test := range tests {
		states := &States{
			testChecksRunner: fakeRunChecksOnce,
		}

		err := states.runPreChecks(context.Background(), test.preChecks, test.contChecks)
		if (err != nil) != test.wantErr {
			t.Errorf("TestRunPreChecks(%s): err == %v, want err == %v", test.name, err, test.wantErr)
		}
	}
}

func newActionWithState(name string, state *workflow.State) *workflow.Action {
	a := &workflow.Action{Name: name}
	a.State.Set(*state)
	return a
}

func newChecksWithStateAndActions(actions []*workflow.Action, state *workflow.State) *workflow.Checks {
	c := &workflow.Checks{Actions: actions}
	c.State.Set(*state)
	return c
}

func TestRunChecksOnce(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name       string
		checks     *workflow.Checks
		wantChecks *workflow.Checks
		wantErr    bool
	}{
		{
			name: "Error: runActionsParallel returns error",
			checks: &workflow.Checks{
				Actions: []*workflow.Action{
					{Name: "error"},
				},
			},
			wantChecks: newChecksWithStateAndActions([]*workflow.Action{newActionWithState("error", &workflow.State{})}, &workflow.State{Status: workflow.Failed, Start: now, End: now}),
			wantErr:    true,
		},
		{
			name: "Success",
			checks: &workflow.Checks{
				Actions: []*workflow.Action{
					{Name: "action1"},
				},
			},
			wantChecks: newChecksWithStateAndActions([]*workflow.Action{newActionWithState("action1", &workflow.State{})}, &workflow.State{Status: workflow.Completed, Start: now, End: now}),
		},
	}

	for _, test := range tests {
		updater := &fakeUpdater{}
		states := &States{
			store:                     updater,
			testActionsParallelRunner: fakeParallelActionRunner,
			nower:                     func() time.Time { return now },
		}
		test.checks.State.Set(workflow.State{})
		for _, action := range test.checks.Actions {
			action.State.Set(workflow.State{})
		}

		err := states.runChecksOnce(context.Background(), test.checks)
		if diff := pretty.Compare(test.wantChecks, test.checks); diff != "" {
			t.Errorf("TestRunChecksOnce(%s): checks not correct: -want/+got:\n%s", test.name, diff)
		}
		if test.wantErr != (err != nil) {
			t.Errorf("TestRunChecksOnce(%s): got err == %v, want err == %v", test.name, err, test.wantErr)
		}
		if updater.calls.Load() != 2 {
			t.Errorf("TestRunChecksOnce(%s): updater got %d calls, want 2", test.name, updater.calls.Load())
		}
	}
}

func TestParallelActionsRunner(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	tests := []struct {
		name        string
		actions     []*workflow.Action
		wantActions []*workflow.Action
		err         bool
	}{
		{
			name: "One action fails",
			actions: []*workflow.Action{
				{Name: "action1"},
				{Name: "error"},
			},
			// Note: Even though this is a failed action, we are faking the action runner
			// so we just need to make sure the action was marked Running.
			wantActions: []*workflow.Action{
				newActionWithState("action1", &workflow.State{Status: workflow.Running, Start: now}),
				newActionWithState("error", &workflow.State{Status: workflow.Running, Start: now}),
			},
			err: true,
		},
		{
			name: "All actions pass",
			actions: []*workflow.Action{
				{Name: "action1"},
				{Name: "action2"},
			},
			wantActions: []*workflow.Action{
				newActionWithState("action1", &workflow.State{Status: workflow.Running, Start: now}),
				newActionWithState("action2", &workflow.State{Status: workflow.Running, Start: now}),
			},
		},
	}

	for _, test := range tests {
		updater := &fakeUpdater{}
		states := &States{
			store:            updater,
			testActionRunner: fakeActionRunner,
			nower:            func() time.Time { return now },
		}
		for _, action := range test.actions {
			action.State.Set(workflow.State{})
		}

		err := states.runActionsParallel(context.Background(), test.actions)

		if diff := pretty.Compare(test.wantActions, test.actions); diff != "" {
			t.Errorf("TestRunChecks(%s): actions differ (-want +got):\n%s", test.name, diff)
		}

		if test.err && err == nil {
			t.Errorf("TestRunChecks(%s): expected error, got nil", test.name)
		}
	}
}

func newSequenceWithState(name string, actions []*workflow.Action, state *workflow.State) *workflow.Sequence {
	s := &workflow.Sequence{Name: name, Actions: actions}
	s.State.Set(*state)
	return s
}

// TestRunActions tests the runActions function. Since this is wrapper around
// the actions state machine, we only need to test it runs, we don't need to
// do indepth testing.
func TestExecSeq(t *testing.T) {
	t.Parallel()

	start := time.Now().UTC()
	end := start.Add(time.Second)

	tests := []struct {
		name      string
		seq       *workflow.Sequence
		wantSeq   *workflow.Sequence
		dbUpdates []*workflow.Sequence
		wantErr   bool
	}{
		{
			name:    "action failed, so seq failed",
			seq:     newSequenceWithState("seq", []*workflow.Action{{Name: "action"}, {Name: "error"}}, &workflow.State{}),
			wantSeq: newSequenceWithState("seq", []*workflow.Action{{Name: "action"}, {Name: "error"}}, &workflow.State{Status: workflow.Failed, Start: start, End: end}),
			dbUpdates: []*workflow.Sequence{
				newSequenceWithState("seq", []*workflow.Action{{Name: "action"}, {Name: "error"}}, &workflow.State{Status: workflow.Running, Start: start}),
				newSequenceWithState("seq", []*workflow.Action{{Name: "action"}, {Name: "error"}}, &workflow.State{Status: workflow.Failed, Start: start, End: end}),
			},
			wantErr: true,
		},
		{
			name:    "seq completed",
			seq:     newSequenceWithState("seq", []*workflow.Action{{Name: "action1"}, {Name: "action2"}}, &workflow.State{}),
			wantSeq: newSequenceWithState("seq", []*workflow.Action{{Name: "action1"}, {Name: "action2"}}, &workflow.State{Status: workflow.Completed, Start: start, End: end}),
			dbUpdates: []*workflow.Sequence{
				newSequenceWithState("seq", []*workflow.Action{{Name: "action1"}, {Name: "action2"}}, &workflow.State{Status: workflow.Running, Start: start}),
				newSequenceWithState("seq", []*workflow.Action{{Name: "action1"}, {Name: "action2"}}, &workflow.State{Status: workflow.Completed, Start: start, End: end}),
			},
		},
	}

	for _, test := range tests {
		updater := &fakeUpdater{}
		callNum := 0
		states := &States{
			store:            updater,
			testActionRunner: fakeActionRunner,
			nower: func() time.Time {
				defer func() { callNum++ }()
				if callNum == 0 {
					return start
				}
				return end
			},
		}

		err := states.execSeq(context.Background(), test.seq)

		if diff := pretty.Compare(test.wantSeq, test.seq); diff != "" {
			t.Errorf("TestExecSeq(%s): expected Sequence: -want/+got:\n%s", test.name, diff)
		}
		if diff := pretty.Compare(test.dbUpdates, updater.seqs); diff != "" {
			t.Errorf("TestExecSeq(%s): expected dbUpdates: -want/+got:\n%s", test.name, diff)
		}
		if (err != nil) != test.wantErr {
			t.Errorf("TestExecSeq(%s): expected error: %v, got: %v", test.name, test.wantErr, err)
		}
	}
}

func TestResetActions(t *testing.T) {
	t.Parallel()

	action := &workflow.Action{
		Name: "action",
	}
	action.Attempts.Set([]workflow.Attempt{{}})
	action.State.Set(workflow.State{Status: workflow.Running, Start: time.Now(), End: time.Now()})

	want := &workflow.Action{
		Name: "action",
	}
	want.State.Set(workflow.State{Status: workflow.NotStarted, Start: time.Time{}, End: time.Time{}})

	resetActions([]*workflow.Action{action})

	if diff := pretty.Compare(want, action); diff != "" {
		t.Errorf("TestResetActions: -want +got):\n%s", diff)
	}
}
