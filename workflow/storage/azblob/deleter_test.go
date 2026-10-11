package azblob

import (
	"github.com/gostdlib/base/values/chans"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
)

// setupDeleterTest creates a test environment with fake client and deleter struct
func setupDeleterTest(t *testing.T) (*blobops.Fake, deleter) {
	t.Helper()

	fakeClient := blobops.NewFake()
	del := deleter{
		mu:     planlocks.New(t.Context()),
		prefix: "test",
		client: fakeClient,
	}

	return fakeClient, del
}

// createAndUploadTestPlan creates a plan and uploads all its blobs to the fake client
func createAndUploadTestPlan(ctx context.Context, t *testing.T, fakeClient *blobops.Fake, prefix string, withBlocks bool) *workflow.Plan {
	t.Helper()

	planID := workflow.NewV7()

	preCheckAction := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    "pre-check action",
		Descr:   "pre-check action desc",
		Plugin:  testPlugins.HelloPluginName,
		Timeout: 30 * time.Second,
		Req:     testPlugins.HelloReq{Say: "hello"},
	}
	preCheckAction.State.Set(workflow.State{Status: workflow.NotStarted})

	preChecks := &workflow.Checks{
		ID:      workflow.NewV7(),
		Actions: []*workflow.Action{preCheckAction},
	}
	preChecks.State.Set(workflow.State{Status: workflow.NotStarted})

	// DeferredActions with one OnFailure (FailElement=true) and one OnSuccess batch,
	// so TestDelete can verify deleteDeferredActionsBlobs cleans them up.
	onFailureAction := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    "on-failure action",
		Descr:   "on-failure action desc",
		Plugin:  testPlugins.HelloPluginName,
		Timeout: 30 * time.Second,
		Req:     testPlugins.HelloReq{Say: "fail"},
	}
	onFailureAction.State.Set(workflow.State{Status: workflow.NotStarted})

	onSuccessAction := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    "on-success action",
		Descr:   "on-success action desc",
		Plugin:  testPlugins.HelloPluginName,
		Timeout: 30 * time.Second,
		Req:     testPlugins.HelloReq{Say: "success"},
	}
	onSuccessAction.State.Set(workflow.State{Status: workflow.NotStarted})

	onFailureBatch := &workflow.DeferBatch{When: workflow.OnFailure, FailElement: true}
	onFailureBatch.ID = workflow.NewV7()
	onFailureBatch.Name = "fail-batch"
	onFailureBatch.Descr = "fail-batch"
	onFailureBatch.Actions = []*workflow.Action{onFailureAction}
	onFailureBatch.State.Set(workflow.State{Status: workflow.NotStarted})

	onSuccessBatch := &workflow.DeferBatch{When: workflow.OnSuccess}
	onSuccessBatch.ID = workflow.NewV7()
	onSuccessBatch.Name = "success-batch"
	onSuccessBatch.Descr = "success-batch"
	onSuccessBatch.Actions = []*workflow.Action{onSuccessAction}
	onSuccessBatch.State.Set(workflow.State{Status: workflow.NotStarted})

	deferredActions := &workflow.DeferredActions{
		ID:              workflow.NewV7(),
		DeferredBatches: []*workflow.DeferBatch{onFailureBatch, onSuccessBatch},
	}
	deferredActions.State.Set(workflow.State{Status: workflow.NotStarted})

	plan := &workflow.Plan{
		ID:              planID,
		Name:            "Test Plan for Deletion",
		Descr:           "Test Plan Description",
		SubmitTime:      time.Now().UTC(),
		PreChecks:       preChecks,
		DeferredActions: deferredActions,
	}
	plan.State.Set(workflow.State{Status: workflow.NotStarted})

	if withBlocks {
		blockCheckAction := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "block check action",
			Descr:   "block check action desc",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: "block check"},
		}
		blockCheckAction.State.Set(workflow.State{Status: workflow.NotStarted})

		blockPreChecks := &workflow.Checks{
			ID:      workflow.NewV7(),
			Actions: []*workflow.Action{blockCheckAction},
		}
		blockPreChecks.State.Set(workflow.State{Status: workflow.NotStarted})

		seqAction := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "sequence action",
			Descr:   "sequence action desc",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: "sequence"},
		}
		seqAction.State.Set(workflow.State{Status: workflow.NotStarted})

		seq := &workflow.Sequence{
			ID:      workflow.NewV7(),
			Name:    "Test Sequence",
			Descr:   "Test Sequence Description",
			Actions: []*workflow.Action{seqAction},
		}
		seq.State.Set(workflow.State{Status: workflow.NotStarted})

		block := &workflow.Block{
			ID:        workflow.NewV7(),
			Name:      "Test Block",
			Descr:     "Test Block Description",
			PreChecks: blockPreChecks,
			Sequences: []*workflow.Sequence{seq},
		}
		block.State.Set(workflow.State{Status: workflow.NotStarted})

		plan.Blocks = []*workflow.Block{block}
	}

	// Upload plan and all sub-objects to fake client
	containerName := containerForPlan(prefix, plan.ID)

	// Create container
	if err := fakeClient.EnsureContainer(ctx, containerName); err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Upload plan entry blob with metadata
	md, err := planToMetadata(ctx, plan)
	if err != nil {
		t.Fatalf("failed to create metadata: %v", err)
	}
	md[mdPlanType] = toPtr(ptEntry)

	planEntry, err := planToPlanEntry(plan)
	if err != nil {
		t.Fatalf("failed to create plan entry: %v", err)
	}

	planEntryData, err := json.Marshal(planEntry)
	if err != nil {
		t.Fatalf("failed to marshal plan entry: %v", err)
	}

	entryBlobName := planEntryBlobName(plan.ID)
	if err := fakeClient.UploadBlob(ctx, containerName, entryBlobName, md, planEntryData); err != nil {
		t.Fatalf("failed to upload plan entry: %v", err)
	}

	// Upload plan object blob
	md[mdPlanType] = toPtr(ptObject)
	planData, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("failed to marshal plan: %v", err)
	}

	objectBlobName := planObjectBlobName(plan.ID)
	if err := fakeClient.UploadBlob(ctx, containerName, objectBlobName, md, planData); err != nil {
		t.Fatalf("failed to upload plan object: %v", err)
	}

	// Upload all sub-objects
	uploader := &uploader{
		client:      fakeClient,
		prefix:      prefix,
		planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
		blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
		leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
	}

	if err := uploader.uploadSubObjects(ctx, containerName, plan); err != nil {
		t.Fatalf("failed to upload sub-objects: %v", err)
	}

	return plan
}

func TestDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withBlocks bool
		wantErr    bool
	}{
		{
			name:       "Success: delete plan with blocks and sequences",
			withBlocks: true,
			wantErr:    false,
		},
		{
			name:       "Success: delete plan without blocks",
			withBlocks: false,
			wantErr:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload test plan
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", test.withBlocks)
			containerName := containerForPlan("test", plan.ID)

			// Verify blobs exist before deletion
			if !fakeClient.BlobExists(containerName, planEntryBlobName(plan.ID)) {
				t.Fatalf("TestDelete(%s): plan entry blob should exist before deletion", test.name)
			}

			// Delete the plan
			var err error
			done := make(chan struct{})
			context.Pool(ctx).Submit(ctx, func() {
				defer close(done)
				err = del.Delete(ctx, plan.ID)
			})
			wctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			// done is only closed, so anything but a close means Delete did not return in time.
			if _, r := chans.Get(wctx, done); !r.Closed() {
				t.Fatalf("TestDelete(%s): Delete did not return", test.name)
			}

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDelete(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDelete(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify plan entry blob is deleted
			if fakeClient.BlobExists(containerName, planEntryBlobName(plan.ID)) {
				t.Errorf("TestDelete(%s): plan entry blob should be deleted", test.name)
			}

			// Verify plan object blob is deleted
			if fakeClient.BlobExists(containerName, planObjectBlobName(plan.ID)) {
				t.Errorf("TestDelete(%s): plan object blob should be deleted", test.name)
			}

			// Verify checks blobs are deleted
			if plan.PreChecks != nil {
				if fakeClient.BlobExists(containerName, checksBlobName(plan.ID, plan.PreChecks.ID)) {
					t.Errorf("TestDelete(%s): PreChecks blob should be deleted", test.name)
				}
				for _, action := range plan.PreChecks.Actions {
					if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
						t.Errorf("TestDelete(%s): PreChecks action blob should be deleted", test.name)
					}
				}
			}

			// Verify DeferredActions blobs are deleted.
			if plan.DeferredActions != nil {
				if fakeClient.BlobExists(containerName, deferredActionsBlobName(plan.ID, plan.DeferredActions.ID)) {
					t.Errorf("TestDelete(%s): DeferredActions blob should be deleted", test.name)
				}
				for _, batch := range plan.DeferredActions.DeferredBatches {
					if fakeClient.BlobExists(containerName, deferBatchBlobName(plan.ID, batch.ID)) {
						t.Errorf("TestDelete(%s): DeferBatch blob should be deleted", test.name)
					}
					for _, action := range batch.Actions {
						if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
							t.Errorf("TestDelete(%s): DeferBatch action blob should be deleted", test.name)
						}
					}
				}
			}

			// Verify block blobs are deleted if plan had blocks
			if test.withBlocks && len(plan.Blocks) > 0 {
				for _, block := range plan.Blocks {
					if fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
						t.Errorf("TestDelete(%s): block blob should be deleted", test.name)
					}

					// Verify block's checks are deleted
					if block.PreChecks != nil {
						if fakeClient.BlobExists(containerName, checksBlobName(plan.ID, block.PreChecks.ID)) {
							t.Errorf("TestDelete(%s): block PreChecks blob should be deleted", test.name)
						}
					}

					// Verify sequences are deleted
					for _, seq := range block.Sequences {
						if fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
							t.Errorf("TestDelete(%s): sequence blob should be deleted", test.name)
						}

						// Verify sequence actions are deleted
						for _, action := range seq.Actions {
							if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
								t.Errorf("TestDelete(%s): sequence action blob should be deleted", test.name)
							}
						}
					}
				}
			}
		})
	}
}

func TestDeletePlanInContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		containerExists bool
		uploadBlobs     bool
		wantErr         bool
	}{
		{
			name:            "Success: container doesn't exist",
			containerExists: false,
			uploadBlobs:     false,
			wantErr:         false,
		},
		{
			name:            "Success: all blobs deleted",
			containerExists: true,
			uploadBlobs:     true,
			wantErr:         false,
		},
		{
			name:            "Success: some blobs already missing",
			containerExists: true,
			uploadBlobs:     false,
			wantErr:         false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			fakeClient, del := setupDeleterTest(t)

			// Create a simple plan
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", false)
			containerName := containerForPlan("test", plan.ID)

			if !test.containerExists {
				// Delete the container to simulate non-existence
				// Since we can't delete containers with the fake, we'll use a non-existent container name
				containerName = "nonexistent-container"
			}

			if !test.uploadBlobs && test.containerExists {
				// Delete some blobs to simulate partial state
				_ = fakeClient.DeleteBlob(ctx, containerName, planEntryBlobName(plan.ID))
			}

			err := del.deletePlanInContainer(ctx, containerName, plan)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDeletePlanInContainer(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDeletePlanInContainer(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if test.containerExists && test.uploadBlobs {
				// Verify all blobs are deleted
				if fakeClient.BlobExists(containerName, planEntryBlobName(plan.ID)) {
					t.Errorf("TestDeletePlanInContainer(%s): plan entry blob should be deleted", test.name)
				}
				if fakeClient.BlobExists(containerName, planObjectBlobName(plan.ID)) {
					t.Errorf("TestDeletePlanInContainer(%s): plan object blob should be deleted", test.name)
				}
			}
		})
	}
}

