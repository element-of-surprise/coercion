package sm

import (
	"errors"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/gostdlib/ops/statemachine"
)

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
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
			wantNext: finals.blocks,
		},
		{
			name: "not all checks pass",
			checks: [3]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Failed}},
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
			State:      &workflow.State{Status: workflow.Running},
		}

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
			block:    &workflow.Block{State: &workflow.State{Status: workflow.Completed}},
			wantNext: finals.end,
		},
		{
			name:    "block is failed",
			block:   &workflow.Block{State: &workflow.State{Status: workflow.Failed}},
			wantErr: true,
		},
		{
			name:        "block is in an invalid state",
			block:       &workflow.Block{State: &workflow.State{Status: workflow.Running}},
			wantErr:     true,
			internalErr: true,
		},
	}

	for _, test := range tests {
		plan := &workflow.Plan{
			Blocks: []*workflow.Block{test.block},
			State:  &workflow.State{Status: workflow.Running},
		}

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

	plan := &workflow.Plan{State: &workflow.State{Status: workflow.Running}}
	req := statemachine.Request[Data]{Data: Data{Plan: plan}}
	f := finalStates{}
	req = f.end(req)
	if req.Data.Plan.State.Status != workflow.Completed {
		t.Errorf("TestEnd: expected plan to be completed, got %s", req.Data.Plan.State.Status)
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
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
		},
		{
			name: "all checks pass, but we have a nil check",
			checks: [4]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Completed}},
				nil,
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
		},
		{
			name: "pre-check fails",
			checks: [4]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Failed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
			wantReason: workflow.FRPreCheck,
			wantErr:    true,
		},
		{
			name: "cont-check fails",
			checks: [4]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Failed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
			wantReason: workflow.FRContCheck,
			wantErr:    true,
		},
		{
			name: "post-check fails",
			checks: [4]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Failed}},
				{State: &workflow.State{Status: workflow.Completed}},
			},
			wantReason: workflow.FRPostCheck,
			wantErr:    true,
		},
		{
			name: "check in an unexpected state",
			checks: [4]*workflow.Checks{
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Completed}},
				{State: &workflow.State{Status: workflow.Running}},
				{State: &workflow.State{Status: workflow.Completed}},
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
