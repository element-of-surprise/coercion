package cosmosdb

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestStorageItemCRUD(t *testing.T) {
	ctx := context.Background()

	cName := "test"

	reg := registry.New()
	reg.MustRegister(&plugins.CheckPlugin{})
	reg.MustRegister(&plugins.HelloPlugin{})

	cc, err := NewFakeCosmosDBClient()
	if err != nil {
		t.Fatal(err)
	}
	mu := &sync.Mutex{}
	r := Vault{
		dbName:       "test-db",
		cName:        "test-container",
		partitionKey: "test-partition",
	}
	r.reader = reader{cName: cName, Client: cc, reg: reg}
	r.creator = creator{mu: mu, Client: cc, reader: r.reader}
	r.updater = newUpdater(mu, cc, r.reader)
	r.closer = closer{Client: cc}
	r.deleter = deleter{mu: mu, Client: cc, reader: r.reader}

	// use test plan
	if err := r.Create(ctx, plan); err != nil {
		t.Fatal(err)
	}

	mustGetCreateCallCount(t, cc.createCallCount, Plan, 1)
	mustGetCreateCallCount(t, cc.createCallCount, Checks, 10)
	mustGetCreateCallCount(t, cc.createCallCount, Block, 1)
	mustGetCreateCallCount(t, cc.createCallCount, Sequence, 1)
	mustGetCreateCallCount(t, cc.createCallCount, Action, 11)

	exists, err := r.reader.Exists(ctx, plan.ID)
	if err != nil {
		t.Fatalf("error checking if plan %s exists: %v", plan.ID, err)
	}
	if !exists {
		t.Fatalf("expected plan %s to exist", plan.ID)
	}

	result, err := r.Read(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != result.Name {
		t.Fatalf("expected %s, got %s", plan.Name, result.Name)
	}
	if plan.State.Status != result.State.Status {
		t.Fatalf("expected %s, got %s", plan.State.Status, result.State.Status)
	}
	// creator will set to zero time
	plan.SubmitTime = zeroTime
	if diff := pretty.Compare(plan, result); diff != "" {
		t.Errorf("TestStorageItemCRUD(%s): returned plan: -want/+got:\n%s", plan.ID, diff)
	}

	// not going to bother with testing search here, since I would need to fake that in the fake pager.
	results, err := r.List(ctx, 0)
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
	if resultCount != 1 {
		t.Fatalf("expected 1 result, got %d", resultCount)
	}

	// plan.State.Status = workflow.Completed
	// test update, which is actually a patch and faking too much is a pain
	if err := r.UpdatePlan(ctx, plan); err != nil {
		t.Fatalf("error updating plan: %v", err)
	}
	if v, ok := cc.client.patchCallCount[Plan]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch, got %d", v)
	}
	if v, ok := cc.client.patchCallCount[Block]; ok && v != 0 {
		t.Fatalf("expected 0 calls to patch block, got %d", v)
	}

	// test walk, to check that every type is found? checks, blocks, etc.
	var block *workflow.Block
	var checks *workflow.Checks
	var action *workflow.Action
	var sequence *workflow.Sequence
	idsFound := make(map[workflow.ObjectType]map[uuid.UUID]int)
	for item := range walk.Plan(ctx, result) {
		if _, ok := idsFound[item.Value.Type()]; !ok {
			idsFound[item.Value.Type()] = make(map[uuid.UUID]int)
		}
		switch item.Value.Type() {
		case workflow.OTPlan:
			p := item.Plan()
			idsFound[workflow.OTPlan][p.ID]++
		case workflow.OTBlock:
			block = item.Block()
			idsFound[workflow.OTBlock][block.ID]++
		case workflow.OTCheck:
			checks = item.Checks()
			idsFound[workflow.OTCheck][checks.ID]++
		case workflow.OTAction:
			action = item.Action()
			idsFound[workflow.OTAction][action.ID]++
		case workflow.OTSequence:
			sequence = item.Sequence()
			idsFound[workflow.OTSequence][sequence.ID]++
		default:
			t.Fatalf("unexpected type: %s", item.Value.Type())
		}
	}

	// update block
	if err := r.UpdateBlock(ctx, block); err != nil {
		t.Fatalf("error updating block: %v", err)
	}
	if v, ok := cc.client.patchCallCount[Plan]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch, got %d", v)
	}
	if v, ok := cc.client.patchCallCount[Block]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch block, got %d", v)
	}

	// update checks
	if err := r.UpdateChecks(ctx, checks); err != nil {
		t.Fatalf("error updating checks: %v", err)
	}
	if v, ok := cc.client.patchCallCount[Plan]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch, got %d", v)
	}
	if v, ok := cc.client.patchCallCount[Checks]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch check, got %d", v)
	}

	// update sequence
	if err := r.UpdateSequence(ctx, sequence); err != nil {
		t.Fatalf("error updating sequence: %v", err)
	}
	if v, ok := cc.client.patchCallCount[Plan]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch, got %d", v)
	}
	if v, ok := cc.client.patchCallCount[Sequence]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch sequence, got %d", v)
	}

	// update action
	if err := r.UpdateAction(ctx, action); err != nil {
		t.Fatalf("error updating action: %v", err)
	}
	if v, ok := cc.client.patchCallCount[Plan]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch, got %d", v)
	}
	if v, ok := cc.client.patchCallCount[Action]; !ok || v != 1 {
		t.Fatalf("expected 1 call to patch action, got %d", v)
	}

	if len(idsFound[workflow.OTBlock]) != 1 {
		t.Fatalf("expected 1 block when reading plan, got %d", len(idsFound[workflow.OTBlock]))
	}
	if len(idsFound[workflow.OTCheck]) != 10 {
		t.Fatalf("expected 10 checks when reading plan, got %d", len(idsFound[workflow.OTCheck]))
	}
	if len(idsFound[workflow.OTSequence]) != 1 {
		t.Fatalf("expected 1 sequence when reading plan, got %d", len(idsFound[workflow.OTSequence]))
	}
	if len(idsFound[workflow.OTAction]) != 11 {
		t.Fatalf("expected 11 actions when reading plan, got %d", len(idsFound[workflow.OTAction]))
	}

	// test delete
	if err := r.Delete(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	result, err = r.Read(ctx, plan.ID)
	if err == nil {
		t.Fatalf("expected error when reading deleted plan %s", plan.ID.String())
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not found error when reading deleted plan %s, got %v", plan.ID.String(), err)
	}
	mustGetDeleteCallCount(t, cc.deleteCallCount, Plan, 1)
	mustGetDeleteCallCount(t, cc.deleteCallCount, Checks, 10)
	mustGetDeleteCallCount(t, cc.deleteCallCount, Block, 1)
	mustGetDeleteCallCount(t, cc.deleteCallCount, Sequence, 1)
	mustGetDeleteCallCount(t, cc.deleteCallCount, Action, 11)
	// test with multiple plans?
}

func mustGetCreateCallCount(t *testing.T, m map[Type]int, dt Type, val int) {
	if v, ok := m[dt]; !ok || v != val {
		t.Fatalf("expected %d calls to create %s, got %d", val, dt.String(), v)
	}
}

func mustGetDeleteCallCount(t *testing.T, m map[Type]int, dt Type, val int) {
	if v, ok := m[dt]; !ok || v != val {
		t.Fatalf("expected %d calls to delete %s, got %d", val, dt.String(), v)
	}
}
