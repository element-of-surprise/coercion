package changes

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/kylelemons/godebug/pretty"
)

var (
	start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	notStarted = workflow.State{Status: workflow.NotStarted}
	running    = workflow.State{Status: workflow.Running, Start: start}
	completed  = workflow.State{Status: workflow.Completed, Start: start, End: start.Add(time.Minute)}
)

// newPlan returns a Plan with one of each kind of object below it. Its walk order (skipping the Plan) is:
// PreChecks, PreChecks.Actions[0], Blocks[0], Sequences[0], Sequences[0].Actions[0], Sequences[0].Actions[1],
// DeferredActions, DeferredBatches[0], DeferredBatches[0].Actions[0].
func newPlan() *workflow.Plan {
	p := &workflow.Plan{
		ID: workflow.NewV7(),
		PreChecks: &workflow.Checks{
			ID:      workflow.NewV7(),
			Actions: []*workflow.Action{{ID: workflow.NewV7()}},
		},
		Blocks: []*workflow.Block{
			{
				ID: workflow.NewV7(),
				Sequences: []*workflow.Sequence{
					{
						ID:      workflow.NewV7(),
						Actions: []*workflow.Action{{ID: workflow.NewV7()}, {ID: workflow.NewV7()}},
					},
				},
			},
		},
		DeferredActions: &workflow.DeferredActions{
			ID: workflow.NewV7(),
			DeferredBatches: []*workflow.DeferBatch{
				{Sequence: workflow.Sequence{ID: workflow.NewV7(), Actions: []*workflow.Action{{ID: workflow.NewV7()}}}},
			},
		},
	}
	p.State.Set(running)
	p.PreChecks.State.Set(completed)
	p.PreChecks.Actions[0].State.Set(completed)
	p.PreChecks.Actions[0].Attempts.Set([]workflow.Attempt{{Start: start, End: start.Add(time.Second)}})
	p.Blocks[0].State.Set(running)
	p.Blocks[0].Sequences[0].State.Set(running)
	p.Blocks[0].Sequences[0].Actions[0].State.Set(completed)
	p.Blocks[0].Sequences[0].Actions[0].Attempts.Set([]workflow.Attempt{{Start: start}, {Start: start}})
	p.Blocks[0].Sequences[0].Actions[1].State.Set(running)
	p.DeferredActions.State.Set(notStarted)
	p.DeferredActions.DeferredBatches[0].State.Set(notStarted)
	p.DeferredActions.DeferredBatches[0].Actions[0].State.Set(notStarted)
	return p
}

func TestRecord(t *testing.T) {
	t.Parallel()

	got := Record(newPlan())

	want := Snapshot{
		{State: completed},              // PreChecks
		{State: completed, Attempts: 1}, // PreChecks.Actions[0]
		{State: running},                // Blocks[0]
		{State: running},                // Sequences[0]
		{State: completed, Attempts: 2}, // Sequences[0].Actions[0]
		{State: running},                // Sequences[0].Actions[1]
		{State: notStarted},             // DeferredActions
		{State: notStarted},             // DeferredBatches[0]
		{State: notStarted},             // DeferredBatches[0].Actions[0]
	}

	if diff := pretty.Compare(want, got); diff != "" {
		t.Errorf("TestRecord: -want, +got:\n%s", diff)
	}
}

func TestSince(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// noBefore uses an empty Snapshot instead of recording the Plan before change runs.
		noBefore bool
		// change modifies the Plan in place after the before Snapshot is taken.
		change func(p *workflow.Plan)
		// want returns the objects of p that Since should return, in order.
		want func(p *workflow.Plan) []workflow.Object
	}{
		{
			name:   "Success: nothing changed returns nothing",
			change: func(p *workflow.Plan) {},
			want:   func(p *workflow.Plan) []workflow.Object { return nil },
		},
		{
			name: "Success: a change to the Plan itself is not returned",
			change: func(p *workflow.Plan) {
				p.State.Set(completed)
			},
			want: func(p *workflow.Plan) []workflow.Object { return nil },
		},
		{
			name: "Success: an Action whose status changed is returned",
			change: func(p *workflow.Plan) {
				p.Blocks[0].Sequences[0].Actions[1].State.Set(completed)
			},
			want: func(p *workflow.Plan) []workflow.Object {
				return []workflow.Object{p.Blocks[0].Sequences[0].Actions[1]}
			},
		},
		{
			name: "Success: an Action with a new attempt but the same state is returned",
			change: func(p *workflow.Plan) {
				p.Blocks[0].Sequences[0].Actions[1].Attempts.Append(workflow.Attempt{Start: start})
			},
			want: func(p *workflow.Plan) []workflow.Object {
				return []workflow.Object{p.Blocks[0].Sequences[0].Actions[1]}
			},
		},
		{
			name:   "Success: changed objects are returned in walk order, every object before its descendants",
			change: changeSeveral,
			want: func(p *workflow.Plan) []workflow.Object {
				return []workflow.Object{
					p.Blocks[0],
					p.Blocks[0].Sequences[0],
					p.Blocks[0].Sequences[0].Actions[1],
					p.DeferredActions.DeferredBatches[0].Actions[0],
				}
			},
		},
		{
			name:     "Success: with an empty before every object below the Plan is returned in walk order",
			noBefore: true,
			change:   func(p *workflow.Plan) {},
			want: func(p *workflow.Plan) []workflow.Object {
				return walkOrder(p)
			},
		},
	}

	for _, test := range tests {
		p := newPlan()
		before := Snapshot{}
		if !test.noBefore {
			before = Record(p)
		}
		test.change(p)

		var got []workflow.Object
		for _, item := range Since(p, before) {
			got = append(got, item.Value)
		}
		want := test.want(p)

		if len(got) != len(want) {
			t.Errorf("TestSince(%s): got %d objects, want %d", test.name, len(got), len(want))
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("TestSince(%s): object %d: got %v %p, want %v %p", test.name, i, got[i].Type(), got[i], want[i].Type(), want[i])
			}
		}
	}
}

// changeSeveral changes a Block, its Sequence, one of that Sequence's Actions and a deferred Action.
func changeSeveral(p *workflow.Plan) {
	p.Blocks[0].State.Set(completed)
	p.Blocks[0].Sequences[0].State.Set(completed)
	p.Blocks[0].Sequences[0].Actions[1].State.Set(completed)
	p.DeferredActions.DeferredBatches[0].Actions[0].State.Set(completed)
}

// walkOrder returns every object below p (see newPlan) in walk order.
func walkOrder(p *workflow.Plan) []workflow.Object {
	return []workflow.Object{
		p.PreChecks,
		p.PreChecks.Actions[0],
		p.Blocks[0],
		p.Blocks[0].Sequences[0],
		p.Blocks[0].Sequences[0].Actions[0],
		p.Blocks[0].Sequences[0].Actions[1],
		p.DeferredActions,
		p.DeferredActions.DeferredBatches[0],
		p.DeferredActions.DeferredBatches[0].Actions[0],
	}
}
