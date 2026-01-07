package sm

import (
	"context"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/kylelemons/godebug/pretty"
)

var pConfig = pretty.Config{
	PrintStringers: true,
}

func newActionWithStateAndAttempts(state *workflow.State, attempts []workflow.Attempt) *workflow.Action {
	a := &workflow.Action{}
	a.Attempts.Set(attempts)
	a.State.Set(*state)
	return a
}

func newChecksWithStateAndActionsRecov(state *workflow.State, actions []*workflow.Action) *workflow.Checks {
	c := &workflow.Checks{Actions: actions}
	c.State.Set(*state)
	return c
}

func newSequenceWithStateAndActionsRecov(state *workflow.State, actions []*workflow.Action) *workflow.Sequence {
	s := &workflow.Sequence{Actions: actions}
	s.State.Set(*state)
	return s
}

func newBlockWithStateSeqsChecks(state *workflow.State, seqs []*workflow.Sequence, bypass, pre, cont, post *workflow.Checks) *workflow.Block {
	b := &workflow.Block{Sequences: seqs, BypassChecks: bypass, PreChecks: pre, ContChecks: cont, PostChecks: post}
	b.State.Set(*state)
	return b
}

func newPlanWithStateBlocksChecks(state *workflow.State, blocks []*workflow.Block, bypass, pre, cont, post, deferred *workflow.Checks) *workflow.Plan {
	p := &workflow.Plan{Blocks: blocks, BypassChecks: bypass, PreChecks: pre, ContChecks: cont, PostChecks: post, DeferredChecks: deferred}
	p.State.Set(*state)
	return p
}

func TestFixAction(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		action *workflow.Action
		want   *workflow.Action
	}{
		{
			name:   "action not running, no change",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
		},
		{
			name:   "running action, empty attempts, reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now, End: now}, []workflow.Attempt{}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
		},
		{
			name:   "running action with attempts that didn't finish, reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
		},
		{
			name:   "running action with attempts that have been completed, no reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Start: now, End: now.Add(1)}}),
		},
		{
			name:   "running action with attempts that have been failed, no reset",
			action: newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
			want:   newActionWithStateAndAttempts(&workflow.State{Status: workflow.Failed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Err: &plugins.Error{}, Start: now, End: now.Add(1)}}),
		},
	}

	for _, test := range tests {
		fixAction(test.action)
		if diff := pConfig.Compare(test.want, test.action); diff != "" {
			t.Errorf("TestFixAction(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestFixChecks(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		checks *workflow.Checks
		want   *workflow.Checks
	}{
		{
			name:   "nil checks, no panic",
			checks: nil,
			want:   nil,
		},
		{
			name:   "checks not running, no reset",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
		},
		{
			name:   "running checks with completed action, marks as completed",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now, End: now.Add(1)}})}),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed, Start: now, End: now.Add(1)}, []workflow.Attempt{{Start: now, End: now.Add(1)}})}),
		},
		{
			name:   "running checks with incomplete action, resets",
			checks: newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now, End: now}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running, Start: now}, []workflow.Attempt{{Start: now}})}),
			want:   newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, []*workflow.Action{newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil)}),
		},
	}

	for _, test := range tests {
		// Save the original status to detect if fixChecks changed it
		var originalStatus workflow.Status
		if test.checks != nil {
			originalStatus = test.checks.State.Get().Status
		}

		fixChecks(test.checks)

		// For checks that were Running and got marked as Completed by fixChecks, the End time is set to time.Now()
		// We need to exclude it from comparison but verify it was set
		if test.checks != nil && test.want != nil && originalStatus == workflow.Running && test.checks.State.Get().Status == workflow.Completed && test.want.State.Get().Status == workflow.Completed {
			// Save the actual end time
			actualEnd := test.checks.State.Get().End
			// Set both to zero for comparison
			checksState := test.checks.State.Get()
			checksState.End = time.Time{}
			test.checks.State.Set(checksState)
			wantState := test.want.State.Get()
			wantState.End = time.Time{}
			test.want.State.Set(wantState)
			if diff := pConfig.Compare(test.want, test.checks); diff != "" {
				t.Errorf("TestFixChecks(%s): -want/+got):\n%s", test.name, diff)
			}
			// Verify that the End time was actually set (not zero)
			if actualEnd.IsZero() {
				t.Errorf("TestFixChecks(%s): Checks.State.End was not set", test.name)
			}
		} else {
			if diff := pConfig.Compare(test.want, test.checks); diff != "" {
				t.Errorf("TestFixChecks(%s): -want/+got):\n%s", test.name, diff)
			}
		}
	}
}

