package azblob

import (
	"github.com/kylelemons/godebug/pretty"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/gostdlib/base/concurrency/sync"
)

// TestDeferredActionsRoundTrip creates a plan with DeferredActions (via the
// shared createAndUploadTestPlan helper in deleter_test.go, which drives the
// real uploadSubObjects path), reads the DeferredActions back through the
// reader, mutates both the DeferredActions and a DeferBatch state, re-reads
// them, and verifies the updates persisted.
//
// This exercises uploader_deferredactions.go, reader_deferredactions.go, and
// updater_deferredactions.go end-to-end against the fake blob client — the
// only code paths in the azblob backend that had zero direct test coverage
// for the deferred actions feature.
func TestDeferredActionsRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fakeClient := blobops.NewFake()
	prefix := "test"

	plan := createAndUploadTestPlan(ctx, t, fakeClient, prefix, true)
	containerName := containerForPlan(prefix, plan.ID)

	if plan.DeferredActions == nil {
		t.Fatalf("TestDeferredActionsRoundTrip: fixture plan has no DeferredActions")
	}

	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	planMu := planlocks.New(ctx)
	r := reader{
		mu:            planMu,
		readFlight:    &sync.Flight[string, *workflow.Plan]{},
		existsFlight:  &sync.Flight[string, bool]{},
		pools:         newFetchPools(ctx),
		prefix:        prefix,
		client:        fakeClient,
		reg:           reg,
		retentionDays: 30,
	}

	// 1. Fetch DeferredActions directly and verify shape.
	got, err := r.fetchDeferredActions(ctx, containerName, plan.ID, plan.DeferredActions.ID)
	if err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: fetchDeferredActions: %s", err)
	}
	if got == nil {
		t.Fatalf("TestDeferredActionsRoundTrip: fetchDeferredActions returned nil")
	}
	if got.ID != plan.DeferredActions.ID {
		t.Errorf("TestDeferredActionsRoundTrip: DA ID = %v, want %v", got.ID, plan.DeferredActions.ID)
	}
	if got.GetPlanID() != plan.ID {
		t.Errorf("TestDeferredActionsRoundTrip: DA planID = %v, want %v", got.GetPlanID(), plan.ID)
	}
	// batch is the part of a DeferBatch this round trip must keep.
	type batch struct {
		Name        string
		When        workflow.WhenDeferred
		FailElement bool
		Actions     int
	}
	want := []batch{
		{Name: "fail-batch", When: workflow.OnFailure, FailElement: true, Actions: 1},
		{Name: "success-batch", When: workflow.OnSuccess, Actions: 1},
	}
	var gotBatches []batch
	for _, b := range got.DeferredBatches {
		gotBatches = append(gotBatches, batch{Name: b.Name, When: b.When, FailElement: b.FailElement, Actions: len(b.Actions)})
	}
	if diff := pretty.Compare(want, gotBatches); diff != "" {
		t.Fatalf("TestDeferredActionsRoundTrip: DeferredBatches mismatch, -want/+got:\n%s", diff)
	}
	failBatch := got.DeferredBatches[0]

	// 2. Mutate DeferredActions state and one DeferBatch state via the updaters.
	daU := deferredActionsUpdater{mu: planMu, prefix: prefix, client: fakeClient}
	bU := deferBatchUpdater{mu: planMu, prefix: prefix, client: fakeClient}

	newDAState := workflow.State{
		Status: workflow.Failed,
		Start:  time.Unix(1000, 0).UTC(),
		End:    time.Unix(2000, 0).UTC(),
	}
	got.State.Set(newDAState)
	if err := daU.UpdateDeferredActions(ctx, got); err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: UpdateDeferredActions: %s", err)
	}

	newBatchState := workflow.State{
		Status: workflow.Completed,
		Start:  time.Unix(3000, 0).UTC(),
		End:    time.Unix(4000, 0).UTC(),
	}
	failBatch.State.Set(newBatchState)
	if err := bU.UpdateDeferBatch(ctx, failBatch); err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: UpdateDeferBatch: %s", err)
	}

	// 3. Re-fetch and verify state persisted on both levels, and that the
	// untouched success batch's state did not move.
	reloaded, err := r.fetchDeferredActions(ctx, containerName, plan.ID, plan.DeferredActions.ID)
	if err != nil {
		t.Fatalf("TestDeferredActionsRoundTrip: reload fetchDeferredActions: %s", err)
	}
	gotDA := reloaded.State.Get()
	if gotDA.Status != newDAState.Status {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DA status = %v, want %v", gotDA.Status, newDAState.Status)
	}
	if !gotDA.Start.Equal(newDAState.Start) || !gotDA.End.Equal(newDAState.End) {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DA times = (%v, %v), want (%v, %v)",
			gotDA.Start, gotDA.End, newDAState.Start, newDAState.End)
	}
	gotBatch := reloaded.DeferredBatches[0].State.Get()
	if gotBatch.Status != newBatchState.Status {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DeferredBatches[0] status = %v, want %v", gotBatch.Status, newBatchState.Status)
	}
	if !gotBatch.Start.Equal(newBatchState.Start) || !gotBatch.End.Equal(newBatchState.End) {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DeferredBatches[0] times = (%v, %v), want (%v, %v)",
			gotBatch.Start, gotBatch.End, newBatchState.Start, newBatchState.End)
	}
	// The untouched success batch must still be NotStarted — proves the
	// DeferredActions-level update did not clobber sibling batches.
	if gotSuccess := reloaded.DeferredBatches[1].State.Get(); gotSuccess.Status != workflow.NotStarted {
		t.Errorf("TestDeferredActionsRoundTrip: reloaded DeferredBatches[1] status = %v, want NotStarted", gotSuccess.Status)
	}
}

// TestDeferredActionsFetchMissing verifies that fetching a DeferredActions
// blob that does not exist returns a NotFound error (not a silent nil).
func TestDeferredActionsFetchMissing(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fakeClient := blobops.NewFake()
	prefix := "test"

	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	r := reader{
		mu:            planlocks.New(ctx),
		readFlight:    &sync.Flight[string, *workflow.Plan]{},
		existsFlight:  &sync.Flight[string, bool]{},
		pools:         newFetchPools(ctx),
		prefix:        prefix,
		client:        fakeClient,
		reg:           reg,
		retentionDays: 30,
	}

	planID := uuid.Must(uuid.NewV7())
	daID := uuid.Must(uuid.NewV7())
	containerName := containerForPlan(prefix, planID)
	if err := fakeClient.EnsureContainer(ctx, containerName); err != nil {
		t.Fatalf("TestDeferredActionsFetchMissing: EnsureContainer: %s", err)
	}

	if _, err := r.fetchDeferredActions(ctx, containerName, planID, daID); err == nil {
		t.Fatalf("TestDeferredActionsFetchMissing: got err == nil, want NotFound")
	}
}
