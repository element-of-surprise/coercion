package sm

import (
	"testing"

	"github.com/google/uuid"
	"github.com/gostdlib/base/statemachine"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// TestHandleRecoveredSeqs is a regression test for handleRecoveredSeqs running recovered sequences
// under a bare context.Background() instead of one derived from req.Ctx, which silently dropped the
// request's pool, tracing, and plan ID. We assert the plan ID set on req.Ctx reaches the recovered
// sequence's action runner.
func TestHandleRecoveredSeqs(t *testing.T) {
	t.Parallel()

	planID := uuid.New()
	gotID := make(chan uuid.UUID, 1)

	s := &States{
		store: &fakeUpdater{},
		testActionRunner: func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
			gotID <- context.PlanID(ctx)
			return nil
		},
	}

	seq := newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{{Name: "action"}})
	block := newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{seq}, nil, nil, nil, nil)

	req := statemachine.Request[Data]{Ctx: context.SetPlanID(context.Background(), planID)}

	s.handleRecoveredSeqs(req, block)

	if got := <-gotID; got != planID {
		t.Errorf("TestHandleRecoveredSeqs: got plan ID %v in recovered sequence context, want %v (context not derived from req.Ctx)", got, planID)
	}
}
