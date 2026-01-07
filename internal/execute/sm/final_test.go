package sm

import (
	"errors"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
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

func TestPlanChecks(t *testing.T) {
	t.Parallel()

	finals := finalStates{}

	tests := []struct {
		name       string
		checks     [3]*workflow.Checks
		wantNext   statemachine.State[Data]
		wantReason workflow.FailureReason
		wantErr    bool
	}{
		{
			name: "all checks pass",
			checks: [3]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantNext: finals.blocks,
		},
		{
			name: "not all checks pass",
			checks: [3]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
			},
			wantErr:    true,
			wantNext:   finals.end,
			wantReason: workflow.FRPreCheck,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			PreChecks:  test.checks[0],
			ContChecks: test.checks[1],
			PostChecks: test.checks[2],
		}
		plan.State.Set(workflow.State{Status: workflow.Running})

		req := finals.planChecks(statemachine.Request[Data]{Data: Data{Plan: plan}})
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestPlanChecks(%s): got err == nil, want err != nil", test.name)
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestPlanChecks(%s): got err == %v, want err == nil", test.name, req.Err)
		}

		if methodName(req.Next) != methodName(test.wantNext) {
			t.Errorf("TestPlanChecks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(test.wantNext))
		}

		if plan.Reason != test.wantReason {
			t.Errorf("TestPlanChecks(%s): got reason == %v, want reason == %v", test.name, plan.Reason, test.wantReason)
		}
	}
}

func TestBlocks(t *testing.T) {
	t.Parallel()

	finals := finalStates{}

	tests := []struct {
		name        string
		block       *workflow.Block
		wantNext    statemachine.State[Data]
		wantErr     bool
		internalErr bool
	}{
		{
			name:     "block is completed",
			block:    newBlockWithState(&workflow.State{Status: workflow.Completed}),
			wantNext: finals.end,
		},
		{
			name:    "block is failed",
			block:   newBlockWithState(&workflow.State{Status: workflow.Failed}),
			wantErr: true,
		},
		{
			name:        "block is in an invalid state",
			block:       newBlockWithState(&workflow.State{Status: workflow.Running}),
			wantErr:     true,
			internalErr: true,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			Blocks: []*workflow.Block{test.block},
		}
		plan.State.Set(workflow.State{Status: workflow.Running})

		req := finals.blocks(statemachine.Request[Data]{Data: Data{Plan: plan}})
		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestBlocks(%s): got err == nil, want err != nil", test.name)
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestBlocks(%s): got err != %v, want err == nil", test.name, req.Err)
		case req.Err != nil:
			if errors.Is(req.Err, ErrInternalFailure) != test.internalErr {
				t.Errorf("TestBlocks(%s): got err == %v, want err == %v", test.name, req.Err, ErrInternalFailure)
			}
		}
		if test.wantNext != nil {
			if methodName(req.Next) != methodName(test.wantNext) {
				t.Errorf("TestBlocks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(test.wantNext))
			}
		}
	}
}

func TestFinalsEnd(t *testing.T) {
	t.Parallel()

	plan := &workflow.Plan{}
	plan.State.Set(workflow.State{Status: workflow.Running})
	req := statemachine.Request[Data]{Data: Data{Plan: plan}}
	f := finalStates{}
	req = f.end(req)
	if req.Data.Plan.State.Get().Status != workflow.Completed {
		t.Errorf("TestEnd: expected plan to be completed, got %s", req.Data.Plan.State.Get().Status)
	}
}

// TestPlanChecksFailurePreservesStatus tests that when planChecks fails,
// the plan ends with Failed status after going through end().
// This tests the fix for a bug where end() unconditionally set the status
// to Completed, overwriting the Failed status set by planChecks.
func TestPlanChecksFailurePreservesStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checks     [4]*workflow.Checks
		wantStatus workflow.Status
		wantReason workflow.FailureReason
	}{
		{
			name: "Success: Failed PreChecks results in Failed plan after end()",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				nil,
				nil,
				nil,
			},
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPreCheck,
		},
		{
			name: "Success: Failed ContChecks results in Failed plan after end()",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				nil,
				nil,
			},
			wantStatus: workflow.Failed,
			wantReason: workflow.FRContCheck,
		},
		{
			name: "Success: Failed PostChecks results in Failed plan after end()",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
				nil,
			},
			wantStatus: workflow.Failed,
			wantReason: workflow.FRPostCheck,
		},
		{
			name: "Success: Failed DeferredChecks results in Failed plan after end()",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Failed}),
			},
			wantStatus: workflow.Failed,
			wantReason: workflow.FRDeferredCheck,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			PreChecks:      test.checks[0],
			ContChecks:     test.checks[1],
			PostChecks:     test.checks[2],
			DeferredChecks: test.checks[3],
		}
		plan.State.Set(workflow.State{Status: workflow.Running})

		finals := finalStates{}

		// First run planChecks which should set Failed
		req := finals.planChecks(statemachine.Request[Data]{Data: Data{Plan: plan}})

		// Verify planChecks set the failure
		if plan.State.Get().Status != workflow.Failed {
			t.Errorf("TestPlanChecksFailurePreservesStatus(%s): after planChecks, got status = %v, want %v",
				test.name, plan.State.Get().Status, workflow.Failed)
		}

		// Now run end() which should preserve the Failed status
		req = finals.end(req)

		// Verify end() did NOT overwrite the Failed status
		if plan.State.Get().Status != test.wantStatus {
			t.Errorf("TestPlanChecksFailurePreservesStatus(%s): after end(), got status = %v, want %v",
				test.name, plan.State.Get().Status, test.wantStatus)
		}

		if plan.Reason != test.wantReason {
			t.Errorf("TestPlanChecksFailurePreservesStatus(%s): got reason = %v, want %v",
				test.name, plan.Reason, test.wantReason)
		}
	}
}

func TestExamineChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		checks      [4]*workflow.Checks
		wantReason  workflow.FailureReason
		wantErr     bool
		internalErr bool
	}{
		{
			name: "all checks pass",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
		},
		{
			name: "all checks pass, but we have a nil check",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				nil,
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
		},
		{
			name: "pre-check fails",
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
			name: "cont-check fails",
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
			name: "post-check fails",
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
			name: "check in an unexpected state",
			checks: [4]*workflow.Checks{
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
				newChecksWithState(&workflow.State{Status: workflow.Running}),
				newChecksWithState(&workflow.State{Status: workflow.Completed}),
			},
			wantReason:  workflow.FRPostCheck,
			wantErr:     true,
			internalErr: true,
		},
	}

	for _, test := range tests {
		f := finalStates{}
		r, err := f.examineChecks(test.checks)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExamineChecks(%s): got nil error, want error", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestExamineChecks(%s): got error %v, want nil", test.name, err)
		case err != nil:
			if errors.Is(err, ErrInternalFailure) != test.internalErr {
				t.Errorf("TestExamineChecks(%s): got error %v, want internal error", test.name, err)
			}
		}
		if r != test.wantReason {
			t.Errorf("TestExamineChecks(%s): got reason %v, want %v", test.name, r, test.wantReason)
		}
	}
}
