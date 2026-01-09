package azblob

import (
	"fmt"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"golang.org/x/sync/singleflight"
)

// setupRecoveryTest creates a test environment with fake client and recovery struct
func setupRecoveryTest(t *testing.T) (*blobops.Fake, recovery) {
	t.Helper()

	ctx := context.Background()
	fakeClient := blobops.NewFake()
	prefix := "test"

	// Create plugin registry
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	// Create reader
	r := reader{
		mu:            planlocks.New(ctx),
		readFlight:    &singleflight.Group{},
		existsFlight:  &singleflight.Group{},
		prefix:        prefix,
		client:        fakeClient,
		reg:           reg,
		retentionDays: 14,
	}

	// Create uploader
	u := &uploader{
		mu:          planlocks.New(ctx),
		client:      fakeClient,
		prefix:      prefix,
		planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
		blockPool:   context.Pool(ctx).Limited(ctx, "", 10),
		leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
	}

	// Create recovery
	rec := recovery{
		reader:   r,
		uploader: u,
	}

	return fakeClient, rec
}

// createTestPlan creates a plan with various sub-objects for testing
func createTestPlan(running bool) *workflow.Plan {
	planID := workflow.NewV7()

	status := workflow.NotStarted
	if running {
		status = workflow.Running
	}

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
		Sequences: []*workflow.Sequence{seq},
	}
	block.State.Set(workflow.State{Status: workflow.NotStarted})

	plan := &workflow.Plan{
		ID:         planID,
		Name:       "Test Plan",
		Descr:      "Test Plan Description",
		SubmitTime: time.Now().UTC(),
		PreChecks:  preChecks,
		Blocks:     []*workflow.Block{block},
	}
	plan.State.Set(workflow.State{Status: status})

	return plan
}

// uploadPlanToFake uploads a plan and its metadata to the fake client.
func uploadPlanToFake(ctx context.Context, t *testing.T, fakeClient *blobops.Fake, prefix string, plan *workflow.Plan) {
	t.Helper()

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

	// Upload plan object blob with metadata
	md[mdPlanType] = toPtr(ptObject)
	planData, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("failed to marshal plan: %v", err)
	}

	objectBlobName := planObjectBlobName(plan.ID)
	if err := fakeClient.UploadBlob(ctx, containerName, objectBlobName, md, planData); err != nil {
		t.Fatalf("failed to upload plan object: %v", err)
	}
}

func TestRecoveryBlobExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupBlob     bool
		wantErr       bool
		expectedExist bool
	}{
		{
			name:          "Success: blob exists",
			setupBlob:     true,
			wantErr:       false,
			expectedExist: true,
		},
		{
			name:          "Success: blob does not exist",
			setupBlob:     false,
			wantErr:       false,
			expectedExist: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			containerName := "test-container"
			blobName := "test-blob"

			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestRecoveryBlobExists: failed to create container: %v", err)
			}

			if test.setupBlob {
				if err := fakeClient.UploadBlob(ctx, containerName, blobName, nil, []byte("test data")); err != nil {
					t.Fatalf("TestRecoveryBlobExists: failed to upload blob: %v", err)
				}
			}

			exists, err := rec.blobExists(ctx, containerName, blobName)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestRecoveryBlobExists(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestRecoveryBlobExists(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			if exists != test.expectedExist {
				t.Errorf("TestRecoveryBlobExists(%s): got exists == %v, want exists == %v", test.name, exists, test.expectedExist)
			}
		})
	}
}

func TestRecoverPlanOrphanedEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fakeClient, rec := setupRecoveryTest(t)

	plan := createTestPlan(false)
	containerName := containerForPlan("test", plan.ID)

	if err := fakeClient.EnsureContainer(ctx, containerName); err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: failed to create container: %v", err)
	}

	// Upload ONLY the entry blob (simulating a failed creation where object blob wasn't written)
	md, err := planToMetadata(ctx, plan)
	if err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: failed to create metadata: %v", err)
	}
	md[mdPlanType] = toPtr(ptEntry)

	planEntry, err := planToPlanEntry(plan)
	if err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: failed to create plan entry: %v", err)
	}

	planEntryData, err := json.Marshal(planEntry)
	if err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: failed to marshal plan entry: %v", err)
	}

	entryBlobName := planEntryBlobName(plan.ID)
	if err := fakeClient.UploadBlob(ctx, containerName, entryBlobName, md, planEntryData); err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: failed to upload plan entry: %v", err)
	}

	if !fakeClient.BlobExists(containerName, entryBlobName) {
		t.Fatalf("TestRecoverPlanOrphanedEntry: entry blob should exist before recovery")
	}

	err = rec.recoverPlan(ctx, containerName, plan.ID)
	if err != nil {
		t.Fatalf("TestRecoverPlanOrphanedEntry: got err == %s, want err == nil", err)
	}

	if fakeClient.BlobExists(containerName, entryBlobName) {
		t.Errorf("TestRecoverPlanOrphanedEntry: orphaned entry blob should have been deleted")
	}
}

func TestRecoverPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		planRunning     bool
		missingBlobs    []string
		wantErr         bool
		expectRecovered []string
	}{
		{
			name:            "Success: plan running, no recovery",
			planRunning:     true,
			missingBlobs:    []string{},
			wantErr:         false,
			expectRecovered: []string{},
		},
		{
			name:            "Success: plan not running, all blobs exist",
			planRunning:     false,
			missingBlobs:    []string{},
			wantErr:         false,
			expectRecovered: []string{},
		},
		{
			name:            "Success: plan not running, missing block blob",
			planRunning:     false,
			missingBlobs:    []string{"block"},
			wantErr:         false,
			expectRecovered: []string{"block"},
		},
		{
			name:            "Success: plan not running, missing sequence blob",
			planRunning:     false,
			missingBlobs:    []string{"sequence"},
			wantErr:         false,
			expectRecovered: []string{"sequence"},
		},
		{
			name:            "Success: plan not running, missing checks blob",
			planRunning:     false,
			missingBlobs:    []string{"checks"},
			wantErr:         false,
			expectRecovered: []string{"checks"},
		},
		{
			name:            "Success: plan not running, missing action blob",
			planRunning:     false,
			missingBlobs:    []string{"action"},
			wantErr:         false,
			expectRecovered: []string{"action"},
		},
		{
			name:            "Success: plan not running, multiple missing blobs",
			planRunning:     false,
			missingBlobs:    []string{"block", "sequence", "checks", "action"},
			wantErr:         false,
			expectRecovered: []string{"block", "sequence", "checks", "action"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			plan := createTestPlan(test.planRunning)

			uploadPlanToFake(ctx, t, fakeClient, "test", plan)

			containerName := containerForPlan("test", plan.ID)

			u := &uploader{
				mu:          planlocks.New(ctx),
				client:      fakeClient,
				prefix:      "test",
				planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
				blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
				leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
			}

			if err := u.uploadSubObjects(ctx, containerName, plan); err != nil {
				t.Fatalf("TestRecoverPlan: failed to upload sub-objects: %v", err)
			}

			// Delete the blobs we want to be missing
			for _, blobType := range test.missingBlobs {
				var blobName string
				switch blobType {
				case "block":
					blobName = blockBlobName(plan.ID, plan.Blocks[0].ID)
				case "sequence":
					blobName = sequenceBlobName(plan.ID, plan.Blocks[0].Sequences[0].ID)
				case "checks":
					blobName = checksBlobName(plan.ID, plan.PreChecks.ID)
				case "action":
					if plan.PreChecks != nil && len(plan.PreChecks.Actions) > 0 {
						blobName = actionBlobName(plan.ID, plan.PreChecks.Actions[0].ID)
					}
				}
				if blobName != "" {
					if err := fakeClient.DeleteBlob(ctx, containerName, blobName); err != nil {
						t.Fatalf("TestRecoverPlan: failed to delete blob %s: %v", blobName, err)
					}
				}
			}

			err := rec.recoverPlan(ctx, containerName, plan.ID)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestRecoverPlan(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestRecoverPlan(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			for _, blobType := range test.expectRecovered {
				var blobName string
				switch blobType {
				case "block":
					blobName = blockBlobName(plan.ID, plan.Blocks[0].ID)
				case "sequence":
					blobName = sequenceBlobName(plan.ID, plan.Blocks[0].Sequences[0].ID)
				case "checks":
					blobName = checksBlobName(plan.ID, plan.PreChecks.ID)
				case "action":
					if plan.PreChecks != nil && len(plan.PreChecks.Actions) > 0 {
						blobName = actionBlobName(plan.ID, plan.PreChecks.Actions[0].ID)
					}
				}

				if blobName != "" {
					exists := fakeClient.BlobExists(containerName, blobName)
					if !exists {
						t.Errorf("TestRecoverPlan(%s): expected blob %s to be recovered, but it doesn't exist", test.name, blobName)
					}
				}
			}
		})
	}
}

func TestRecoverPlansInContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		numPlans        int
		plansRunning    []bool
		recoverPlanErrs map[int]error
		wantErr         bool
	}{
		{
			name:     "Success: empty container",
			numPlans: 0,
			wantErr:  false,
		},
		{
			name:         "Success: single plan recovered",
			numPlans:     1,
			plansRunning: []bool{false},
			wantErr:      false,
		},
		{
			name:         "Success: multiple plans recovered",
			numPlans:     3,
			plansRunning: []bool{false, false, false},
			wantErr:      false,
		},
		{
			name:         "Success: mix of running and non-running plans",
			numPlans:     2,
			plansRunning: []bool{false, true},
			wantErr:      false,
		},
		{
			name:         "Error: recoverPlan fails for one plan",
			numPlans:     2,
			plansRunning: []bool{false, false},
			recoverPlanErrs: map[int]error{
				0: fmt.Errorf("test recovery error"),
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			containerName := "test-container"

			// Create container in fake client
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestRecoverPlansInContainer: failed to create container: %v", err)
			}

			plans := make([]*workflow.Plan, test.numPlans)
			listResults := make([]storage.ListResult, test.numPlans)

			for i := 0; i < test.numPlans; i++ {
				running := false
				if i < len(test.plansRunning) {
					running = test.plansRunning[i]
				}
				plan := createTestPlan(running)
				plans[i] = plan

				listResults[i] = storage.ListResult{
					ID:         plan.ID,
					Name:       plan.Name,
					Descr:      plan.Descr,
					SubmitTime: plan.SubmitTime,
					State:      plan.State.Get(),
				}
			}

			rec.reader.testListPlansInContainer = func(ctx context.Context, containerName string) ([]storage.ListResult, error) {
				return listResults, nil
			}

			recoveredPlans := sync.ShardedMap[uuid.UUID, bool]{}
			rec.testRecoverPlan = func(ctx context.Context, containerName string, planID uuid.UUID) error {
				// Find the plan index
				for i, plan := range plans {
					if plan.ID == planID {
						if err, ok := test.recoverPlanErrs[i]; ok {
							return err
						}
						recoveredPlans.Set(planID, true)
						return nil
					}
				}
				return fmt.Errorf("unknown plan ID: %s", planID)
			}

			err := rec.recoverPlansInContainer(ctx, containerName)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestRecoverPlansInContainer(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestRecoverPlansInContainer(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify all plans were attempted to be recovered
			for i, plan := range plans {
				if _, recovered := recoveredPlans.Get(plan.ID); !recovered {
					t.Errorf("TestRecoverPlansInContainer(%s): plan %d (ID: %s) was not recovered", test.name, i, plan.ID)
				}
			}
		})
	}
}

func TestEnsureActionBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupBlob  bool
		wantErr    bool
		wantExists bool
	}{
		{
			name:       "Success: action blob exists",
			setupBlob:  true,
			wantErr:    false,
			wantExists: true,
		},
		{
			name:       "Success: action blob missing, create it",
			setupBlob:  false,
			wantErr:    false,
			wantExists: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			// Create test plan
			plan := createTestPlan(false)
			action := plan.PreChecks.Actions[0]

			containerName := containerForPlan("test", plan.ID)
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestEnsureActionBlob: failed to create container: %v", err)
			}

			if test.setupBlob {
				// Upload the action blob
				u := &uploader{
					mu:          planlocks.New(ctx),
					client:      fakeClient,
					prefix:      "test",
					planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
					blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
					leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
				}
				if err := u.uploadActionBlob(ctx, containerName, plan.ID, action, 0); err != nil {
					t.Fatalf("TestEnsureActionBlob: failed to upload action blob: %v", err)
				}
			}

			c := creator{
				prefix:   "test",
				endpoint: "https://test.blob.core.windows.net",
				reader:   rec.reader,
			}

			err := rec.ensureActionBlob(ctx, c, containerName, plan.ID, action, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestEnsureActionBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestEnsureActionBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify blob exists
			actionBlobName := actionBlobName(plan.ID, action.ID)
			exists := fakeClient.BlobExists(containerName, actionBlobName)
			if exists != test.wantExists {
				t.Errorf("TestEnsureActionBlob(%s): got exists == %v, want exists == %v", test.name, exists, test.wantExists)
			}
		})
	}
}

func TestEnsureChecksBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupBlob  bool
		wantErr    bool
		wantExists bool
	}{
		{
			name:       "Success: checks blob exists",
			setupBlob:  true,
			wantErr:    false,
			wantExists: true,
		},
		{
			name:       "Success: checks blob missing, create it",
			setupBlob:  false,
			wantErr:    false,
			wantExists: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			// Create test plan
			plan := createTestPlan(false)
			checks := plan.PreChecks

			containerName := containerForPlan("test", plan.ID)
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestEnsureChecksBlob: failed to create container: %v", err)
			}

			if test.setupBlob {
				// Upload the checks blob and its actions
				u := &uploader{
					mu:          planlocks.New(ctx),
					client:      fakeClient,
					prefix:      "test",
					planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
					blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
					leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
				}
				if err := u.uploadChecksBlob(ctx, containerName, plan.ID, checks); err != nil {
					t.Fatalf("TestEnsureChecksBlob: failed to upload checks blob: %v", err)
				}
			}

			c := creator{
				prefix:   "test",
				endpoint: "https://test.blob.core.windows.net",
				reader:   rec.reader,
			}

			err := rec.ensureChecksBlob(ctx, c, containerName, plan.ID, checks)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestEnsureChecksBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestEnsureChecksBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify checks blob exists
			checksBlobName := checksBlobName(plan.ID, checks.ID)
			exists := fakeClient.BlobExists(containerName, checksBlobName)
			if exists != test.wantExists {
				t.Errorf("TestEnsureChecksBlob(%s): got checks blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
			}

			// Verify action blobs exist
			for _, action := range checks.Actions {
				actionBlobName := actionBlobName(plan.ID, action.ID)
				exists := fakeClient.BlobExists(containerName, actionBlobName)
				if exists != test.wantExists {
					t.Errorf("TestEnsureChecksBlob(%s): got action blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
				}
			}
		})
	}
}

func TestEnsureSequenceBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupBlob  bool
		wantErr    bool
		wantExists bool
	}{
		{
			name:       "Success: sequence blob exists",
			setupBlob:  true,
			wantErr:    false,
			wantExists: true,
		},
		{
			name:       "Success: sequence blob missing, create it",
			setupBlob:  false,
			wantErr:    false,
			wantExists: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			// Create test plan
			plan := createTestPlan(false)
			seq := plan.Blocks[0].Sequences[0]

			containerName := containerForPlan("test", plan.ID)
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestEnsureSequenceBlob: failed to create container: %v", err)
			}

			if test.setupBlob {
				// Upload the sequence blob and its actions
				u := &uploader{
					mu:          planlocks.New(ctx),
					client:      fakeClient,
					prefix:      "test",
					planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
					blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
					leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
				}
				if err := u.uploadSequenceBlob(ctx, containerName, plan.ID, seq, 0); err != nil {
					t.Fatalf("TestEnsureSequenceBlob: failed to upload sequence blob: %v", err)
				}
			}

			c := creator{
				prefix:   "test",
				endpoint: "https://test.blob.core.windows.net",
				reader:   rec.reader,
			}

			err := rec.ensureSequenceBlob(ctx, c, containerName, plan.ID, seq, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestEnsureSequenceBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestEnsureSequenceBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify sequence blob exists
			seqBlobName := sequenceBlobName(plan.ID, seq.ID)
			exists := fakeClient.BlobExists(containerName, seqBlobName)
			if exists != test.wantExists {
				t.Errorf("TestEnsureSequenceBlob(%s): got sequence blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
			}

			// Verify action blobs exist
			for _, action := range seq.Actions {
				actionBlobName := actionBlobName(plan.ID, action.ID)
				exists := fakeClient.BlobExists(containerName, actionBlobName)
				if exists != test.wantExists {
					t.Errorf("TestEnsureSequenceBlob(%s): got action blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
				}
			}
		})
	}
}

func TestEnsureBlockBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupBlob  bool
		wantErr    bool
		wantExists bool
	}{
		{
			name:       "Success: block blob exists",
			setupBlob:  true,
			wantErr:    false,
			wantExists: true,
		},
		{
			name:       "Success: block blob missing, create it",
			setupBlob:  false,
			wantErr:    false,
			wantExists: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fakeClient, rec := setupRecoveryTest(t)

			// Create test plan
			plan := createTestPlan(false)
			block := plan.Blocks[0]

			containerName := containerForPlan("test", plan.ID)
			if err := fakeClient.CreateContainer(ctx, containerName); err != nil {
				t.Fatalf("TestEnsureBlockBlob: failed to create container: %v", err)
			}

			if test.setupBlob {
				// Upload the block blob and its sub-objects
				u := &uploader{
					mu:          planlocks.New(ctx),
					client:      fakeClient,
					prefix:      "test",
					planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
					blockPool:   context.Pool(ctx).Limited(ctx, "", 5),
					leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
				}
				if err := u.uploadBlockBlob(ctx, containerName, plan.ID, block, 0); err != nil {
					t.Fatalf("TestEnsureBlockBlob: failed to upload block blob: %v", err)
				}
			}

			c := creator{
				prefix:   "test",
				endpoint: "https://test.blob.core.windows.net",
				reader:   rec.reader,
			}

			err := rec.ensureBlockBlob(ctx, c, containerName, plan.ID, block, 0)

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestEnsureBlockBlob(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestEnsureBlockBlob(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			// Verify block blob exists
			blockBlobName := blockBlobName(plan.ID, block.ID)
			exists := fakeClient.BlobExists(containerName, blockBlobName)
			if exists != test.wantExists {
				t.Errorf("TestEnsureBlockBlob(%s): got block blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
			}

			// Verify sequence blobs exist
			for _, seq := range block.Sequences {
				seqBlobName := sequenceBlobName(plan.ID, seq.ID)
				exists := fakeClient.BlobExists(containerName, seqBlobName)
				if exists != test.wantExists {
					t.Errorf("TestEnsureBlockBlob(%s): got sequence blob exists == %v, want exists == %v", test.name, exists, test.wantExists)
				}
			}
		})
	}
}
