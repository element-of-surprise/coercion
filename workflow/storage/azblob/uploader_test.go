package azblob

import (
	"runtime"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// setupUploaderTest creates a test environment with fake client and uploader struct
func setupUploaderTest(t *testing.T) (*blobops.Fake, *uploader) {
	t.Helper()

	ctx := t.Context()
	fakeClient := blobops.NewFake()
	prefix := "test"

	// Create uploader
	u := &uploader{
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

// errFakeDelete is returned by a failing fake DeleteBlob.
var errFakeDelete = errors.New("fake delete failure")

// errFakeUpload is returned by a failing fake UploadBlob.
var errFakeUpload = errors.New("fake upload failure")

// blobState returns the plan status stored in a plan blob's metadata.
func blobState(t *testing.T, f *blobops.Fake, containerName, blobName string) workflow.Status {
	t.Helper()

	md, err := f.GetMetadata(t.Context(), containerName, blobName)
	if err != nil {
		t.Fatalf("blobState(%s): %s", blobName, err)
	}
	pm, err := mapToPlanMeta(md)
	if err != nil {
		t.Fatalf("blobState(%s): %s", blobName, err)
	}
	return pm.State.Status
}

func TestUploadPlan(t *testing.T) {
	t.Parallel()

	const (
		failNone = iota
		failSubObjects
		failObject
	)

	tests := []struct {
		name string
		// replace, when set, returns the plan to upload in place of the valid test plan; used for invalid input.
		replace func() *workflow.Plan
		// createFirst creates the test plan before the upload under test.
		createFirst bool
		// uploadPlanType is the upload under test.
		uploadPlanType uploadPlanType
		// status is the plan status written by the upload under test.
		status workflow.Status
		// fail picks which upload fails.
		fail int
		// deleteFails makes the planEntry cleanup delete fail.
		deleteFails bool
		// cancelAfterEntry ends the upload's context as its entry is written, before the sub-object uploads start.
		cancelAfterEntry bool

		wantEntry  bool
		wantObject bool
		// wantEntryStatus and wantObjectStatus are the stored statuses afterwards; checked only with createFirst.
		wantEntryStatus  workflow.Status
		wantObjectStatus workflow.Status
		wantErr          bool
	}{
		{
			name:           "Success: create writes the entry and object",
			uploadPlanType: uptCreate,
			status:         workflow.NotStarted,
			wantEntry:      true,
			wantObject:     true,
		},
		{
			name:             "Success: update rewrites the entry and object",
			createFirst:      true,
			uploadPlanType:   uptUpdate,
			status:           workflow.Running,
			wantEntry:        true,
			wantObject:       true,
			wantEntryStatus:  workflow.Running,
			wantObjectStatus: workflow.Running,
		},
		{
			name:             "Success: completion rewrites the entry and object",
			createFirst:      true,
			uploadPlanType:   uptComplete,
			status:           workflow.Completed,
			wantEntry:        true,
			wantObject:       true,
			wantEntryStatus:  workflow.Completed,
			wantObjectStatus: workflow.Completed,
		},
		{
			name:           "Error: plan is nil",
			replace:        func() *workflow.Plan { return nil },
			uploadPlanType: uptCreate,
			wantErr:        true,
		},
		{
			name: "Error: plan ID is nil",
			replace: func() *workflow.Plan {
				p := createUploadTestPlan(true)
				p.ID = uuid.Nil
				return p
			},
			uploadPlanType: uptCreate,
			wantErr:        true,
		},
		{
			name:           "Error: uploadPlanType is unknown",
			uploadPlanType: uptUnknown,
			wantErr:        true,
		},
		{
			// Regression: the cleanup error was joined in even when the cleanup succeeded, so the caller got a join
			// instead of the typed upload error.
			name:           "Error: a failed object upload on create removes the entry and returns the typed error",
			uploadPlanType: uptCreate,
			status:         workflow.NotStarted,
			fail:           failObject,
			wantErr:        true,
		},
		{
			// Regression: when the cleanup succeeded, the raw sub-object upload error was returned with no category or
			// type.
			name:           "Error: a failed sub-object upload on create removes the entry and returns a typed error",
			uploadPlanType: uptCreate,
			status:         workflow.NotStarted,
			fail:           failSubObjects,
			wantErr:        true,
		},
		{
			// Regression: once the context ended, the sub-object uploads not yet started were skipped without an error,
			// so the create wrote its object over missing sub-objects and reported success.
			name:             "Error: a create whose context ends before the sub-object uploads fails and removes the entry",
			uploadPlanType:   uptCreate,
			status:           workflow.NotStarted,
			cancelAfterEntry: true,
			wantErr:          true,
		},
		{
			name:           "Error: a failed entry cleanup on create is returned with the upload error",
			uploadPlanType: uptCreate,
			status:         workflow.NotStarted,
			fail:           failObject,
			deleteFails:    true,
			wantEntry:      true,
			wantErr:        true,
		},
		{
			name:             "Error: a failed object upload on update keeps the new entry and the old object",
			createFirst:      true,
			uploadPlanType:   uptUpdate,
			status:           workflow.Running,
			fail:             failObject,
			wantEntry:        true,
			wantObject:       true,
			wantEntryStatus:  workflow.Running,
			wantObjectStatus: workflow.NotStarted,
			wantErr:          true,
		},
		{
			name:             "Error: a failed object upload on completion keeps the new entry and the old object",
			createFirst:      true,
			uploadPlanType:   uptComplete,
			status:           workflow.Completed,
			fail:             failObject,
			wantEntry:        true,
			wantObject:       true,
			wantEntryStatus:  workflow.Completed,
			wantObjectStatus: workflow.NotStarted,
			wantErr:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(true)
			containerName := containerForPlan("test", plan.ID)

			if test.createFirst {
				if err := u.uploadPlan(ctx, plan, uptCreate); err != nil {
					t.Fatalf("TestUploadPlan(%s): setup create: %s", test.name, err)
				}
			}

			uploadCtx, cancelUpload := context.WithCancel(ctx)
			defer cancelUpload()
			fakeClient.UploadBlobErr = func(_, blobName string) error {
				if test.cancelAfterEntry && blobName == planEntryBlobName(plan.ID) {
					cancelUpload()
				}
				switch {
				case test.fail == failObject && blobName == planObjectBlobName(plan.ID):
					return errFakeUpload
				case test.fail == failSubObjects && blobName != planObjectBlobName(plan.ID) && blobName != planEntryBlobName(plan.ID):
					return errFakeUpload
				}
				return nil
			}
			if test.deleteFails {
				fakeClient.DeleteBlobErr = func(_, _ string) error { return errFakeDelete }
			}

			state := plan.State.Get()
			state.Status = test.status
			plan.State.Set(state)

			upload := plan
			if test.replace != nil {
				upload = test.replace()
			}
			err := u.uploadPlan(uploadCtx, upload, test.uploadPlanType)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestUploadPlan(%s): got err == nil, want err != nil", test.name)
			case err != nil && !test.wantErr:
				t.Errorf("TestUploadPlan(%s): got err == %s, want err == nil", test.name, err)
			}
			entryName := planEntryBlobName(plan.ID)
			if got := fakeClient.BlobExists(containerName, entryName); got != test.wantEntry {
				t.Errorf("TestUploadPlan(%s): got entry exists == %v, want %v", test.name, got, test.wantEntry)
			}
			if got := fakeClient.BlobExists(containerName, planObjectBlobName(plan.ID)); got != test.wantObject {
				t.Errorf("TestUploadPlan(%s): got object exists == %v, want %v", test.name, got, test.wantObject)
			}
			if !test.createFirst || !test.wantEntry {
				return
			}
			if got := blobState(t, fakeClient, containerName, entryName); got != test.wantEntryStatus {
				t.Errorf("TestUploadPlan(%s): got entry status %v, want %v", test.name, got, test.wantEntryStatus)
			}
			if got := blobState(t, fakeClient, containerName, planObjectBlobName(plan.ID)); got != test.wantObjectStatus {
				t.Errorf("TestUploadPlan(%s): got object status %v, want %v", test.name, got, test.wantObjectStatus)
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
			t.Parallel()

			ctx := t.Context()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadPlanEntry(%s): failed to create container: %v", test.name, err)
			}

			md, err := planToMetadata(ctx, plan)
			if err != nil {
				t.Fatalf("TestUploadPlanEntry(%s): failed to create metadata: %v", test.name, err)
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
			t.Parallel()

			ctx := t.Context()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadPlanObject(%s): failed to create container: %v", test.name, err)
			}

			md, err := planToMetadata(ctx, plan)
			if err != nil {
				t.Fatalf("TestUploadPlanObject(%s): failed to create metadata: %v", test.name, err)
			}

			err = u.uploadPlanObject(ctx, plan, md, uptCreate)

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
			t.Parallel()

			ctx := t.Context()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(test.withBlocks)
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadSubObjects(%s): failed to create container: %v", test.name, err)
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

// TestUploadObjectBlobs verifies that uploading a Block, Sequence or Checks writes its own blob and every blob under it.
func TestUploadObjectBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withBlocks bool
		// upload uploads the object under test from plan.
		upload func(ctx context.Context, u *uploader, containerName string, plan *workflow.Plan) error
		// blobs are the names of the object's blob and the blobs under it. All must exist after upload.
		blobs func(plan *workflow.Plan) []string
	}{
		{
			name:       "Success: upload block with sequences and checks",
			withBlocks: true,
			upload: func(ctx context.Context, u *uploader, containerName string, plan *workflow.Plan) error {
				return u.uploadBlockBlob(ctx, containerName, plan.ID, plan.Blocks[0], 0)
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
			name:       "Success: upload sequence with actions",
			withBlocks: true,
			upload: func(ctx context.Context, u *uploader, containerName string, plan *workflow.Plan) error {
				return u.uploadSequenceBlob(ctx, containerName, plan.ID, plan.Blocks[0].Sequences[0], 0)
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
			name: "Success: upload checks with actions",
			upload: func(ctx context.Context, u *uploader, containerName string, plan *workflow.Plan) error {
				return u.uploadChecksBlob(ctx, containerName, plan.ID, plan.PreChecks)
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
			fakeClient, u := setupUploaderTest(t)
			plan := createUploadTestPlan(test.withBlocks)
			containerName := containerForPlan("test", plan.ID)
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadObjectBlobs(%s): failed to create container: %v", test.name, err)
			}

			if err := test.upload(ctx, u, containerName, plan); err != nil {
				t.Fatalf("TestUploadObjectBlobs(%s): got err == %s, want err == nil", test.name, err)
			}

			for _, name := range test.blobs(plan) {
				if !fakeClient.BlobExists(containerName, name) {
					t.Errorf("TestUploadObjectBlobs(%s): blob %s should exist", test.name, name)
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
			t.Parallel()

			ctx := t.Context()
			fakeClient, u := setupUploaderTest(t)

			plan := createUploadTestPlan(false)
			action := plan.PreChecks.Actions[0]
			containerName := containerForPlan("test", plan.ID)

			// Create container first
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestUploadActionBlob(%s): failed to create container: %v", test.name, err)
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
	// Not parallel: sets process-wide GOMAXPROCS.

	// Set GOMAXPROCS to 1 to increase likelihood of deadlock with single-threaded scheduling
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	ctx := t.Context()
	fakeClient := blobops.NewFake()

	// Use very small pool sizes to maximize deadlock potential
	// With a single pool of size 2, uploading a complex plan with nested parallelism
	// would deadlock because parent tasks would occupy both workers waiting for children.
	// With separate pools for each level tasks at different levels
	// should not block each other.
	u := &uploader{
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
