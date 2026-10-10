package sm

import (
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/gostdlib/base/statemachine"
)

func newChecksWithState(state *workflow.State) *workflow.Checks {
	c := &workflow.Checks{}
	c.State.Set(*state)
	return c
}

func newBlockWithState(state *workflow.State) *workflow.Block {
	b := &workflow.Block{}
	b.State.Set(*state)
	return b
}

func failedDA() *workflow.DeferredActions {
	d := &workflow.DeferredActions{}
	d.State.Set(workflow.State{Status: workflow.Failed})
	return d
}

func TestPlanChecks(t *testing.T) {
	t.Parallel()

	finals := finalStates{}

	tests := []struct {
		name       string
		plan       func() *workflow.Plan
		wantNext   statemachine.State[Data]
		wantStatus workflow.Status
		wantReason workflow.FailureReason
		wantErr    bool
	}{
		{
			name: "Success: all checks pass and the Plan moves on to its blocks",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:  newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks: newChecksWithState(&workflow.State{Status: workflow.Completed}),
					PostChecks: newChecksWithState(&workflow.State{Status: workflow.Completed}),
				}
			},
			wantNext:   finals.blocks,
			wantStatus: workflow.Running,
		},
		{
			name: "Error: failed PreChecks fail the Plan with FRPreCheck",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks: newChecksWithState(&workflow.State{Status: workflow.Failed}),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPreCheck,
			wantErr:    true,
		},
		{
			name: "Error: failed ContChecks fail the Plan with FRContCheck",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:  newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks: newChecksWithState(&workflow.State{Status: workflow.Failed}),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRContCheck,
			wantErr:    true,
		},
		{
			name: "Error: failed PostChecks fail the Plan with FRPostCheck",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:  newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks: newChecksWithState(&workflow.State{Status: workflow.Completed}),
					PostChecks: newChecksWithState(&workflow.State{Status: workflow.Failed}),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPostCheck,
			wantErr:    true,
		},
		{
			name: "Error: failed DeferredChecks fail the Plan with FRDeferredCheck",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:      newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks:     newChecksWithState(&workflow.State{Status: workflow.Completed}),
					PostChecks:     newChecksWithState(&workflow.State{Status: workflow.Completed}),
					DeferredChecks: newChecksWithState(&workflow.State{Status: workflow.Failed}),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredCheck,
			wantErr:    true,
		},
		{
			name: "Error: failed PreChecks and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:       newChecksWithState(&workflow.State{Status: workflow.Failed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name: "Error: failed ContChecks and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:       newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks:      newChecksWithState(&workflow.State{Status: workflow.Failed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name: "Error: failed PostChecks and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:       newChecksWithState(&workflow.State{Status: workflow.Completed}),
					PostChecks:      newChecksWithState(&workflow.State{Status: workflow.Failed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name: "Error: failed DeferredChecks and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					DeferredChecks:  newChecksWithState(&workflow.State{Status: workflow.Failed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name: "Error: passing checks and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:       newChecksWithState(&workflow.State{Status: workflow.Completed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
		{
			name: "Error: all four checks passing and failed DeferredActions fail the Plan with FRDeferredAction",
			plan: func() *workflow.Plan {
				return &workflow.Plan{
					PreChecks:       newChecksWithState(&workflow.State{Status: workflow.Completed}),
					ContChecks:      newChecksWithState(&workflow.State{Status: workflow.Completed}),
					PostChecks:      newChecksWithState(&workflow.State{Status: workflow.Completed}),
					DeferredChecks:  newChecksWithState(&workflow.State{Status: workflow.Completed}),
					DeferredActions: failedDA(),
				}
			},
			wantNext:   finals.end,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredAction,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		plan := test.plan()
		plan.State.Set(workflow.State{Status: workflow.Running})

		req := finals.planChecks(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan}})
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestPlanChecks(%s): got err == nil, want err != nil", test.name)
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestPlanChecks(%s): got err == %s, want err == nil", test.name, req.Err)
		}

		if methodName(req.Next) != methodName(test.wantNext) {
			t.Errorf("TestPlanChecks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(test.wantNext))
		}
		if plan.State.Get().Status != test.wantStatus {
			t.Errorf("TestPlanChecks(%s): got status == %v, want status == %v", test.name, plan.State.Get().Status, test.wantStatus)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestPlanChecks(%s): got reason == %v, want reason == %v", test.name, plan.Reason, test.wantReason)
		}

		// Regression: end set the status to Completed unconditionally, overwriting the Failed status planChecks set. end
		// records a Plan still Running as Completed and must leave a Failed one Failed.
		finals.end(req)
		wantEnd := test.wantStatus
		if wantEnd == workflow.Running {
			wantEnd = workflow.Completed
		}
		if got := plan.State.Get().Status; got != wantEnd {
			t.Errorf("TestPlanChecks(%s): after end, got status == %v, want status == %v", test.name, got, wantEnd)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestPlanChecks(%s): after end, got reason == %v, want reason == %v", test.name, plan.Reason, test.wantReason)
		}
	}
}

func TestBlocks(t *testing.T) {
	t.Parallel()

	finals := finalStates{}

	tests := []struct {
		name     string
		block    *workflow.Block
		wantNext statemachine.State[Data]
		wantErr  bool
		wantBug  bool
	}{
		{
			name:     "Success: a completed block moves the Plan to end",
			block:    newBlockWithState(&workflow.State{Status: workflow.Completed}),
			wantNext: finals.end,
		},
		{
			name:    "Error: a failed block fails the Plan",
			block:   newBlockWithState(&workflow.State{Status: workflow.Failed}),
			wantErr: true,
		},
		{
			name:    "Error: a block still Running is a bug",
			block:   newBlockWithState(&workflow.State{Status: workflow.Running}),
			wantErr: true,
			wantBug: true,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			Blocks: []*workflow.Block{test.block},
		}
		plan.State.Set(workflow.State{Status: workflow.Running})

		req := finals.blocks(statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan}})
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestBlocks(%s): got err == nil, want err != nil", test.name)
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestBlocks(%s): got err == %s, want err == nil", test.name, req.Err)
		case req.Err != nil:
			// End logs the final state error only when it is a bug (errors.IsBug).
			if errors.IsBug(req.Err) != test.wantBug {
				t.Errorf("TestBlocks(%s): got errors.IsBug(err) == %v, want %v", test.name, errors.IsBug(req.Err), test.wantBug)
			}
		}
		if test.wantNext != nil {
			if methodName(req.Next) != methodName(test.wantNext) {
				t.Errorf("TestBlocks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(test.wantNext))
			}
		}
	}
}

func TestExamineChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checks     [4]*workflow.Checks
		wantReason workflow.FailureReason
		wantUnrun  workflow.FailureReason
		wantErr    bool
		wantBug    bool
	}{
		{
			// Regression: NotStarted checks were a bug here, but a failed block skips the Plan's PostChecks. Whether
			// they are a bug is decided by finalStates.blocks.
			name: "Success: PostChecks that never ran are reported as unrun, not a failure",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.NotStarted}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantUnrun: workflow.FRPostCheck,
		},
		{
			name: "Error: a failed DeferredCheck after PostChecks that never ran returns FRDeferredCheck",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.NotStarted}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
			},
			wantReason: workflow.FRDeferredCheck,
			wantUnrun:  workflow.FRPostCheck,
			wantErr:    true,
		},
		{
			name: "Success: all checks pass",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
		},
		{
			name: "Success: all checks pass with a nil check",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				nil,
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
		},
		{
			name: "Error: a failed PreCheck returns FRPreCheck",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantReason: workflow.FRPreCheck,
			wantErr:    true,
		},
		{
			name: "Error: a failed ContCheck returns FRContCheck",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantReason: workflow.FRContCheck,
			wantErr:    true,
		},
		{
			name: "Error: a failed PostCheck returns FRPostCheck",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantReason: workflow.FRPostCheck,
			wantErr:    true,
		},
		{
			name: "Error: a check still Running is a bug",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Running}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantReason: workflow.FRPostCheck,
			wantErr:    true,
			wantBug:    true,
		},
	}

	for _, test := range tests {
		f := finalStates{}
		r, unrun, err := f.examineChecks(t.Context(), test.checks)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExamineChecks(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestExamineChecks(%s): got err == %s, want err == nil", test.name, err)
		case err != nil:
			// End logs the final state error only when it is a bug (errors.IsBug).
			if errors.IsBug(err) != test.wantBug {
				t.Errorf("TestExamineChecks(%s): got errors.IsBug(err) == %v, want %v", test.name, errors.IsBug(err), test.wantBug)
			}
		}
		if r != test.wantReason {
			t.Errorf("TestExamineChecks(%s): got reason %v, want %v", test.name, r, test.wantReason)
		}
		if unrun != test.wantUnrun {
			t.Errorf("TestExamineChecks(%s): got unrun %v, want %v", test.name, unrun, test.wantUnrun)
		}
	}
}

