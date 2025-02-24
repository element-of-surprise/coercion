package cosmosdb

import (
	"context"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestStorageItemCRUD(t *testing.T) {
	ctx := context.Background()

	plan0 := NewTestPlan()
	plan1 := NewTestPlan()
	plan2 := NewTestPlan()
	plans := []*workflow.Plan{plan0, plan1, plan2}

	store := newFakeStorage(testReg)

	mu := &sync.RWMutex{}
	container := "container"
	defaultIOpts := &azcosmos.ItemOptions{}
	reader := reader{
		mu:           mu,
		container:    container,
		client:       store,
		defaultIOpts: defaultIOpts,
		reg:          testReg,
	}

	newUpdater(mu, store, defaultIOpts)
	v := &Vault{
		reader: reader,
		creator: creator{
			mu:     mu,
			client: store,
			reader: reader,
		},
		updater: newUpdater(mu, store, defaultIOpts),
		deleter: deleter{
			mu:     mu,
			client: store,
			reader: reader,
		},
	}

	for _, p := range plans {
		if err := v.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	for i, p := range plans {
		gotPlan, err := v.fetchPlan(ctx, p.GetID())
		if err != nil {
			panic(err)
		}
		if diff := prettyConfig.Compare(p, gotPlan); diff != "" {
			t.Fatalf("TestStorageItemCRUD(Create(%d): -want/+got:\n%s", i, diff)
		}
	}

	for i, p := range plans {
		exists, err := v.reader.Exists(ctx, p.ID)
		if err != nil {
			t.Fatalf("TestStorageItemCRUD(Exists(%d)): error checking if plan exists: %v", i, err)
		}
		if !exists {
			t.Fatalf("TestStorageItemCRUD(Exists(%d)): expected plan to exist", i)
		}
	}

	for _, p := range plans {
		result, err := v.Read(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != result.Name {
			t.Fatalf("expected %s, got %s", p.Name, result.Name)
		}
		if p.State.Status != result.State.Status {
			t.Fatalf("expected %s, got %s", p.State.Status, result.State.Status)
		}

		if diff := prettyConfig.Compare(p, result); diff != "" {
			t.Errorf("TestStorageItemCRUD(%s): returned plan: -want/+got:\n%s", p.ID, diff)
		}
	}

	results, err := v.List(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var resultCount int
	for res := range results {
		if res.Err != nil {
			t.Fatalf("error when listing results: %v", res.Err)
		}
		resultCount++
	}
	if resultCount != 3 {
		t.Fatalf("expected 3 result, got %d", resultCount)
	}

	// The fake pager only implements querying by ID. It's a pain to fake too much.
	filters := storage.Filters{
		ByIDs: []uuid.UUID{
			plan0.ID,
			plan1.ID,
		},
	}
	results, err = v.Search(ctx, filters)
	if err != nil {
		t.Fatal(err)
	}
	resultCount = 0
	for res := range results {
		if res.Err != nil {
			t.Fatalf("error when listing results: %v", res.Err)
		}
		resultCount++
	}
	if resultCount != 2 {
		t.Fatalf("expected 2 result, got %d", resultCount)
	}

	// Walk every item and change the state.Status to Stopped.
	// We can then update all the objects and then test that the updates occurred.
	for item := range walk.Plan(context.Background(), plan0) {
		if so, ok := item.Value.(stateObject); ok {
			state := so.GetState()
			state.Status = workflow.Stopped
			so.SetState(state)
			if err := v.UpdateObject(ctx, item.Value); err != nil {
				panic(err)
			}
		}
	}

	got, err := v.Read(ctx, plan0.GetID())
	if err != nil {
		t.Fatalf("TestStorageItemCRUD(read back changed object): error reading plan back")
	}
	if diff := prettyConfig.Compare(plan0, got); diff != "" {
		t.Fatalf("TestStorageItemCRUD(read back changed object): -want/+got:\n%s", diff)
	}

	// test delete
	if err := v.Delete(ctx, plan0.ID); err != nil {
		t.Fatal(err)
	}

	for item := range walk.Plan(context.Background(), plan0) {
		id := item.Value.(getIDer).GetID()
		if _, err := v.Read(ctx, id); err == nil {
			t.Errorf("TestStorageItemCRUD(delete): %s value in deleted plan still in storage", item.Value.Type())
		}
	}
}
