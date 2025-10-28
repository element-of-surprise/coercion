package azblob

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
)

func TestFixActions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	planID := workflow.NewV7()

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name: "Success: fix Action.Req in plan-level PreChecks",
			plan: &workflow.Plan{
				ID:    planID,
				Name:  "Test Plan",
				Descr: "Test Description",
				PreChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "test action",
							Descr:   "test action",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "hello"},
							State: &workflow.State{
								Status: workflow.NotStarted,
							},
						},
					},
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
				},
				Blocks: []*workflow.Block{},
				State: &workflow.State{
					Status: workflow.NotStarted,
				},
			},
			wantErr: false,
		},
		{
			name: "Success: fix Action.Req and Attempt.Resp in multiple locations",
			plan: &workflow.Plan{
				ID:    planID,
				Name:  "Test Plan",
				Descr: "Test Description",
				PreChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "test action with attempt",
							Descr:   "test action with attempt",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "hello"},
							Attempts: []*workflow.Attempt{
								{
									Resp:  &testPlugins.HelloResp{Said: "hello"},
									Start: time.Now().UTC(),
									End:   time.Now().UTC(),
								},
							},
							State: &workflow.State{
								Status: workflow.Completed,
							},
						},
					},
					State: &workflow.State{
						Status: workflow.Completed,
					},
				},
				Blocks: []*workflow.Block{
					{
						ID:    workflow.NewV7(),
						Name:  "test block",
						Descr: "test block",
						PreChecks: &workflow.Checks{
							ID: workflow.NewV7(),
							Actions: []*workflow.Action{
								{
									ID:      workflow.NewV7(),
									Name:    "block action",
									Descr:   "block action",
									Plugin:  testPlugins.HelloPluginName,
									Timeout: 30 * time.Second,
									Req:     testPlugins.HelloReq{Say: "world"},
									State: &workflow.State{
										Status: workflow.NotStarted,
									},
								},
							},
							State: &workflow.State{
								Status: workflow.NotStarted,
							},
						},
						Sequences: []*workflow.Sequence{
							{
								ID:    workflow.NewV7(),
								Name:  "test seq",
								Descr: "test seq",
								Actions: []*workflow.Action{
									{
										ID:      workflow.NewV7(),
										Name:    "seq action",
										Descr:   "seq action",
										Plugin:  testPlugins.HelloPluginName,
										Timeout: 30 * time.Second,
										Req:     testPlugins.HelloReq{Say: "sequence"},
										Attempts: []*workflow.Attempt{
											{
												Resp:  &testPlugins.HelloResp{Said: "sequence"},
												Start: time.Now().UTC(),
												End:   time.Now().UTC(),
											},
										},
										State: &workflow.State{
											Status: workflow.Completed,
										},
									},
								},
								State: &workflow.State{
									Status: workflow.NotStarted,
								},
							},
						},
						State: &workflow.State{
							Status: workflow.NotStarted,
						},
					},
				},
				State: &workflow.State{
					Status: workflow.NotStarted,
				},
			},
			wantErr: false,
		},
		{
			name: "Success: fix Action.Req in all check types",
			plan: &workflow.Plan{
				ID:    planID,
				Name:  "Test Plan",
				Descr: "Test Description",
				BypassChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "bypass",
							Descr:   "bypass",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "bypass"},
							State:   &workflow.State{Status: workflow.NotStarted},
						},
					},
					State: &workflow.State{Status: workflow.NotStarted},
				},
				PreChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "pre",
							Descr:   "pre",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "pre"},
							State:   &workflow.State{Status: workflow.NotStarted},
						},
					},
					State: &workflow.State{Status: workflow.NotStarted},
				},
				PostChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "post",
							Descr:   "post",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "post"},
							State:   &workflow.State{Status: workflow.NotStarted},
						},
					},
					State: &workflow.State{Status: workflow.NotStarted},
				},
				ContChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "cont",
							Descr:   "cont",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "cont"},
							State:   &workflow.State{Status: workflow.NotStarted},
						},
					},
					State: &workflow.State{Status: workflow.NotStarted},
				},
				DeferredChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "deferred",
							Descr:   "deferred",
							Plugin:  testPlugins.HelloPluginName,
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "deferred"},
							State:   &workflow.State{Status: workflow.NotStarted},
						},
					},
					State: &workflow.State{Status: workflow.NotStarted},
				},
				Blocks: []*workflow.Block{},
				State:  &workflow.State{Status: workflow.NotStarted},
			},
			wantErr: false,
		},
		{
			name: "Error: plugin not found in registry",
			plan: &workflow.Plan{
				ID:    planID,
				Name:  "Test Plan",
				Descr: "Test Description",
				PreChecks: &workflow.Checks{
					ID: workflow.NewV7(),
					Actions: []*workflow.Action{
						{
							ID:      workflow.NewV7(),
							Name:    "test action",
							Descr:   "test action",
							Plugin:  "nonexistent.plugin",
							Timeout: 30 * time.Second,
							Req:     testPlugins.HelloReq{Say: "hello"},
							State: &workflow.State{
								Status: workflow.NotStarted,
							},
						},
					},
					State: &workflow.State{
						Status: workflow.NotStarted,
					},
				},
				Blocks: []*workflow.Block{},
				State: &workflow.State{
					Status: workflow.NotStarted,
				},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Simulate what happens when unmarshaling from JSON:
			// Action.Req and Attempt.Resp lose their concrete types and become map[string]interface{}
			planBytes, err := json.Marshal(test.plan)
			if err != nil {
				t.Fatalf("TestFixActions(%s): failed to marshal plan: %v", test.name, err)
			}

			var unmarshaledPlan workflow.Plan
			if err := json.Unmarshal(planBytes, &unmarshaledPlan); err != nil {
				t.Fatalf("TestFixActions(%s): failed to unmarshal plan: %v", test.name, err)
			}

			// At this point, all Action.Req and Attempt.Resp are map[string]interface{}
			// Verify this is the case before fixing
			if unmarshaledPlan.PreChecks != nil && len(unmarshaledPlan.PreChecks.Actions) > 0 {
				action := unmarshaledPlan.PreChecks.Actions[0]
				if action.Req != nil {
					if _, ok := action.Req.(testPlugins.HelloReq); ok {
						t.Errorf("TestFixActions(%s): Req should be map[string]interface{} before fix, got %T", test.name, action.Req)
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

			// Verify Action.Req was fixed in plan-level checks
			for _, checks := range []*workflow.Checks{
				unmarshaledPlan.BypassChecks,
				unmarshaledPlan.PreChecks,
				unmarshaledPlan.PostChecks,
				unmarshaledPlan.ContChecks,
				unmarshaledPlan.DeferredChecks,
			} {
				if checks != nil {
					for _, action := range checks.Actions {
						if action.Req != nil {
							req, ok := action.Req.(testPlugins.HelloReq)
							if !ok {
								t.Errorf("TestFixActions(%s): plan-level action Req type = %T, want testPlugins.HelloReq", test.name, action.Req)
								continue
							}
							if req.Say == "" {
								t.Errorf("TestFixActions(%s): plan-level action Req.Say is empty", test.name)
							}
						}

						// Verify Attempt.Resp was fixed
						for _, attempt := range action.Attempts {
							if attempt.Resp != nil {
								resp, ok := attempt.Resp.(testPlugins.HelloResp)
								if !ok {
									t.Errorf("TestFixActions(%s): plan-level attempt Resp type = %T, want testPlugins.HelloResp", test.name, attempt.Resp)
									continue
								}
								if resp.Said == "" {
									t.Errorf("TestFixActions(%s): plan-level attempt Resp.Said is empty", test.name)
								}
							}
						}
					}
				}
			}

			// Verify Action.Req was fixed in blocks
			for _, block := range unmarshaledPlan.Blocks {
				// Block-level checks
				for _, checks := range []*workflow.Checks{
					block.BypassChecks,
					block.PreChecks,
					block.PostChecks,
					block.ContChecks,
					block.DeferredChecks,
				} {
					if checks != nil {
						for _, action := range checks.Actions {
							if action.Req != nil {
								req, ok := action.Req.(testPlugins.HelloReq)
								if !ok {
									t.Errorf("TestFixActions(%s): block-level action Req type = %T, want testPlugins.HelloReq", test.name, action.Req)
									continue
								}
								if req.Say == "" {
									t.Errorf("TestFixActions(%s): block-level action Req.Say is empty", test.name)
								}
							}
						}
					}
				}

				// Sequence actions
				for _, seq := range block.Sequences {
					for _, action := range seq.Actions {
						if action.Req != nil {
							req, ok := action.Req.(testPlugins.HelloReq)
							if !ok {
								t.Errorf("TestFixActions(%s): sequence action Req type = %T, want testPlugins.HelloReq", test.name, action.Req)
								continue
							}
							if req.Say == "" {
								t.Errorf("TestFixActions(%s): sequence action Req.Say is empty", test.name)
							}
						}

						// Verify Attempt.Resp was fixed in sequences
						for _, attempt := range action.Attempts {
							if attempt.Resp != nil {
								resp, ok := attempt.Resp.(testPlugins.HelloResp)
								if !ok {
									t.Errorf("TestFixActions(%s): sequence attempt Resp type = %T, want testPlugins.HelloResp", test.name, attempt.Resp)
									continue
								}
								if resp.Said == "" {
									t.Errorf("TestFixActions(%s): sequence attempt Resp.Said is empty", test.name)
								}
							}
						}
					}
				}
			}
		})
	}
}
