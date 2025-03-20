package sm

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/kylelemons/godebug/pretty"
)

var pConfig = pretty.Config{
	PrintStringers: true,
}

func TestFixAction(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		action *workflow.Action
		want   *workflow.Action
	}{
		{
			name: "action not running, no change",
			action: &workflow.Action{
				State: &workflow.State{Status: workflow.Completed},
				Attempts: []*workflow.Attempt{
					{Start: now, End: now.Add(1)},
				},
			},
			want: &workflow.Action{
				State: &workflow.State{Status: workflow.Completed},
				Attempts: []*workflow.Attempt{
					{Start: now, End: now.Add(1)},
				},
			},
		},
		{
			name: "running action, empty attempts, reset",
			action: &workflow.Action{
				State:    &workflow.State{Status: workflow.Running, Start: now, End: now},
				Attempts: []*workflow.Attempt{},
			},
			want: &workflow.Action{
				State:    &workflow.State{Status: workflow.NotStarted},
				Attempts: nil,
			},
		},
		{
			name: "running action with attempts, no reset",
			action: &workflow.Action{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Attempts: []*workflow.Attempt{
					{Start: now, End: now.Add(1)},
				},
			},
			want: &workflow.Action{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Attempts: []*workflow.Attempt{
					{Start: now, End: now.Add(1)},
				},
			},
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
			name: "checks not running, no reset",
			checks: &workflow.Checks{
				State: &workflow.State{Status: workflow.Completed},
			},
			want: &workflow.Checks{
				State: &workflow.State{Status: workflow.Completed},
			},
		},
		{
			name: "running checks, reset",
			checks: &workflow.Checks{
				State: &workflow.State{Status: workflow.Running, Start: now, End: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Running, Start: now}, Attempts: []*workflow.Attempt{{Start: now, End: now.Add(1)}}},
				},
			},
			want: &workflow.Checks{
				State: &workflow.State{Status: workflow.NotStarted},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.NotStarted}, Attempts: nil},
				},
			},
		},
	}

	for _, test := range tests {
		fixChecks(test.checks)
		if diff := pConfig.Compare(test.want, test.checks); diff != "" {
			t.Errorf("TestFixChecks(%s): -want/+got):\n%s", test.name, diff)
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
			seq: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Completed, End: now},
			},
			want: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Completed},
			},
		},
		{
			name: "running sequence, no actions completed, reset sequence",
			seq: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Running}},
					{State: &workflow.State{Status: workflow.Running}},
				},
			},
			want: &workflow.Sequence{
				State: &workflow.State{Status: workflow.NotStarted},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.NotStarted}},
					{State: &workflow.State{Status: workflow.NotStarted}},
				},
			},
		},
		{
			name: "running sequence, an action was stopped, sequence is stopped",
			seq: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Stopped, End: now}},
					{State: &workflow.State{Status: workflow.Running}},
				},
			},
			want: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Stopped, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Stopped}},
					{State: &workflow.State{Status: workflow.Stopped}},
				},
			},
		},
		{
			name: "running sequence, all actions completed, complete sequence",
			seq: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Completed, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
		{
			name: "running sequence, some actions completed, stays running",
			seq: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Running}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Sequence{
				State: &workflow.State{Status: workflow.Running, Start: now},
				Actions: []*workflow.Action{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.NotStarted}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
	}

	for _, test := range tests {
		fixSeq(test.seq)
		if test.seq.State.Status == workflow.Stopped {
			if test.seq.State.End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			test.seq.State.End = time.Time{} // Reset time so we can compare
			for _, a := range test.seq.Actions {
				if a.State.Status == workflow.Stopped {
					if a.State.End.IsZero() {
						t.Errorf("TestFixSeq(%s): got action.End == 0, want non-zero", test.name)
					}
					a.State.End = time.Time{} // Reset time so we can compare
				}
			}
		}
		if test.seq.State.Status == workflow.Completed {
			if test.seq.State.End.IsZero() {
				t.Errorf("TestFixSeq(%s): got seq.State.End == 0, want non-zero", test.name)
			}
			test.seq.State.End = time.Time{} // Reset time so we can compare
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
			b: &workflow.Block{
				State: &workflow.State{Status: workflow.Completed},
			},
			want: &workflow.Block{
				State: &workflow.State{Status: workflow.Completed},
			},
		},
		{
			name: "running block, prechecks failed, block fails",
			b: &workflow.Block{
				State:     &workflow.State{Status: workflow.Running},
				PreChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
			},
			want: &workflow.Block{
				State:     &workflow.State{Status: workflow.Failed},
				PreChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
			},
		},
		{
			name: "running block, contChecks failed, block fails",
			b: &workflow.Block{
				State:      &workflow.State{Status: workflow.Running},
				PreChecks:  &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				ContChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
			},
			want: &workflow.Block{
				State:      &workflow.State{Status: workflow.Failed},
				PreChecks:  &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				ContChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
			},
		},
		{
			name: "running block, postChecks failed, block fails",
			b: &workflow.Block{
				State:      &workflow.State{Status: workflow.Failed},
				PreChecks:  &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				ContChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				PostChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Block{
				State:      &workflow.State{Status: workflow.Failed},
				PreChecks:  &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				ContChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				PostChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Failed}},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
		{
			name: "running block, all sequences completed, no postchecks, completes block",
			b: &workflow.Block{
				State: &workflow.State{Status: workflow.Running},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Block{
				State: &workflow.State{Status: workflow.Completed},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
		{
			name: "running block, all sequences completed, postcheck completed, completes block",
			b: &workflow.Block{
				State:      &workflow.State{Status: workflow.Running},
				PostChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Block{
				State:      &workflow.State{Status: workflow.Completed},
				PostChecks: &workflow.Checks{State: &workflow.State{Status: workflow.Completed}},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
		{
			name: "running block, one sequence stopped, block stops",
			b: &workflow.Block{
				State: &workflow.State{Status: workflow.Running},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Stopped}},
					{State: &workflow.State{Status: workflow.Running}},
				},
			},
			want: &workflow.Block{
				State: &workflow.State{Status: workflow.Stopped},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.Stopped}},
					{State: &workflow.State{Status: workflow.Stopped}},
				},
			},
		},
		{
			name: "running block, bypass checks completed, block state completed",
			b: &workflow.Block{
				State: &workflow.State{Status: workflow.Running},
				BypassChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.NotStarted}},
				},
			},
			want: &workflow.Block{
				State: &workflow.State{Status: workflow.Completed},
				BypassChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
				Sequences: []*workflow.Sequence{
					{State: &workflow.State{Status: workflow.NotStarted}},
				},
			},
		},
	}

	for _, test := range tests {
		fixBlock(test.b)
		if test.want.State.Status != test.b.State.Status {
			t.Errorf("TestFixBlock(%s): got state %v, want %v", test.name, test.b.State.Status, test.want.State.Status)
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
			plan: &workflow.Plan{
				State: &workflow.State{Status: workflow.Completed},
			},
			want: &workflow.Plan{
				State: &workflow.State{Status: workflow.Completed},
			},
		},
		{
			name: "running plan, bypass checks completed, plan completes",
			plan: &workflow.Plan{
				State: &workflow.State{Status: workflow.Running},
				BypassChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
			},
			want: &workflow.Plan{
				State: &workflow.State{Status: workflow.Completed},
				BypassChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
			},
		},
		{
			name: "running plan, prechecks failed, plan fails",
			plan: &workflow.Plan{
				State: &workflow.State{Status: workflow.Running},
				PreChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Failed},
				},
			},
			want: &workflow.Plan{
				State: &workflow.State{Status: workflow.Failed},
				PreChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Failed},
				},
			},
		},
		{
			name: "running plan, all blocks completed, postchecks/contchecks completed, deferred checks completed, plan completes",
			plan: &workflow.Plan{
				State: &workflow.State{Status: workflow.Running},
				Blocks: []*workflow.Block{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
				PostChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
				DeferredChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
			},
			want: &workflow.Plan{
				State: &workflow.State{Status: workflow.Completed},
				Blocks: []*workflow.Block{
					{State: &workflow.State{Status: workflow.Completed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
				PostChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
				ContChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
				DeferredChecks: &workflow.Checks{
					State: &workflow.State{Status: workflow.Completed},
				},
			},
		},
		{
			name: "running plan, block failed, plan fails",
			plan: &workflow.Plan{
				State: &workflow.State{Status: workflow.Running},
				Blocks: []*workflow.Block{
					{State: &workflow.State{Status: workflow.Failed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
			want: &workflow.Plan{
				State: &workflow.State{Status: workflow.Failed},
				Blocks: []*workflow.Block{
					{State: &workflow.State{Status: workflow.Failed}},
					{State: &workflow.State{Status: workflow.Completed}},
				},
			},
		},
	}

	for _, test := range tests {
		fixPlan(test.plan)
		if test.plan.State.Status != test.want.State.Status {
			t.Errorf("TestFixPlan(%s): got status %v, want %v", test.name, test.plan.State.Status, test.want.State.Status)
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
		{"checks failed", &workflow.Checks{State: &workflow.State{Status: workflow.Failed}}, true},
		{"checks completed", &workflow.Checks{State: &workflow.State{Status: workflow.Completed}}, false},
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
		{"checks completed", &workflow.Checks{State: &workflow.State{Status: workflow.Completed}}, true},
		{"checks running", &workflow.Checks{State: &workflow.State{Status: workflow.Running}}, false},
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
		{"completed block", block{block: &workflow.Block{State: &workflow.State{Status: workflow.Completed}}}, true},
		{"running block", block{block: &workflow.Block{State: &workflow.State{Status: workflow.Running}}}, false},
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
		{"completed object", &workflow.Block{State: &workflow.State{Status: workflow.Completed}}, true},
		{"running object", &workflow.Plan{State: &workflow.State{Status: workflow.Running}}, false},
		{"failed object", &workflow.Sequence{State: &workflow.State{Status: workflow.Failed}}, true},
		{"stopped object", &workflow.Action{State: &workflow.State{Status: workflow.Stopped}}, true},
	}
	for _, test := range tests {
		if got := isCompleted(test.o); got != test.want {
			t.Errorf("TestIsCompleted(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}
