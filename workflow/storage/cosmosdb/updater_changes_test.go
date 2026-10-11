package cosmosdb

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// storedStates returns the status and attempt count of every object below p, keyed by walk position and type.
func storedStates(p *workflow.Plan) map[string]string {
	out := map[string]string{}
	i := 0
	for item := range walk.Plan(p, walk.WithSkipPlan()) {
		v := item.Value.(walk.Stateful).GetState().Status.String()
		if item.Value.Type() == workflow.OTAction {
			v = fmt.Sprintf("%s attempts=%d", v, len(item.Action().Attempts.Get()))
		}
		out[fmt.Sprintf("%02d %v", i, item.Value.Type())] = v
		i++
	}
	return out
}

func TestUpdateChanges(t *testing.T) {
	t.Parallel()

	done := workflow.State{Status: workflow.Completed, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC()}
	// changed returns the objects the change below modifies.
	changed := func(p *workflow.Plan) []walk.Stateful {
		return []walk.Stateful{
			p.Blocks[0],
			p.Blocks[0].Sequences[0],
			p.Blocks[0].Sequences[0].Actions[0],
			p.DeferredActions.DeferredBatches[0],
		}
	}

	tests := []struct {
		name string
		// noChange leaves the Plan as it was stored.
		noChange bool
		// batchFails makes the transactional batch fail.
		batchFails bool

		wantErr bool
	}{
		{
			name:     "Success: nothing changed writes nothing",
			noChange: true,
		},
		{
			name: "Success: changed objects are patched in one batch and take their new ETags",
		},
		{
			name:       "Error: a failed batch stores none of the changes",
			batchFails: true,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		store := newFakeStorage(testReg)
		mu := &sync.RWMutex{}
		defaultIOpts := &azcosmos.ItemOptions{}
		r := reader{mu: mu, container: "container", client: store, defaultIOpts: defaultIOpts, reg: testReg}
		v := &Vault{
			reader:  r,
			creator: creator{mu: mu, client: store, reader: r},
			updater: newUpdater(mu, store, defaultIOpts),
		}

		plan := NewTestPlan()
		if err := v.Create(t.Context(), plan); err != nil {
			t.Fatalf("TestUpdateChanges(%s): Create: %s", test.name, err)
		}
		stored, err := v.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): Read: %s", test.name, err)
		}
		want := storedStates(stored)
		before := changes.Record(stored)
		if !test.noChange {
			for _, obj := range changed(stored) {
				obj.SetState(done)
			}
			stored.Blocks[0].Sequences[0].Actions[0].Attempts.Append(workflow.Attempt{Start: done.Start, End: done.End})
			if !test.wantErr {
				want = storedStates(stored)
			}
		}
		store.batchPatchErr = test.batchFails

		err = v.UpdateChanges(t.Context(), stored, before)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestUpdateChanges(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestUpdateChanges(%s): got err == %s, want err == nil", test.name, err)
			continue
		}

		reloaded, err := v.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): reload Read: %s", test.name, err)
		}
		if diff := pretty.Compare(want, storedStates(reloaded)); diff != "" {
			t.Errorf("TestUpdateChanges(%s): stored states -want +got:\n%s", test.name, diff)
		}
		if test.noChange || test.wantErr {
			continue
		}
		for _, obj := range changed(stored) {
			id := obj.(stateIDer).GetID()
			if got, want := obj.GetState().ETag, "etag-"+id.String(); got != want {
				t.Errorf("TestUpdateChanges(%s): object(%s) got ETag %q, want %q", test.name, id, got, want)
			}
		}
	}
}

// fakeActionItem returns an Action whose attempts encode to about respBytes.
func fakeActionItem(respBytes int) walk.Item {
	a := &workflow.Action{ID: uuid.New()}
	a.Attempts.Set([]workflow.Attempt{{Resp: strings.Repeat("x", respBytes)}})
	return walk.Item{Value: a}
}

func TestBatchOps(t *testing.T) {
	t.Parallel()

	small := func(n int) []walk.Item {
		items := make([]walk.Item, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, fakeActionItem(10))
		}
		return items
	}
	// Each large patch is a fifth of maxBatchBytes before encoding. Its attempts are base64 encoded twice on the way
	// into the patch, so measure one to know how many fit in a batch.
	large := func(n int) []walk.Item {
		items := make([]walk.Item, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, fakeActionItem(maxBatchBytes/5))
		}
		return items
	}
	one, err := batchOps(large(1))
	if err != nil {
		t.Fatalf("TestBatchOps: sizing a large patch: %s", err)
	}
	perBatch := maxBatchBytes / one[0][0].size
	if perBatch < 1 || perBatch >= 10 {
		t.Fatalf("TestBatchOps: %d large patches fit in a batch, want between 1 and 9 so ten are split by size", perBatch)
	}
	largeBatches := (10 + perBatch - 1) / perBatch

	tests := []struct {
		name        string
		items       []walk.Item
		wantBatches int
		wantErr     bool
	}{
		{
			name:        "Success: a few small patches fit in one batch.",
			items:       small(4),
			wantBatches: 1,
		},
		{
			name:        "Success: more small patches than maxBatchOps are split by count.",
			items:       small(maxBatchOps + 1),
			wantBatches: 2,
		},
		{
			name:        "Success: patches that together pass maxBatchBytes are split by size.",
			items:       large(10),
			wantBatches: largeBatches,
		},
		{
			name:    "Error: an object that has no patch is not batched.",
			items:   []walk.Item{fakeActionItem(10), {Value: &workflow.Plan{}}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		batches, err := batchOps(test.items)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestBatchOps(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestBatchOps(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		if len(batches) != test.wantBatches {
			t.Errorf("TestBatchOps(%s): got %d batches, want %d", test.name, len(batches), test.wantBatches)
		}
		var got []walk.Item
		for i, batch := range batches {
			size := 0
			for _, op := range batch {
				size += op.size
				got = append(got, op.item)
			}
			if len(batch) > maxBatchOps {
				t.Errorf("TestBatchOps(%s): batch %d has %d operations, want at most %d", test.name, i, len(batch), maxBatchOps)
			}
			if size > maxBatchBytes {
				t.Errorf("TestBatchOps(%s): batch %d is %d bytes, want at most %d", test.name, i, size, maxBatchBytes)
			}
		}
		if len(got) != len(test.items) {
			t.Errorf("TestBatchOps(%s): got %d operations, want %d", test.name, len(got), len(test.items))
			continue
		}
		for i := range got {
			if got[i].Value != test.items[i].Value {
				t.Errorf("TestBatchOps(%s): operation %d is out of order", test.name, i)
				break
			}
		}
	}
}