func TestFixSeq(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		seq  *workflow.Sequence
		want *workflow.Sequence
	}{
		{
			name: "sequence not running, no change",
			seq:  newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, End: now}, nil),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
		},
		{
			name: "running sequence, no actions completed, reset sequence",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
			}),
		},
		{
			name: "running sequence, an action was stopped, sequence is stopped",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped, End: now}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil),
			}),
		},
		{
			name: "running sequence, all actions completed, complete sequence",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
		},
		{
			name: "running sequence, some actions completed, stays running",
			seq: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Running}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
			want: newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running, Start: now}, []*workflow.Action{
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.NotStarted}, nil),
				newActionWithStateAndAttempts(&workflow.State{Status: workflow.Completed}, nil),
			}),
		},
	}

	for _, test := range tests {
		fixSeq(test.seq)
		if test.seq.State.Get().Status == workflow.Stopped {
			if test.seq.State.Get().End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			seqState := test.seq.State.Get()
			seqState.End = time.Time{} // Reset time so we can compare
			test.seq.State.Set(seqState)
			for _, a := range test.seq.Actions {
				if a.State.Get().Status == workflow.Stopped {
					if a.State.Get().End.IsZero() {
						t.Errorf("TestFixSeq(%s): got action.End == 0, want non-zero", test.name)
					}
					aState := a.State.Get()
					aState.End = time.Time{} // Reset time so we can compare
					a.State.Set(aState)
				}
			}
		}
		if test.seq.State.Get().Status == workflow.Completed {
			if test.seq.State.Get().End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			seqState := test.seq.State.Get()
			seqState.End = time.Time{} // Reset time so we can compare
			test.seq.State.Set(seqState)
		}
		if diff := pConfig.Compare(test.want, test.seq); diff != "" {
			t.Errorf("TestFixSeq(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestFixBlock(t *testing.T) {
	tests := []struct {
		name string
		b    *workflow.Block
		want *workflow.Block
	}{
		{
			name: "block not running, no change",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
		},
		{
			name: "running block, prechecks failed, block fails",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil),
		},
		{
			name: "running block, contChecks failed, block fails",
			b:    newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil),
		},
		{
			name: "running block, postChecks failed, block fails",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil)),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil)),
		},
		{
			name: "running block, all sequences completed, no postchecks, leaves running",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, nil),
		},
		{
			name: "running block, all sequences completed, postcheck completed, leaves running",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
		},
		{
			name: "running block, one sequence stopped, block stops",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, nil),
			}, nil, nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Stopped}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Stopped}, nil),
			}, nil, nil, nil, nil),
		},
		{
			name: "running block, bypass checks completed, block state completed",
			b: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, nil),
			}, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil),
			want: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, []*workflow.Sequence{
				newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.NotStarted}, nil),
			}, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil),
		},
	}

	for _, test := range tests {
		reg := registry.New()
		vault, err := sqlite.New(context.Background(), "", reg, sqlite.WithInMemory())
		if err != nil {
			t.Fatalf("TestFixBlock(%s): failed to create vault: %v", test.name, err)
		}
		(&States{store: vault}).fixBlock(test.b)
		if test.want.State.Get().Status != test.b.State.Get().Status {
			t.Errorf("TestFixBlock(%s): got state %v, want %v", test.name, test.b.State.Get().Status, test.want.State.Get().Status)
		}
	}
}

func TestFixPlan(t *testing.T) {
	tests := []struct {
		name string
		plan *workflow.Plan
		want *workflow.Plan
	}{
		{
			name: "plan not running, no change",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil, nil),
		},
		{
			name: "running plan, bypass checks completed, plan completes",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), nil, nil, nil, nil),
		},
		{
			name: "running plan, prechecks failed, plan fails",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Failed}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), nil, nil, nil),
		},
		{
			name: "running plan, all blocks completed, postchecks/contchecks completed, deferred checks completed, plan completes",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Completed}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil)),
		},
		{
			name: "running plan, block failed, plan fails",
			plan: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, nil, nil),
			want: newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Failed}, []*workflow.Block{
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Failed}, nil, nil, nil, nil, nil),
				newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil),
			}, nil, nil, nil, nil, nil),
		},
	}

	for _, test := range tests {
		reg := registry.New()
		vault, err := sqlite.New(context.Background(), "", reg, sqlite.WithInMemory())
		if err != nil {
			t.Fatalf("TestFixPlan(%s): failed to create vault: %v", test.name, err)
		}
		(&States{store: vault}).fixPlan(test.plan)
		if test.plan.State.Get().Status != test.want.State.Get().Status {
			t.Errorf("TestFixPlan(%s): got status %v, want %v", test.name, test.plan.State.Get().Status, test.want.State.Get().Status)
		}
	}
}

func TestChecksFailed(t *testing.T) {
	tests := []struct {
		name string
		c    *workflow.Checks
		want bool
	}{
		{"nil checks", nil, false},
		{"checks failed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), true},
		{"checks completed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), false},
	}
	for _, test := range tests {
		if got := checksFailed(test.c); got != test.want {
			t.Errorf("TestChecksFailed(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestChecksCompleted(t *testing.T) {
	tests := []struct {
		name string
		c    *workflow.Checks
		want bool
	}{
		{"nil checks", nil, true},
		{"checks completed", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Completed}, nil), true},
		{"checks running", newChecksWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, nil), false},
	}
	for _, test := range tests {
		if got := checksCompleted(test.c); got != test.want {
			t.Errorf("TestChecksCompleted(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestSkipBlock(t *testing.T) {
	tests := []struct {
		name string
		b    block
		want bool
	}{
		{"completed block", block{block: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil)}, true},
		{"running block", block{block: newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil)}, false},
	}
	for _, test := range tests {
		if got := skipBlock(test.b); got != test.want {
			t.Errorf("TestSkipBlock(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestIsCompleted(t *testing.T) {
	tests := []struct {
		name string
		o    workflow.Object
		want bool
	}{
		{"completed object", newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Completed}, nil, nil, nil, nil, nil), true},
		{"running object", newPlanWithStateBlocksChecks(&workflow.State{Status: workflow.Running}, nil, nil, nil, nil, nil, nil), false},
		{"failed object", newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Failed}, nil), true},
		{"stopped object", newActionWithStateAndAttempts(&workflow.State{Status: workflow.Stopped}, nil), true},
	}
	for _, test := range tests {
		if got := isCompleted(test.o); got != test.want {
			t.Errorf("TestIsCompleted(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}
