package cosmosdb

import (
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/element-of-surprise/coercion/workflow"
)

// TestDeferredActionsRoundTrip drives the cosmosdb Create -> Read -> Update
// path for a plan's DeferredActions. Prior to this test the cosmosdb backend
// had no direct coverage for creator_deferredactions.go /
// reader_deferredactions.go / updater_deferredactions.go — TestStorageItemCRUD
// exercised the whole plan via prettyConfig.Compare, but made no explicit
// assertions on DeferredActions shape or on the update path.
func TestDeferredActionsRoundTrip(t *testing.T) {
	ctx := context.Background()

	plan := NewTestPlan()
	if plan.DeferredActions == nil {
		t.Fatalf("TestDeferredActionsRoundTrip: fixture has no DeferredActions")
	}
	if len(plan.DeferredActions.OnFailure) == 0 || len(plan.DeferredActions.OnSuccess) == 0 {
		t.Fatalf("TestDeferredActionsRoundTrip: fixture DeferredActions missing batches")
	}

	store := newFakeStorage(testReg)

	mu := &sync.RWMutex{}
	container := "container"
	defaultIOpts := &azcosmos.ItemOptions{}
	r := reader{
		mu:           mu,
		container:    container,
		client:       store,
		defaultIOpts: defaultIOpts,
		reg:          testReg,
	}
	v := &Vault{
		reader: r,
		creator: creator{
			mu:     mu,
			client: store,
			reader: r,
		},
		updater: newUpdater(mu, store, defaultIOpts),
		deleter: deleter{
			mu:     mu,
			client: store,
			reader: r,
		},
	}

	if err := v.Create(ctx, plan); err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: Create: %s", err)
	}

	// 1. Read the plan back and confirm DeferredActions + batches round-tripped.
	stored, err := v.Read(ctx, plan.ID)
	if err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: Read: %s", err)
	}
	if stored.DeferredActions == nil {
		t.Fatalf("TestDeferredActionsRoundTrip: stored.DeferredActions is nil")
	}
	if got, want := stored.DeferredActions.ID, plan.DeferredActions.ID; got != want {
		t.Errorf("TestDeferredActionsRoundTrip: DA ID = %v, want %v", got, want)
	}
	if got, want := len(stored.DeferredActions.OnFailure), len(plan.DeferredActions.OnFailure); got != want {
		t.Fatalf("TestDeferredActionsRoundTrip: OnFailure batch count = %d, want %d", got, want)
	}
	if got, want := len(stored.DeferredActions.OnSuccess), len(plan.DeferredActions.OnSuccess); got != want {
		t.Fatalf("TestDeferredActionsRoundTrip: OnSuccess batch count = %d, want %d", got, want)
	}
	failBatch := stored.DeferredActions.OnFailure[0]
	successBatch := stored.DeferredActions.OnSuccess[0]
	if !failBatch.FailElement {
		t.Errorf("TestDeferredActionsRoundTrip: OnFailure[0].FailElement = false, want true")
	}
	if successBatch.FailElement {
		t.Errorf("TestDeferredActionsRoundTrip: OnSuccess[0].FailElement = true, want false")
	}
	if got, want := failBatch.Name, "fail-batch"; got != want {
		t.Errorf("TestDeferredActionsRoundTrip: OnFailure[0].Name = %q, want %q", got, want)
	}
	if got, want := successBatch.Name, "success-batch"; got != want {
		t.Errorf("TestDeferredActionsRoundTrip: OnSuccess[0].Name = %q, want %q", got, want)
	}
	if len(failBatch.Actions) == 0 {
		t.Errorf("TestDeferredActionsRoundTrip: OnFailure[0].Actions is empty")
	}
	if len(successBatch.Actions) == 0 {
		t.Errorf("TestDeferredActionsRoundTrip: OnSuccess[0].Actions is empty")
	}

	// 2. Update DeferredActions state + one DeferBatch state via the updater,
	//    go through UpdateObject to also exercise the type-switch wiring.
	newDAState := workflow.State{
		Status: workflow.Failed,
		Start:  time.Unix(1000, 0).UTC(),
		End:    time.Unix(2000, 0).UTC(),
	}
	stored.DeferredActions.State.Set(newDAState)
	if err := v.UpdateObject(ctx, stored.DeferredActions); err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: UpdateObject(DeferredActions): %s", err)
	}

	newBatchState := workflow.State{
		Status: workflow.Completed,
		Start:  time.Unix(3000, 0).UTC(),
		End:    time.Unix(4000, 0).UTC(),
	}
	failBatch.State.Set(newBatchState)
	if err := v.UpdateObject(ctx, failBatch); err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: UpdateObject(DeferBatch): %s", err)
	}

	// 3. Re-read the plan and verify both updates persisted; the untouched
	//    success batch must still be in its original state.
	reloaded, err := v.Read(ctx, plan.ID)
	if err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: reload Read: %s", err)
	}
	gotDA := reloaded.DeferredActions.State.Get()
	if gotDA.Status != newDAState.Status {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DA status = %v, want %v", gotDA.Status, newDAState.Status)
	}
	if !gotDA.Start.Equal(newDAState.Start) || !gotDA.End.Equal(newDAState.End) {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DA times = (%v, %v), want (%v, %v)",
			gotDA.Start, gotDA.End, newDAState.Start, newDAState.End)
	}
	gotBatch := reloaded.DeferredActions.OnFailure[0].State.Get()
	if gotBatch.Status != newBatchState.Status {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded OnFailure[0] status = %v, want %v", gotBatch.Status, newBatchState.Status)
	}
	if !gotBatch.Start.Equal(newBatchState.Start) || !gotBatch.End.Equal(newBatchState.End) {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded OnFailure[0] times = (%v, %v), want (%v, %v)",
			gotBatch.Start, gotBatch.End, newBatchState.Start, newBatchState.End)
	}
	// The untouched OnSuccess batch must not have been clobbered by either
	// the DeferredActions-level or OnFailure-level update.
	gotSuccessStatus := reloaded.DeferredActions.OnSuccess[0].State.Get().Status
	origSuccessStatus := plan.DeferredActions.OnSuccess[0].State.Get().Status
	if gotSuccessStatus != origSuccessStatus {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded OnSuccess[0] status = %v, want %v (original)", gotSuccessStatus, origSuccessStatus)
	}
}
