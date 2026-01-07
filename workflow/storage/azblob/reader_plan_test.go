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

	ctx := context.Background()
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	tests := []struct {
		name    string
		plan    *workflow.Plan
		wantErr bool
	}{
		{
			name: "Success: fix Action.Req in plan-level PreChecks",
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
						for _, attempt := range action.Attempts.Get() {
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
						for _, attempt := range action.Attempts.Get() {
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
