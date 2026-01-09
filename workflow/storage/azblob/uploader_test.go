package azblob

import (
	"runtime"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// setupUploaderTest creates a test environment with fake client and uploader struct
func setupUploaderTest(t *testing.T) (*blobops.Fake, *uploader) {
	t.Helper()

	ctx := context.Background()
	fakeClient := blobops.NewFake()
	prefix := "test"

	// Create uploader
	u := &uploader{
		mu:          planlocks.New(ctx),
		client:      fakeClient,
		prefix:      prefix,
		planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
		blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
		leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
	}

	return fakeClient, u
}

// createUploadTestPlan creates a plan for upload testing
func createUploadTestPlan(withBlocks bool) *workflow.Plan {
	planID := workflow.NewV7()

	preCheckAction := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    "pre-check action",
		Descr:   "pre-check action desc",
		Plugin:  testPlugins.HelloPluginName,
		Timeout: 30 * time.Second,
		Req:     testPlugins.HelloReq{Say: "hello"},
	}
	preCheckAction.SetState(workflow.State{Status: workflow.NotStarted})

	preChecks := &workflow.Checks{
		ID:      workflow.NewV7(),
		Actions: []*workflow.Action{preCheckAction},
	}
	preChecks.SetState(workflow.State{Status: workflow.NotStarted})

	plan := &workflow.Plan{
		ID:         planID,
		Name:       "Test Upload Plan",
		Descr:      "Test Plan Description",
		SubmitTime: time.Now().UTC(),
		PreChecks:  preChecks,
	}
	plan.SetState(workflow.State{Status: workflow.NotStarted})

	if withBlocks {
		blockCheckAction := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "block check action",
			Descr:   "block check action desc",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: "block check"},
		}
		blockCheckAction.SetState(workflow.State{Status: workflow.NotStarted})

		blockPreChecks := &workflow.Checks{
			ID:      workflow.NewV7(),
			Actions: []*workflow.Action{blockCheckAction},
		}
		blockPreChecks.SetState(workflow.State{Status: workflow.NotStarted})

		seqAction := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "sequence action",
			Descr:   "sequence action desc",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: "sequence"},
		}
		seqAction.SetState(workflow.State{Status: workflow.NotStarted})

		seq := &workflow.Sequence{
			ID:      workflow.NewV7(),
			Name:    "Test Sequence",
			Descr:   "Test Sequence Description",
			Actions: []*workflow.Action{seqAction},
		}
		seq.SetState(workflow.State{Status: workflow.NotStarted})

		block := &workflow.Block{
			ID:        workflow.NewV7(),
			Name:      "Test Block",
			Descr:     "Test Block Description",
			PreChecks: blockPreChecks,
			Sequences: []*workflow.Sequence{seq},
		}
		block.SetState(workflow.State{Status: workflow.NotStarted})

		plan.Blocks = []*workflow.Block{block}
	}

	return plan
}

// createNilIDPlan creates a plan with a nil ID for error testing
func createNilIDPlan() *workflow.Plan {
	plan := &workflow.Plan{
		ID:   uuid.Nil,
		Name: "Test",
	}
	plan.SetState(workflow.State{Status: workflow.NotStarted})
	return plan
}

func TestUploadPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		plan           *workflow.Plan
		uploadPlanType uploadPlanType
		wantErr        bool
	}{
		{
			name:           "Success: upload plan with uptCreate",
			plan:           createUploadTestPlan(true),
			uploadPlanType: uptCreate,
			wantErr:        false,
		},
		{
			name:           "Success: upload plan with uptUpdate",
			plan:           createUploadTestPlan(false),
			uploadPlanType: uptUpdate,
			wantErr:        false,
		},
		{
			name:           "Success: upload plan with uptComplete",
			plan:           createUploadTestPlan(false),
			uploadPlanType: uptComplete,
			wantErr:        false,
		},
		{
			name:           "Error: plan is nil",
			plan:           nil,
			uploadPlanType: uptCreate,
			wantErr:        true,
		},
		{
			name:           "Error: plan ID is nil",
			plan:           createNilIDPlan(),
			uploadPlanType: uptCreate,
			wantErr:        true,
		},
		{
			name:           "Error: uploadPlanType is unknown",
			plan:           createUploadTestPlan(false),
			uploadPlanType: uptUnknown,
			wantErr:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			err := u.uploadPlan(ctx, test.plan, test.uploadPlanType)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadPlan(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadPlan(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify blobs were created
			containerName := containerForPlan("test", test.plan.ID)

			// Verify plan entry blob
			if !fakeClient.BlobExists(containerName, planEntryBlobName(test.plan.ID)) {
				t.Errorf("TestUploadPlan(%s): plan entry blob should exist", test.name)
			}

			// Verify plan object blob
			if !fakeClient.BlobExists(containerName, planObjectBlobName(test.plan.ID)) {
				t.Errorf("TestUploadPlan(%s): plan object blob should exist", test.name)
			}

			// For uptCreate, verify sub-objects were uploaded
			if test.uploadPlanType == uptCreate {
				if test.plan.PreChecks != nil {
					if !fakeClient.BlobExists(containerName, checksBlobName(test.plan.ID, test.plan.PreChecks.ID)) {
						t.Errorf("TestUploadPlan(%s): PreChecks blob should exist for uptCreate", test.name)
					}
				}

				for _, block := range test.plan.Blocks {
					if !fakeClient.BlobExists(containerName, blockBlobName(test.plan.ID, block.ID)) {
						t.Errorf("TestUploadPlan(%s): block blob should exist for uptCreate", test.name)
					}
				}
			}
		})
	}
}

func TestUploadPlanEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload plan entry",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadPlanEntry: failed to create container: %v", err)
			}

			md, err := planToMetadata(ctx, plan)
			if err != nil {
				t.Fatalf("TestUploadPlanEntry: failed to create metadata: %v", err)
			}

			err = u.uploadPlanEntry(ctx, plan, md)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadPlanEntry(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadPlanEntry(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify blob exists
			if !fakeClient.BlobExists(containerName, planEntryBlobName(plan.ID)) {
				t.Errorf("TestUploadPlanEntry(%s): plan entry blob should exist", test.name)
			}

			// Verify blob content
			data, err := fakeClient.GetBlob(ctx, containerName, planEntryBlobName(plan.ID))
			if err != nil {
				t.Errorf("TestUploadPlanEntry(%s): failed to get blob: %v", test.name, err)
			}

			var entry planEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Errorf("TestUploadPlanEntry(%s): failed to unmarshal entry: %v", test.name, err)
			}

			if entry.ID != plan.ID {
				t.Errorf("TestUploadPlanEntry(%s): entry ID mismatch: got %v, want %v", test.name, entry.ID, plan.ID)
			}
		})
	}
}

func TestUploadPlanObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload plan object",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadPlanObject: failed to create container: %v", err)
			}

			md, err := planToMetadata(ctx, plan)
			if err != nil {
				t.Fatalf("TestUploadPlanObject: failed to create metadata: %v", err)
			}

			err = u.uploadPlanObject(ctx, plan, md)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadPlanObject(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadPlanObject(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify blob exists
			if !fakeClient.BlobExists(containerName, planObjectBlobName(plan.ID)) {
				t.Errorf("TestUploadPlanObject(%s): plan object blob should exist", test.name)
			}

			// Verify blob content
			data, err := fakeClient.GetBlob(ctx, containerName, planObjectBlobName(plan.ID))
			if err != nil {
				t.Errorf("TestUploadPlanObject(%s): failed to get blob: %v", test.name, err)
			}

			var retrievedPlan workflow.Plan
			if err := json.Unmarshal(data, &retrievedPlan); err != nil {
				t.Errorf("TestUploadPlanObject(%s): failed to unmarshal plan: %v", test.name, err)
			}

			if retrievedPlan.ID != plan.ID {
				t.Errorf("TestUploadPlanObject(%s): plan ID mismatch: got %v, want %v", test.name, retrievedPlan.ID, plan.ID)
			}
		})
	}
}

func TestUploadSubObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withBlocks bool
		wantErr    bool
	}{
		{
			name:       "Success: upload sub-objects with blocks",
			withBlocks: true,
			wantErr:    false,
		},
		{
			name:       "Success: upload sub-objects without blocks",
			withBlocks: false,
			wantErr:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(test.withBlocks)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadSubObjects: failed to create container: %v", err)
			}

			err := u.uploadSubObjects(ctx, containerName, plan)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadSubObjects(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadSubObjects(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify checks blobs
			if plan.PreChecks != nil {
				if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, plan.PreChecks.ID)) {
					t.Errorf("TestUploadSubObjects(%s): PreChecks blob should exist", test.name)
				}

				for _, action := range plan.PreChecks.Actions {
					if !fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
						t.Errorf("TestUploadSubObjects(%s): PreChecks action blob should exist", test.name)
					}
				}
			}

			// Verify block blobs
			for _, block := range plan.Blocks {
				if !fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
					t.Errorf("TestUploadSubObjects(%s): block blob should exist", test.name)
				}

				// Verify block's checks
				if block.PreChecks != nil {
					if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, block.PreChecks.ID)) {
						t.Errorf("TestUploadSubObjects(%s): block PreChecks blob should exist", test.name)
					}
				}

				// Verify sequences
				for _, seq := range block.Sequences {
					if !fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
						t.Errorf("TestUploadSubObjects(%s): sequence blob should exist", test.name)
					}

					// Verify actions
					for _, action := range seq.Actions {
						if !fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
							t.Errorf("TestUploadSubObjects(%s): sequence action blob should exist", test.name)
						}
					}
				}
			}
		})
	}
}

func TestUploadBlockBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload block with sequences and checks",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(true)
			block := plan.Blocks[0]
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadBlockBlob: failed to create container: %v", err)
			}

			err := u.uploadBlockBlob(ctx, containerName, plan.ID, block, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadBlockBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadBlockBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify block blob
			if !fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
				t.Errorf("TestUploadBlockBlob(%s): block blob should exist", test.name)
			}

			// Verify sequences
			for _, seq := range block.Sequences {
				if !fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
					t.Errorf("TestUploadBlockBlob(%s): sequence blob should exist", test.name)
				}
			}

			// Verify checks
			if block.PreChecks != nil {
				if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, block.PreChecks.ID)) {
					t.Errorf("TestUploadBlockBlob(%s): block checks blob should exist", test.name)
				}
			}
		})
	}
}

func TestUploadSequenceBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload sequence with actions",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(true)
			seq := plan.Blocks[0].Sequences[0]
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadSequenceBlob: failed to create container: %v", err)
			}

			err := u.uploadSequenceBlob(ctx, containerName, plan.ID, seq, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadSequenceBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadSequenceBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify sequence blob
			if !fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
				t.Errorf("TestUploadSequenceBlob(%s): sequence blob should exist", test.name)
			}

			// Verify actions
			for _, action := range seq.Actions {
				if !fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
					t.Errorf("TestUploadSequenceBlob(%s): action blob should exist", test.name)
				}
			}
		})
	}
}

func TestUploadChecksBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload checks with actions",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			checks := plan.PreChecks
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadChecksBlob: failed to create container: %v", err)
			}

			err := u.uploadChecksBlob(ctx, containerName, plan.ID, checks)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadChecksBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadChecksBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify checks blob
			if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, checks.ID)) {
				t.Errorf("TestUploadChecksBlob(%s): checks blob should exist", test.name)
			}

			// Verify actions
			for _, action := range checks.Actions {
				if !fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
					t.Errorf("TestUploadChecksBlob(%s): action blob should exist", test.name)
				}
			}
		})
	}
}

func TestUploadActionBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "Success: upload action blob",
			wantErr: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			action := plan.PreChecks.Actions[0]
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadActionBlob: failed to create container: %v", err)
			}

			err := u.uploadActionBlob(ctx, containerName, plan.ID, action, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadActionBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadActionBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify action blob
			if !fakeClient.BlobExists(containerName, actionBlobName(plan.ID, action.ID)) {
				t.Errorf("TestUploadActionBlob(%s): action blob should exist", test.name)
			}

			// Verify blob content
			data, err := fakeClient.GetBlob(ctx, containerName, actionBlobName(plan.ID, action.ID))
			if err != nil {
				t.Errorf("TestUploadActionBlob(%s): failed to get blob: %v", test.name, err)
			}

			var entry actionsEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Errorf("TestUploadActionBlob(%s): failed to unmarshal action: %v", test.name, err)
			}

			if entry.ID != action.ID {
				t.Errorf("TestUploadActionBlob(%s): action ID mismatch: got %v, want %v", test.name, entry.ID, action.ID)
			}
		})
	}
}

// createComplexPlan creates a plan with multiple blocks, sequences, checks, and actions
// to maximize the potential for worker pool exhaustion in nested parallel uploads.
func createComplexPlan(numBlocks, numSequencesPerBlock, numActionsPerSequence int) *workflow.Plan {
	planID := workflow.NewV7()

	var preCheckActions []*workflow.Action
	for i := 0; i < 3; i++ {
		action := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "plan-pre-check-action",
			Descr:   "plan pre-check action desc",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: "hello"},
		}
		action.SetState(workflow.State{Status: workflow.NotStarted})
		preCheckActions = append(preCheckActions, action)
	}

	preChecks := &workflow.Checks{
		ID:      workflow.NewV7(),
		Actions: preCheckActions,
	}
	preChecks.SetState(workflow.State{Status: workflow.NotStarted})

	var blocks []*workflow.Block
	for b := 0; b < numBlocks; b++ {
		// Block-level checks
		var blockCheckActions []*workflow.Action
		for i := 0; i < 2; i++ {
			action := &workflow.Action{
				ID:      workflow.NewV7(),
				Name:    "block-check-action",
				Descr:   "block check action desc",
				Plugin:  testPlugins.HelloPluginName,
				Timeout: 30 * time.Second,
				Req:     testPlugins.HelloReq{Say: "block check"},
			}
			action.SetState(workflow.State{Status: workflow.NotStarted})
			blockCheckActions = append(blockCheckActions, action)
		}

		blockPreChecks := &workflow.Checks{
			ID:      workflow.NewV7(),
			Actions: blockCheckActions,
		}
		blockPreChecks.SetState(workflow.State{Status: workflow.NotStarted})

		var sequences []*workflow.Sequence
		for s := 0; s < numSequencesPerBlock; s++ {
			var seqActions []*workflow.Action
			for a := 0; a < numActionsPerSequence; a++ {
				action := &workflow.Action{
					ID:      workflow.NewV7(),
					Name:    "sequence-action",
					Descr:   "sequence action desc",
					Plugin:  testPlugins.HelloPluginName,
					Timeout: 30 * time.Second,
					Req:     testPlugins.HelloReq{Say: "sequence"},
				}
				action.SetState(workflow.State{Status: workflow.NotStarted})
				seqActions = append(seqActions, action)
			}

			seq := &workflow.Sequence{
				ID:      workflow.NewV7(),
				Name:    "Test Sequence",
				Descr:   "Test Sequence Description",
				Actions: seqActions,
			}
			seq.SetState(workflow.State{Status: workflow.NotStarted})
			sequences = append(sequences, seq)
		}

		block := &workflow.Block{
			ID:        workflow.NewV7(),
			Name:      "Test Block",
			Descr:     "Test Block Description",
			PreChecks: blockPreChecks,
			Sequences: sequences,
		}
		block.SetState(workflow.State{Status: workflow.NotStarted})
		blocks = append(blocks, block)
	}

	plan := &workflow.Plan{
		ID:         planID,
		Name:       "Complex Test Plan",
		Descr:      "Complex Test Plan Description",
		SubmitTime: time.Now().UTC(),
		PreChecks:  preChecks,
		Blocks:     blocks,
	}
	plan.SetState(workflow.State{Status: workflow.NotStarted})

	return plan
}

