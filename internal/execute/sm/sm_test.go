package sm

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/gostdlib/ops/statemachine"
)

func TestBlockEnd(t *testing.T) {
	t.Parallel()

	// This is simply used to get the name next State we expect.
	// We create new ones in the tests to avoid having a shared one.
	states := &States{}

	tests := []struct {
		name     string
		data    Data
		contCheckResult error
		wantErr  bool
		wantBlockStatus workflow.Status
		wantNextState statemachine.State[Data]
		wantBlocksLen int
	}{
		{
			name: "Error: contchecks failure",
			data: Data{
				blocks: []block{{}},
			},
			contCheckResult: fmt.Errorf("error"),
			wantErr: true,
			wantBlockStatus: workflow.Failed,
			wantNextState: states.End,
			wantBlocksLen: 1,
		},
		{
			name: "Success: no more blocks",
			data: Data{
				blocks: []block{{}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState: states.ExecuteBlock,
			wantBlocksLen: 0,
		},
		{
			name: "Success: more blocks",
			data: Data{
				blocks: []block{{}, {}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState: states.ExecuteBlock,
			wantBlocksLen: 1,
		},
	}

	for _, test := range tests {
		states := &States{
			store: &fakeUpdater{},
		}
		for i, block := range test.data.blocks {
			block.block = &workflow.Block{State: &workflow.State{}}
			test.data.blocks[i] = block
		}
		var ctx context.Context
		ctx, test.data.blocks[0].contCancel = context.WithCancel(context.Background())

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: test.data,
		}

		req.Data.blocks[0].contCheckResult = make(chan error, 1)
		if test.contCheckResult != nil {
			req.Data.blocks[0].contCheckResult = make(chan error, 1)
			req.Data.blocks[0].contCheckResult <- test.contCheckResult
		}
		close(req.Data.blocks[0].contCheckResult)

		// We store this here because blocks is shrunk after the call.
		block := req.Data.blocks[0].block

		req = states.BlockEnd(req)

		if test.wantErr != (req.Data.err != nil) {
			t.Errorf("TestBlockEnd(%s): got err == %v, want err == %v", test.name, req.Data.err, test.wantErr)
		}
		if block.State.Status != test.wantBlockStatus {
			t.Errorf("TestBlockEnd(%s): got block status == %v, want block status == %v", test.name, block.State.Status, test.wantBlockStatus)
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockEnd(%s): got next state == %v, want next state == %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if len(req.Data.blocks) != test.wantBlocksLen {
			t.Errorf("TestBlockEnd(%s): got blocks len == %v, want blocks len == %v", test.name, len(req.Data.blocks), test.wantBlocksLen)
		}
		if ctx.Err() == nil {
			t.Errorf("TestBlockEnd(%s): context for continuous checks should have been cancelled", test.name)
		}
		if states.store.(*fakeUpdater).calls != 1 {
			t.Errorf("TestBlockEnd(%s): got store calls == %v, want store calls == 1", test.name, states.store.(*fakeUpdater).calls)
		}
	}
}

func TestPlanPostChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name 	 string
		plan 	 *workflow.Plan
		contCheckResult error
		wantErr  bool
	}{
		{
			name: "Success: No post checks",
			plan: &workflow.Plan{},
		},
		{
			name: "Error: Continuous checks fail",
			plan: &workflow.Plan{
				ContChecks: &workflow.Checks{},
			},
			contCheckResult:  fmt.Errorf("error"),
			wantErr: true,
		},
		{
			name: "Error: PostChecks fail",
			plan: &workflow.Plan{
				PostChecks: &workflow.Checks{Actions: []*workflow.Action{{Name: "error"}}},
			},
			wantErr: true,
		},
		{
			name: "Success: Cont and Post checks succeed",
			plan: &workflow.Plan{
				ContChecks: &workflow.Checks{},
				PostChecks: &workflow.Checks{Actions: []*workflow.Action{{Name: "success"}}},
			},
		},
	}

	for _, test := range tests {
		states := &States{
			checksRunner: fakeRunChecksOnce,
		}
		// We cancel a context for continuous checks that are running. This
		// is used to simulate that we signal the continuous checks to stop.
		ctx, cancel := context.WithCancel(context.Background())

		// Simulates that we are done waiting for the continuous checks.`
		var results chan error
		if test.plan.ContChecks != nil {
			results = make(chan error, 1)
			if test.contCheckResult != nil {
				results <- test.contCheckResult
			}
			close(results)
		}

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				Plan: test.plan,
				contCheckResult: results,
				contCancel: cancel,
			},
		}

		req = states.PlanPostChecks(req)

		if test.wantErr != (req.Data.err != nil) {
			t.Errorf("TestPlanPostChecks(%s): got err == %v, want err == %v", test.name, req.Data.err, test.wantErr)
		}
		if test.plan.ContChecks != nil {
			if ctx.Err() == nil {
				t.Errorf("TestPlanPostChecks(%s): continuous checks ctx.Err() == nil, want ctx.Err() != nil", test.name)
			}
		}
	}
}

// methodName returns the name of the method of the given value.
func methodName(method any) string {
	if method == nil {
		return "<nil>"
	}
	valueOf := reflect.ValueOf(method)
	switch valueOf.Kind() {
	case reflect.Func:
		return strings.TrimSuffix(strings.TrimSuffix(runtime.FuncForPC(valueOf.Pointer()).Name(), "-fm"), "[...]")
	default:
		return "<not a function>"
	}
}