// TestDeleteObjectBlobs verifies that deleting a Block, Sequence or Checks removes its own blob and every blob under it.
func TestDeleteObjectBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withBlocks bool
		// del deletes the object under test from plan.
		del func(ctx context.Context, d deleter, containerName string, plan *workflow.Plan) error
		// blobs are the names of the object's blob, first, and the blobs under it. All must exist before del and be
		// gone after.
		blobs func(plan *workflow.Plan) []string
	}{
		{
			name:       "Success: block with sequences and checks deleted",
			withBlocks: true,
			del: func(ctx context.Context, d deleter, containerName string, plan *workflow.Plan) error {
				return d.deleteBlockBlobs(ctx, containerName, plan.ID, plan.Blocks[0])
			},
			blobs: func(plan *workflow.Plan) []string {
				block := plan.Blocks[0]
				names := []string{blockBlobName(plan.ID, block.ID)}
				for _, seq := range block.Sequences {
					names = append(names, sequenceBlobName(plan.ID, seq.ID))
				}
				if block.PreChecks != nil {
					names = append(names, checksBlobName(plan.ID, block.PreChecks.ID))
				}
				return names
			},
		},
		{
			name:       "Success: sequence with actions deleted",
			withBlocks: true,
			del: func(ctx context.Context, d deleter, containerName string, plan *workflow.Plan) error {
				return d.deleteSequenceBlobs(ctx, containerName, plan.ID, plan.Blocks[0].Sequences[0])
			},
			blobs: func(plan *workflow.Plan) []string {
				seq := plan.Blocks[0].Sequences[0]
				names := []string{sequenceBlobName(plan.ID, seq.ID)}
				for _, action := range seq.Actions {
					names = append(names, actionBlobName(plan.ID, action.ID))
				}
				return names
			},
		},
		{
			name: "Success: checks with actions deleted",
			del: func(ctx context.Context, d deleter, containerName string, plan *workflow.Plan) error {
				return d.deleteChecksBlobs(ctx, containerName, plan.ID, plan.PreChecks)
			},
			blobs: func(plan *workflow.Plan) []string {
				names := []string{checksBlobName(plan.ID, plan.PreChecks.ID)}
				for _, action := range plan.PreChecks.Actions {
					names = append(names, actionBlobName(plan.ID, action.ID))
				}
				return names
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			fakeClient, del := setupDeleterTest(t)
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", test.withBlocks)
			containerName := containerForPlan("test", plan.ID)

			names := test.blobs(plan)
			for _, name := range names {
				if !fakeClient.BlobExists(containerName, name) {
					t.Fatalf("TestDeleteObjectBlobs(%s): blob %s should exist before deletion", test.name, name)
				}
			}

			if err := test.del(ctx, del, containerName, plan); err != nil {
				t.Fatalf("TestDeleteObjectBlobs(%s): got err == %s, want err == nil", test.name, err)
			}

			for _, name := range names {
				if fakeClient.BlobExists(containerName, name) {
					t.Errorf("TestDeleteObjectBlobs(%s): blob %s should be deleted", test.name, name)
				}
			}
		})
	}
}

func TestDeleteActionBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		blobExists bool
		wantErr    bool
	}{
		{
			name:       "Success: action blob deleted",
			blobExists: true,
			wantErr:    false,
		},
		{
			name:       "Success: action blob doesn't exist (no error)",
			blobExists: false,
			wantErr:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload plan
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", false)
			containerName := containerForPlan("test", plan.ID)
			action := plan.PreChecks.Actions[0]

			if !test.blobExists {
				// Delete the action blob to simulate it not existing
				_ = fakeClient.DeleteBlob(ctx, containerName, actionBlobName(plan.ID, action.ID))
			}

			err := del.deleteActionBlob(ctx, containerName, plan.ID, action.ID)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDeleteActionBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDeleteActionBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify action blob is deleted
			if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
				t.Errorf("TestDeleteActionBlob(%s): action blob should be deleted", test.name)
			}
		})
	}
}

// TestDeleteRetry is a regression test: Delete removed the planEntry blob first, so a Delete that failed on a later blob
// could not be retried. The retry's read needs the entry, so it failed with not found, and recovery skips plans with no
// entry, so the rest of the plan's blobs stayed until the container aged out. A retried Delete must finish the job.
// That includes a Running plan, which a read rebuilds from its child blobs: once a failed Delete has removed some of
// them, the retry's read failed as inconsistent storage.
func TestDeleteRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// running stores a Running plan, which a read rebuilds from its child blobs, instead of a NotStarted one.
		running bool
		// failBlob names the blob whose delete fails in the first Delete.
		failBlob func(plan *workflow.Plan) string
	}{
		{
			name:     "Success: a Delete retried after a block blob failed to delete removes the rest",
			failBlob: func(plan *workflow.Plan) string { return blockBlobName(plan.ID, plan.Blocks[0].ID) },
		},
		{
			name:     "Success: a Delete retried after the plan object blob failed to delete removes the rest",
			failBlob: func(plan *workflow.Plan) string { return planObjectBlobName(plan.ID) },
		},
		{
			// Only the entry is left, so the retry deletes just the entry.
			name:     "Success: a Delete retried after the planEntry blob failed to delete removes it",
			failBlob: func(plan *workflow.Plan) string { return planEntryBlobName(plan.ID) },
		},
		{
			// Regression: the block blob is deleted before its sequences, so the retry's read could not rebuild the
			// Running plan.
			name:     "Success: a Delete of a Running plan retried after a sequence blob failed to delete removes the rest",
			running:  true,
			failBlob: func(plan *workflow.Plan) string { return sequenceBlobName(plan.ID, plan.Blocks[0].Sequences[0].ID) },
		},
		{
			// Regression: as above, with the sequence blob gone too.
			name:    "Success: a Delete of a Running plan retried after an action blob failed to delete removes the rest",
			running: true,
			failBlob: func(plan *workflow.Plan) string {
				return actionBlobName(plan.ID, plan.Blocks[0].Sequences[0].Actions[0].ID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			var (
				fakeClient *blobops.Fake
				del        storage.Deleter
				plan       *workflow.Plan
			)
			if test.running {
				var v *Vault
				v, fakeClient = newFakeVault(t)
				plan = newRunningPlan(t, v)
				del = v
			} else {
				var d deleter
				fakeClient, d = setupDeleterTest(t)
				plan = createAndUploadTestPlan(ctx, t, fakeClient, "test", true)
				del = d
			}
			containerName := containerForPlan("test", plan.ID)

			failBlob := test.failBlob(plan)
			fakeClient.DeleteBlobErr = func(_, blob string) error {
				if blob == failBlob {
					return errors.New("throttled")
				}
				return nil
			}
			if err := del.Delete(ctx, plan.ID); err == nil {
				t.Fatalf("TestDeleteRetry(%s): first Delete: got err == nil, want err != nil", test.name)
			}

			fakeClient.DeleteBlobErr = nil
			if err := del.Delete(ctx, plan.ID); err != nil {
				t.Errorf("TestDeleteRetry(%s): retried Delete: got err == %s, want err == nil", test.name, err)
			}
			if left := len(fakeClient.GetContainer(containerName)); left != 0 {
				t.Errorf("TestDeleteRetry(%s): got %d blobs left after the retried Delete, want 0", test.name, left)
			}
		})
	}
}
