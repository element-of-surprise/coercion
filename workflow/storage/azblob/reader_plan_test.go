package azblob

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/go-json-experiment/json"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/kylelemons/godebug/pretty"
)

// groupErrs builds the *sync.Errors that a worker Group's Wait() returns, in the given order.
func groupErrs(errs ...error) *sync.Errors {
	var e sync.Errors
	for i, err := range errs {
		e.Add(i, err)
	}
	return &e
}

// TestUnwrapGroup verifies that unwrapGroup classifies a fan-out's aggregated errors
// deterministically: a not-found is reported only when every failure was a not-found, so a
// concurrent transient/internal failure is never masked as a not-found regardless of the order the
// goroutines happened to finish in.
func TestUnwrapGroup(t *testing.T) {
	t.Parallel()

	notFound := &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound), RawResponse: &http.Response{}}
	notFound2 := &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound), RawResponse: &http.Response{}}
	transient := &azcore.ResponseError{ErrorCode: string(bloberror.ServerBusy), RawResponse: &http.Response{}}
	internal := fmt.Errorf("internal boom") // carries no ResponseError

	tests := []struct {
		name         string
		in           error
		wantNotFound bool
		wantErr      bool
	}{
		{
			name: "Success: a nil error passes through as nil",
			in:   nil,
		},
		{
			name:    "Error: a non-group internal error passes through and is not a not-found",
			in:      internal,
			wantErr: true,
		},
		{
			name:         "Error: a non-group not-found error passes through as a not-found",
			in:           notFound,
			wantNotFound: true,
			wantErr:      true,
		},
		{
			name:         "Error: a group of only not-found errors classifies as a not-found",
			in:           groupErrs(notFound, notFound2),
			wantNotFound: true,
			wantErr:      true,
		},
		{
			name:    "Error: a not-found ahead of a transient ResponseError is not a not-found",
			in:      groupErrs(notFound, transient),
			wantErr: true,
		},
		{
			name:    "Error: a transient ResponseError ahead of a not-found is not a not-found",
			in:      groupErrs(transient, notFound),
			wantErr: true,
		},
		{
			name:    "Error: a not-found ahead of a ResponseError-less internal error is not a not-found",
			in:      groupErrs(notFound, internal),
			wantErr: true,
		},
		{
			name:    "Error: a ResponseError-less internal error ahead of a not-found is not a not-found",
			in:      groupErrs(internal, notFound),
			wantErr: true,
		},
	}

	for _, test := range tests {
		got := unwrapGroup(test.in)
		switch {
		case got == nil && test.wantErr:
			t.Errorf("TestUnwrapGroup(%s): got err == nil, want err != nil", test.name)
			continue
		case got != nil && !test.wantErr:
			t.Errorf("TestUnwrapGroup(%s): got err == %v, want err == nil", test.name, got)
			continue
		}
		// fetchPlan and reader.exists branch on blobops.IsNotFound to report a missing Plan instead of a storage failure.
		if gotNF := blobops.IsNotFound(got); gotNF != test.wantNotFound {
			t.Errorf("TestUnwrapGroup(%s): blobops.IsNotFound == %v, want %v", test.name, gotNF, test.wantNotFound)
		}
	}
}

// makeAction creates a test action with the given parameters and properly initialized State.
func makeAction(name, descr, plugin string, req any, status workflow.Status) *workflow.Action {
	a := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    name,
		Descr:   descr,
		Plugin:  plugin,
		Timeout: 30 * time.Second,
		Req:     req,
	}
	a.State.Set(workflow.State{Status: status})
	return a
}

// makeActionWithAttempts creates a test action with attempts and properly initialized State.
func makeActionWithAttempts(name, descr, plugin string, req any, attempts []workflow.Attempt, status workflow.Status) *workflow.Action {
	a := &workflow.Action{
		ID:      workflow.NewV7(),
		Name:    name,
		Descr:   descr,
		Plugin:  plugin,
		Timeout: 30 * time.Second,
		Req:     req,
		Attempts: func() workflow.AtomicSlice[workflow.Attempt] {
			var atomicAttempts workflow.AtomicSlice[workflow.Attempt]
			atomicAttempts.Set(attempts)
			return atomicAttempts
		}(),
	}
	a.State.Set(workflow.State{Status: status})
	return a
}

// makeChecks creates test checks with the given actions and properly initialized State.
func makeChecks(actions []*workflow.Action, status workflow.Status) *workflow.Checks {
	c := &workflow.Checks{
		ID:      workflow.NewV7(),
		Actions: actions,
	}
	c.State.Set(workflow.State{Status: status})
	return c
}

// makeSequence creates a test sequence with the given actions and properly initialized State.
func makeSequence(name, descr string, actions []*workflow.Action, status workflow.Status) *workflow.Sequence {
	s := &workflow.Sequence{
		ID:      workflow.NewV7(),
		Name:    name,
		Descr:   descr,
		Actions: actions,
	}
	s.State.Set(workflow.State{Status: status})
	return s
}