// TestFinalStates runs the whole finalStates machine. It includes a regression test: a failed block sends the Plan
// from BlockEnd to PlanDeferredActions, skipping PlanPostChecks, so the Plan's PostChecks stay NotStarted.
// examineChecks called that a bug, so the Plan failed with FRPostCheck instead of FRBlock and End logged a bug.
func TestFinalStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		post       workflow.Status
		block      workflow.Status
		wantStatus workflow.Status
		wantReason workflow.FailureReason
		wantErr    bool
		wantBug    bool
	}{
		{
			name:       "Success: completed PostChecks and a completed block complete the Plan",
			post:       workflow.Completed,
			block:      workflow.Completed,
			wantStatus: workflow.Completed,
		},
		{
			name:       "Error: a failed block fails the Plan with FRBlock",
			post:       workflow.Completed,
			block:      workflow.Failed,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRBlock,
			wantErr:    true,
		},
		{
			// The NotStarted PostChecks are not a second input: they follow from the failed block, which sends the Plan
			// past PlanPostChecks. This row checks that pair is not taken for a bug.
			name:       "Error: PostChecks skipped because a block failed fail the Plan with FRBlock, not a bug",
			post:       workflow.NotStarted,
			block:      workflow.Failed,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRBlock,
			wantErr:    true,
		},
		{
			name:       "Error: PostChecks that never ran though every block completed are a bug",
			post:       workflow.NotStarted,
			block:      workflow.Completed,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPostCheck,
			wantErr:    true,
			wantBug:    true,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			PreChecks:  newChecksWithState(&workflow.State{Status: workflow.Completed}),
			PostChecks: newChecksWithState(&workflow.State{Status: test.post}),
			Blocks:     []*workflow.Block{newBlockWithState(&workflow.State{Status: test.block})},
		}
		plan.State.Set(workflow.State{Status: workflow.Running})

		f := finalStates{}
		_, err := statemachine.Run("finalStates", statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan}, Next: f.start})
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestFinalStates(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestFinalStates(%s): got err == %s, want err == nil", test.name, err)
		case err != nil:
			// End logs the final state error only when it is a bug (errors.IsBug).
			if errors.IsBug(err) != test.wantBug {
				t.Errorf("TestFinalStates(%s): got errors.IsBug(err) == %v, want %v", test.name, errors.IsBug(err), test.wantBug)
			}
		}
		if got := plan.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestFinalStates(%s): got status %v, want %v", test.name, got, test.wantStatus)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestFinalStates(%s): got reason %v, want %v", test.name, plan.Reason, test.wantReason)
		}
	}
}
