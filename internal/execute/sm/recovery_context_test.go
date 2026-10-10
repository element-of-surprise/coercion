package sm

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// TestExecuteBlockRecovered is a regression test: ExecuteBlock ran a recovered block's Running sequences to
// completion before the block's bypass, pre and continuous checks were restarted, so they ran unguarded by the
// continuous checks. A recovered block must restart its checks first and resume its sequences under them, with the
// request's Context values (pool, tracing, plan ID) still reaching the actions.
func TestExecuteBlockRecovered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// contAction names the ContChecks action; "error" makes the checks fail.
		contAction string
		wantRuns   int64
		wantStatus workflow.Status
		// wantSeq is the recovered sequence's status when the state machine is done. It must have an end time.
		wantSeq workflow.Status
		wantErr bool
	}{
		{
			name:       "Success: passing ContChecks let the recovered sequence resume and complete the block",
			contAction: "pass",
			wantRuns:   1,
			wantStatus: workflow.Completed,
			wantSeq:    workflow.Completed,
		},
		{
			name:       "Error: failing ContChecks fail the recovered block before its sequence resumes",
			contAction: "error",
			wantStatus: workflow.Failed,
			wantSeq:    workflow.Failed,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		planID := uuid.New()
		var runs atomic.Int64
		var gotID sync.AtomicValue[uuid.UUID]

		s := &States{
			store:            &fakeUpdater{},
			testChecksRunner: fakeRunChecksOnce,
			testActionRunner: func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
				runs.Add(1)
				gotID.Store(context.PlanID(ctx))
				return nil
			},
		}

		cont := newChecksWithStateAndActionsRecov(&workflow.State{}, []*workflow.Action{{Name: test.contAction}})
		cont.Delay = time.Hour
		seq := newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{{Name: "action"}})
		// PreChecks that had not run at the crash: BlockPreChecks runs the ContChecks once with them, before any
		// sequence.
		pre := newChecksWithStateAndActionsRecov(&workflow.State{}, []*workflow.Action{{Name: "pass"}})
		b := newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{seq}, nil, pre, cont, nil)
		b.ToleratedFailures = 0
		b.Concurrency = 1
		plan := &workflow.Plan{ID: planID, Blocks: []*workflow.Block{b}}
		plan.State.Set(workflow.State{Status: workflow.Running})

		req := statemachine.Request[Data]{
			Ctx: context.SetPlanID(t.Context(), planID),
			Data: Data{
				Plan:      plan,
				recovered: true,
				blocks:    []block{{block: b, contCheckResult: make(chan error, 1)}},
			},
			Next: s.ExecuteBlock,
		}
		_, err := statemachine.Run("TestExecuteBlockRecovered", req)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExecuteBlockRecovered(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestExecuteBlockRecovered(%s): got err == %s, want err == nil", test.name, err)
		}

		if got := runs.Load(); got != test.wantRuns {
			t.Errorf("TestExecuteBlockRecovered(%s): the recovered sequence's action ran %d times, want %d", test.name, got, test.wantRuns)
		}
		if got := b.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestExecuteBlockRecovered(%s): got block status %v, want %v", test.name, got, test.wantStatus)
		}
		// A sequence that was Running at the crash and is not resumed must be settled, not left Running for good.
		if got := seq.State.Get(); got.Status != test.wantSeq || got.End.IsZero() {
			t.Errorf("TestExecuteBlockRecovered(%s): got recovered sequence state %v, want %v with an end time", test.name, got, test.wantSeq)
		}
		if test.wantRuns > 0 {
			if got := gotID.Load(); got != planID {
				t.Errorf("TestExecuteBlockRecovered(%s): got plan ID %v in the recovered sequence's Context, want %v", test.name, got, planID)
			}
		}
	}
}