// makeBlock creates a test block with the given parameters and properly initialized State.
func makeBlock(name, descr string, preChecks *workflow.Checks, sequences []*workflow.Sequence, status workflow.Status) *workflow.Block {
	b := &workflow.Block{
		ID:        workflow.NewV7(),
		Name:      name,
		Descr:     descr,
		PreChecks: preChecks,
		Sequences: sequences,
	}
	b.State.Set(workflow.State{Status: status})
	return b
}

// makePlan creates a test plan with the given parameters and properly initialized State.
func makePlan(name, descr string, preChecks *workflow.Checks, blocks []*workflow.Block, status workflow.Status) *workflow.Plan {
	p := &workflow.Plan{
		ID:        workflow.NewV7(),
		Name:      name,
		Descr:     descr,
		PreChecks: preChecks,
		Blocks:    blocks,
	}
	p.State.Set(workflow.State{Status: status})
	return p
}

// makePlanFull creates a test plan with all check types and properly initialized State.
func makePlanFull(bypassChecks, preChecks, postChecks, contChecks, deferredChecks *workflow.Checks, blocks []*workflow.Block, status workflow.Status) *workflow.Plan {
	p := &workflow.Plan{
		ID:             workflow.NewV7(),
		Name:           "Test Plan",
		Descr:          "Test Description",
		BypassChecks:   bypassChecks,
		PreChecks:      preChecks,
		PostChecks:     postChecks,
		ContChecks:     contChecks,
		DeferredChecks: deferredChecks,
		Blocks:         blocks,
	}
	p.State.Set(workflow.State{Status: status})
	return p
}

func TestFixActions(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			// Regression: fixActions wrote back an empty Attempts for an action with none, so a plan read back without
			// running no longer matched the submitted one under a SkipZeroFields comparison ("Attempts: {}").
			name: "Success: fix Action.Req in plan-level PreChecks and keep an action's unset Attempts unset",
			plan: makePlan(
				"Test Plan",
				"Test Description",
				makeChecks(
					[]*workflow.Action{
						makeAction("test action", "test action", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "hello"}, workflow.NotStarted),
					},
					workflow.NotStarted,
				),
				[]*workflow.Block{},
				workflow.NotStarted,
			),
			wantErr: false,
		},
		{
			name: "Success: fix Action.Req and Attempt.Resp in multiple locations",
			plan: makePlan(
				"Test Plan",
				"Test Description",
				makeChecks(
					[]*workflow.Action{
						makeActionWithAttempts(
							"test action with attempt",
							"test action with attempt",
							testPlugins.HelloPluginName,
							testPlugins.HelloReq{Say: "hello"},
							[]workflow.Attempt{
								{
									Resp:  &testPlugins.HelloResp{Said: "hello"},
									Start: time.Now().UTC(),
									End:   time.Now().UTC(),
								},
							},
							workflow.Completed,
						),
					},
					workflow.Completed,
				),
				[]*workflow.Block{
					makeBlock(
						"test block",
						"test block",
						makeChecks(
							[]*workflow.Action{
								makeAction("block action", "block action", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "world"}, workflow.NotStarted),
							},
							workflow.NotStarted,
						),
						[]*workflow.Sequence{
							makeSequence(
								"test seq",
								"test seq",
								[]*workflow.Action{
									makeActionWithAttempts(
										"seq action",
										"seq action",
										testPlugins.HelloPluginName,
										testPlugins.HelloReq{Say: "sequence"},
										[]workflow.Attempt{
											{
												Resp:  &testPlugins.HelloResp{Said: "sequence"},
												Start: time.Now().UTC(),
												End:   time.Now().UTC(),
											},
										},
										workflow.Completed,
									),
								},
								workflow.NotStarted,
							),
						},
						workflow.NotStarted,
					),
				},
				workflow.NotStarted,
			),
			wantErr: false,
		},
		{
			name: "Success: fix Action.Req in all check types",
			plan: makePlanFull(
				makeChecks([]*workflow.Action{makeAction("bypass", "bypass", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "bypass"}, workflow.NotStarted)}, workflow.NotStarted),
				makeChecks([]*workflow.Action{makeAction("pre", "pre", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "pre"}, workflow.NotStarted)}, workflow.NotStarted),
				makeChecks([]*workflow.Action{makeAction("post", "post", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "post"}, workflow.NotStarted)}, workflow.NotStarted),
				makeChecks([]*workflow.Action{makeAction("cont", "cont", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "cont"}, workflow.NotStarted)}, workflow.NotStarted),
				makeChecks([]*workflow.Action{makeAction("deferred", "deferred", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "deferred"}, workflow.NotStarted)}, workflow.NotStarted),
				[]*workflow.Block{},
				workflow.NotStarted,
			),
			wantErr: false,
		},
		{
			name: "Error: plugin not found in registry",
			plan: makePlan(
				"Test Plan",
				"Test Description",
				makeChecks(
					[]*workflow.Action{
						makeAction("test action", "test action", "nonexistent.plugin", testPlugins.HelloReq{Say: "hello"}, workflow.NotStarted),
					},
					workflow.NotStarted,
				),
				[]*workflow.Block{},
				workflow.NotStarted,
			),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Simulate what happens when unmarshaling from JSON:
			// Action.Req and Attempt.Resp lose their concrete types and become map[string]any
			planBytes, err := json.Marshal(test.plan)
			if err != nil {
				t.Fatalf("TestFixActions(%s): failed to marshal plan: %v", test.name, err)
			}

			var unmarshaledPlan workflow.Plan
			if err := json.Unmarshal(planBytes, &unmarshaledPlan); err != nil {
				t.Fatalf("TestFixActions(%s): failed to unmarshal plan: %v", test.name, err)
			}

			// At this point, all Action.Req and Attempt.Resp are map[string]any
			// Verify this is the case before fixing
			if unmarshaledPlan.PreChecks != nil && len(unmarshaledPlan.PreChecks.Actions) > 0 {
				action := unmarshaledPlan.PreChecks.Actions[0]
				if action.Req != nil {
					if _, ok := action.Req.(testPlugins.HelloReq); ok {
						t.Errorf("TestFixActions(%s): Req should be map[string]any before fix, got %T", test.name, action.Req)
					}
				}
			}

			// Create reader with registry
			r := reader{
				reg: reg,
			}

			// Call fixActions
			err = r.fixActions(ctx, &unmarshaledPlan)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestFixActions(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestFixActions(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}

			cfg := pretty.Config{SkipZeroFields: true, PrintStringers: true, PrintTextMarshalers: true}
			if diff := cfg.Compare(test.plan, &unmarshaledPlan); diff != "" {
				t.Errorf("TestFixActions(%s): reconstructed plan mismatch, -want/+got:\n%s", test.name, diff)
			}

			// The comparison cannot tell a decoded map from the plugin's own type, so check every Req and Resp's type.
			for item := range walk.Plan(&unmarshaledPlan) {
				if item.Value.Type() != workflow.OTAction {
					continue
				}
				action := item.Action()
				if _, ok := action.Req.(testPlugins.HelloReq); !ok {
					t.Errorf("TestFixActions(%s): action(%s) Req type = %T, want testPlugins.HelloReq", test.name, action.Name, action.Req)
				}
				for _, attempt := range action.Attempts.Get() {
					if _, ok := attempt.Resp.(testPlugins.HelloResp); attempt.Resp != nil && !ok {
						t.Errorf("TestFixActions(%s): action(%s) attempt Resp type = %T, want testPlugins.HelloResp", test.name, action.Name, attempt.Resp)
					}
				}
			}
		})
	}
}

