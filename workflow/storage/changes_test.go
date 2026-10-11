package storage

import (
	"slices"
	"testing"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
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

// fakeStore is an Updater that records the objects written to it, in order. The first write of an object of
// type failType fails and is not recorded.
type fakeStore struct {
	Updater

	failType workflow.ObjectType
	failErr  error
	writes   []workflow.Object
}

func (f *fakeStore) write(o workflow.Object) error {
	if o.Type() == f.failType {
		f.failType = workflow.OTUnknown
		if f.failErr != nil {
			return f.failErr
		}
		return errors.New("storage busy")
	}
	f.writes = append(f.writes, o)
	return nil
}

func (f *fakeStore) UpdateChecks(ctx context.Context, c *workflow.Checks) error { return f.write(c) }

func (f *fakeStore) UpdateBlock(ctx context.Context, b *workflow.Block) error { return f.write(b) }

func (f *fakeStore) UpdateSequence(ctx context.Context, s *workflow.Sequence) error {
	return f.write(s)
}

func (f *fakeStore) UpdateAction(ctx context.Context, a *workflow.Action) error { return f.write(a) }

func (f *fakeStore) UpdateDeferredActions(ctx context.Context, d *workflow.DeferredActions) error {
	return f.write(d)
}

func (f *fakeStore) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	return f.write(b)
}

// TestWriteObject checks that WriteObject sends each object to its matching updater, returns updater failures
// unchanged, and reports unsupported object types as internal bugs.
func TestWriteObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		item     walk.Item
		failType workflow.ObjectType
		wantErr  error
		wantBug  bool
	}{
		{
			name: "Success: Checks",
			item: walk.Item{Value: &workflow.Checks{}},
		},
		{
			name: "Success: Block",
			item: walk.Item{Value: &workflow.Block{}},
		},
		{
			name: "Success: Sequence",
			item: walk.Item{Value: &workflow.Sequence{}},
		},
		{
			name: "Success: Action",
			item: walk.Item{Value: &workflow.Action{}},
		},
		{
			name: "Success: DeferredActions",
			item: walk.Item{Value: &workflow.DeferredActions{}},
		},
		{
			name: "Success: DeferBatch",
			item: walk.Item{Value: &workflow.DeferBatch{}},
		},
		{
			name:     "Error: updater failure is returned unchanged",
			item:     walk.Item{Value: &workflow.Action{}},
			failType: workflow.OTAction,
			wantErr:  errors.New("storage busy"),
		},
		{
			name:    "Error: Plan is not a writable child object",
			item:    walk.Item{Value: &workflow.Plan{}},
			wantBug: true,
		},
		{
			name:    "Error: zero Item is not writable",
			wantBug: true,
		},
	}

	for _, test := range tests {
		store := &fakeStore{failType: test.failType, failErr: test.wantErr}
		err := WriteObject(t.Context(), store, test.item)

		switch {
		case test.wantErr != nil:
			if err != test.wantErr {
				t.Errorf("TestWriteObject(%s): got err == %v, want the updater's error %v", test.name, err, test.wantErr)
			}
		case test.wantBug:
			var got errors.Error
			if !errors.As(err, &got) || got.Category != errors.CatInternal || got.Type != errors.TypeBug {
				t.Errorf("TestWriteObject(%s): got err == %v, want an internal bug error", test.name, err)
			}
		case err != nil:
			t.Errorf("TestWriteObject(%s): got err == %v, want nil", test.name, err)
		}

		wantWrites := 1
		if test.wantErr != nil || test.wantBug {
			wantWrites = 0
		}
		if len(store.writes) != wantWrites {
			t.Errorf("TestWriteObject(%s): got %d writes, want %d", test.name, len(store.writes), wantWrites)
			continue
		}
		if wantWrites == 1 && store.writes[0] != test.item.Value {
			t.Errorf("TestWriteObject(%s): got write %v %p, want %v %p", test.name, store.writes[0].Type(), store.writes[0], test.item.Value.Type(), test.item.Value)
		}
	}
}

// TestWriteChanges checks that WriteChanges stores the changed objects with every object after all of its descendants.
// Recovery skips an object whose stored state is already final, so a parent stored as final over children that are not
// would never be fixed by a retry.
func TestWriteChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// noBefore uses an empty Snapshot instead of recording the Plan before change runs.
		noBefore bool
		// change modifies the Plan in place after the before Snapshot is taken.
		change func(p *workflow.Plan)
		// failType makes the first write of an object of this type fail.
		failType workflow.ObjectType
		// want returns the objects of p that are stored, in write order.
		want    func(p *workflow.Plan) []workflow.Object
		wantErr bool
	}{
		{
			name:   "Success: nothing changed writes nothing",
			change: func(p *workflow.Plan) {},
			want:   func(p *workflow.Plan) []workflow.Object { return nil },
		},
		{
			name:   "Success: changed objects are written with every object after its descendants",
			change: changeSeveral,
			want: func(p *workflow.Plan) []workflow.Object {
				return []workflow.Object{
					p.DeferredActions.DeferredBatches[0].Actions[0],
					p.Blocks[0].Sequences[0].Actions[1],
					p.Blocks[0].Sequences[0],
					p.Blocks[0],
				}
			},
		},
		{
			name:     "Success: with an empty before every object below the Plan is written, each after its descendants",
			noBefore: true,
			change:   func(p *workflow.Plan) {},
			want: func(p *workflow.Plan) []workflow.Object {
				objs := walkOrder(p)
				slices.Reverse(objs)
				return objs
			},
		},
		{
			name:     "Error: a failed write stops before the object's ancestors are written",
			noBefore: true,
			change:   func(p *workflow.Plan) {},
			failType: workflow.OTBatch,
			want: func(p *workflow.Plan) []workflow.Object {
				return []workflow.Object{p.DeferredActions.DeferredBatches[0].Actions[0]}
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		p := newPlan()
		before := changes.Snapshot{}
		if !test.noBefore {
			before = changes.Record(p)
		}
		test.change(p)

		store := &fakeStore{failType: test.failType}
		err := WriteChanges(t.Context(), store, p, before)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestWriteChanges(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestWriteChanges(%s): got err == %s, want err == nil", test.name, err)
		}

		// A failed write must still have stored every object before it, and nothing after it.
		want := test.want(p)
		if len(store.writes) != len(want) {
			t.Errorf("TestWriteChanges(%s): got %d objects written, want %d", test.name, len(store.writes), len(want))
			continue
		}
		for i := range want {
			if store.writes[i] != want[i] {
				t.Errorf("TestWriteChanges(%s): write %d: got %v %p, want %v %p", test.name, i, store.writes[i].Type(), store.writes[i], want[i].Type(), want[i])
			}
		}
	}
}
