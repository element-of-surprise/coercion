package azblob

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"golang.org/x/sync/singleflight"
)

// setupDeleterTest creates a test environment with fake client and deleter struct
func setupDeleterTest(t *testing.T) (*blobops.Fake, deleter) {
	t.Helper()

	ctx := context.Background()
	fakeClient := blobops.NewFake()
	prefix := "test"

	// Create plugin registry
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	planMu := planlocks.New(ctx)

	// Create reader
	r := reader{
		mu:            planMu,
		readFlight:    &singleflight.Group{},
		existsFlight:  &singleflight.Group{},
		prefix:        prefix,
		client:        fakeClient,
		reg:           reg,
		retentionDays: 30,
	}

	// Create deleter
	del := deleter{
		mu:     planMu,
		prefix: prefix,
		client: fakeClient,
		reader: r,
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

	plan := &workflow.Plan{
		ID:         planID,
		Name:       "Test Plan for Deletion",
		Descr:      "Test Plan Description",
		SubmitTime: time.Now().UTC(),
		PreChecks:  preChecks,
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
		mu:          planlocks.New(ctx),
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
			ctx := context.Background()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload test plan
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", test.withBlocks)
			containerName := containerForPlan("test", plan.ID)

			// Verify blobs exist before deletion
			if !fakeClient.BlobExists(containerName, planEntryBlobName(plan.ID)) {
				t.Fatalf("TestDelete: plan entry blob should exist before deletion")
			}

			// Delete the plan
			err := del.Delete(ctx, plan.ID)

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
			ctx := context.Background()
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

func TestDeleteBlockBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: block with sequences and checks deleted",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload plan with blocks
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", true)
			containerName := containerForPlan("test", plan.ID)
			block := plan.Blocks[0]

			// Verify block blob exists before deletion
			if !fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
				t.Fatalf("TestDeleteBlockBlobs: block blob should exist before deletion")
			}

			err := del.deleteBlockBlobs(ctx, containerName, plan.ID, block)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDeleteBlockBlobs(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDeleteBlockBlobs(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify block blob is deleted
			if fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
				t.Errorf("TestDeleteBlockBlobs(%s): block blob should be deleted", test.name)
			}

			// Verify sequences are deleted
			for _, seq := range block.Sequences {
				if fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
					t.Errorf("TestDeleteBlockBlobs(%s): sequence blob should be deleted", test.name)
				}
			}

			// Verify block checks are deleted
			if block.PreChecks != nil {
				if fakeClient.BlobExists(containerName, checksBlobName(plan.ID, block.PreChecks.ID)) {
					t.Errorf("TestDeleteBlockBlobs(%s): block checks blob should be deleted", test.name)
				}
			}
		})
	}
}

func TestDeleteSequenceBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: sequence with actions deleted",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload plan with blocks
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", true)
			containerName := containerForPlan("test", plan.ID)
			seq := plan.Blocks[0].Sequences[0]

			// Verify sequence blob exists before deletion
			if !fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
				t.Fatalf("TestDeleteSequenceBlobs: sequence blob should exist before deletion")
			}

			err := del.deleteSequenceBlobs(ctx, containerName, plan.ID, seq)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDeleteSequenceBlobs(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDeleteSequenceBlobs(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify sequence blob is deleted
			if fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
				t.Errorf("TestDeleteSequenceBlobs(%s): sequence blob should be deleted", test.name)
			}

			// Verify actions are deleted
			for _, action := range seq.Actions {
				if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
					t.Errorf("TestDeleteSequenceBlobs(%s): action blob should be deleted", test.name)
				}
			}
		})
	}
}

func TestDeleteChecksBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: checks with actions deleted",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, del := setupDeleterTest(t)

			// Create and upload plan
			plan := createAndUploadTestPlan(ctx, t, fakeClient, "test", false)
			containerName := containerForPlan("test", plan.ID)
			checks := plan.PreChecks

			// Verify checks blob exists before deletion
			if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, checks.ID)) {
				t.Fatalf("TestDeleteChecksBlobs: checks blob should exist before deletion")
			}

			err := del.deleteChecksBlobs(ctx, containerName, plan.ID, checks)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestDeleteChecksBlobs(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestDeleteChecksBlobs(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify checks blob is deleted
			if fakeClient.BlobExists(containerName, checksBlobName(plan.ID, checks.ID)) {
				t.Errorf("TestDeleteChecksBlobs(%s): checks blob should be deleted", test.name)
			}

			// Verify actions are deleted
			for _, action := range checks.Actions {
				if fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
					t.Errorf("TestDeleteChecksBlobs(%s): action blob should be deleted", test.name)
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
			ctx := context.Background()
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