func TestFetchNonRunningPlanOrphanedEntry(t *testing.T) {
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
		retentionDays: 14,
	}

	plan := makePlan(
		"Test Plan",
		"Test Description",
		makeChecks(
			[]*workflow.Action{
				makeAction("test action", "test action", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "hello"}, workflow.NotStarted),
			},
			workflow.NotStarted,
		),
		[]*workflow.Block{},
		workflow.NotStarted, // NotStarted, so fetchNonRunningPlan will be called
	)

	containerName := containerForPlan(prefix, plan.ID)

	if err := fakeClient.EnsureContainer(ctx, containerName); err != nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: failed to create container: %v", err)
	}

	// Upload ONLY the entry blob (simulating a failed creation where object blob wasn't written)
	md, err := planToMetadata(ctx, plan)
	if err != nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: failed to create metadata: %v", err)
	}
	md[mdPlanType] = toPtr(ptEntry)

	planEntry, err := planToPlanEntry(plan)
	if err != nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: failed to create plan entry: %v", err)
	}

	planEntryData, err := json.Marshal(planEntry)
	if err != nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: failed to marshal plan entry: %v", err)
	}

	entryBlobName := planEntryBlobName(plan.ID)
	if err := fakeClient.UploadBlob(ctx, containerName, entryBlobName, md, planEntryData); err != nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: failed to upload plan entry: %v", err)
	}

	if !fakeClient.BlobExists(containerName, entryBlobName) {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: entry blob should exist before fetch")
	}

	_, err = r.fetchNonRunningPlan(ctx, containerName, plan.ID)
	if err == nil {
		t.Fatalf("TestFetchNonRunningPlanOrphanedEntry: expected error when object blob missing")
	}

	// fetchPlan branches on blobops.IsNotFound to report the Plan as not found (errors.ErrNotFound) instead of a storage
	// failure.
	if !blobops.IsNotFound(err) {
		t.Errorf("TestFetchNonRunningPlanOrphanedEntry: expected not found error, got: %v", err)
	}

	if fakeClient.BlobExists(containerName, entryBlobName) {
		t.Errorf("TestFetchNonRunningPlanOrphanedEntry: orphaned entry blob should have been deleted")
	}
}