// TestRegressionConcurrentUploadsDeadlock validates that concurrent uploads with nested parallelism
// do not deadlock due to worker pool exhaustion. This test uses GOMAXPROCS=1 and small pool
// sizes to maximize the likelihood of triggering the deadlock if the fix is not in place.
//
// The original bug occurred when multiple concurrent requests each submitted upload tasks to a single shared pool,
// with parent tasks (uploadBlockBlob, uploadSequenceBlob) occupied workers while waiting for children.  Child tasks
// couldn't execute because all workers were occupied by waiting parents and a deadlock occurred. Parents wait for
// children that can never run.
func TestRegressionConcurrentUploadsDeadlock(t *testing.T) {
	// Set GOMAXPROCS to 1 to increase likelihood of deadlock with single-threaded scheduling
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	ctx := context.Background()
	fakeClient := blobops.NewFake()

	// Use very small pool sizes to maximize deadlock potential
	// With a single pool of size 2, uploading a complex plan with nested parallelism
	// would deadlock because parent tasks would occupy both workers waiting for children.
	// With separate pools for each level tasks at different levels
	// should not block each other.
	u := &uploader{
		mu:          planlocks.New(ctx),
		client:      fakeClient,
		prefix:      "test",
		planObjPool: context.Pool(ctx).Limited(ctx, "testTop", 2),
		blockPool:   context.Pool(ctx).Limited(ctx, "testSub", 2),
		leafObjPool: context.Pool(ctx).Limited(ctx, "testLeaf", 2),
	}

	numConcurrentUploads := 5
	plans := make([]*workflow.Plan, numConcurrentUploads)
	for i := 0; i < numConcurrentUploads; i++ {
		plans[i] = createComplexPlan(3, 2, 2)
	}

	for _, plan := range plans {
		containerName := containerForPlan("test", plan.ID)
		if err := fakeClient.EnsureContainer(ctx, containerName); err != nil {
			t.Fatalf("TestRegressionConcurrentUploadsDeadlock: failed to create container: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	g := context.Pool(ctx).Group()
	for _, plan := range plans {
		plan := plan // capture loop variable
		g.Go(ctx, func(ctx context.Context) error {
			containerName := containerForPlan("test", plan.ID)
			return u.uploadSubObjects(ctx, containerName, plan)
		})
	}

	err := g.Wait(ctx)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("TestRegressionConcurrentUploadsDeadlock: test timed out - likely deadlock due to worker pool exhaustion")
	}

	if err != nil {
		t.Fatalf("TestRegressionConcurrentUploadsDeadlock: unexpected error: %v", err)
	}

	for _, plan := range plans {
		containerName := containerForPlan("test", plan.ID)

		if plan.PreChecks != nil {
			if !fakeClient.BlobExists(containerName, checksBlobName(plan.ID, plan.PreChecks.ID)) {
				t.Errorf("TestRegressionConcurrentUploadsDeadlock: PreChecks blob should exist for plan %s", plan.ID)
			}
		}

		for _, block := range plan.Blocks {
			if !fakeClient.BlobExists(containerName, blockBlobName(plan.ID, block.ID)) {
				t.Errorf("TestRegressionConcurrentUploadsDeadlock: block blob should exist for plan %s", plan.ID)
			}

			for _, seq := range block.Sequences {
				if !fakeClient.BlobExists(containerName, sequenceBlobName(plan.ID, seq.ID)) {
					t.Errorf("TestRegressionConcurrentUploadsDeadlock: sequence blob should exist for plan %s", plan.ID)
				}
			}
		}
	}
}
