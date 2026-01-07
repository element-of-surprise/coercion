package sm

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/noop"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/gostdlib/base/statemachine"
)

func TestPlanStart(t *testing.T) {
	t.Parallel()

	plan := &workflow.Plan{
		Blocks: []*workflow.Block{
			{},
			{},
		},
	}
	plan.State.Set(workflow.State{})

	req := statemachine.Request[Data]{
		Ctx: context.Background(),
		Data: Data{
			Plan: plan,
		},
	}

	vault := &fakeUpdater{}
	states := States{
		store: vault,
	}

	req = states.Start(req)
	if len(req.Data.blocks) != 2 {
		t.Errorf("TestPlanStart: req.blocks: expected 2 to be created, got %d", len(req.Data.blocks))
	}
	if req.Data.contCheckResult == nil {
		t.Errorf("TestPlanStart: req.Data.contCheckResult == nil, expect != nil")
	}
	if req.Data.Plan.State.Get().Status != workflow.Running {
		t.Errorf("TestPlanStart: Plan.State.Status is %s, want %s", req.Data.Plan.State.Get().Status, workflow.Running)
	}
	if req.Data.Plan.State.Get().Start.IsZero() {
		t.Errorf("TestPlanStart: Plan.State.Start did not get set")
	}

	if vault.calls.Load() != 1 {
		t.Errorf("TestPlanStart: storage.Create() did not get called")
	}
	if methodName(req.Next) != methodName(states.PlanBypassChecks) {
		t.Errorf("TestPlanStart: expected req.Next == %s, got %s", methodName(req.Next), methodName(states.PlanBypassChecks))
	}
}

func TestPlanBypassChecks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name          string
		plan          *workflow.Plan
		checksRunner  checksRunner
		wantNextState statemachine.State[Data]
	}{
		{
			name: "BypassChecks are nil",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				return p
			}(),
			wantNextState: states.PlanPreChecks,
		},
		{
			name: "BypassChecks succeed",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				c := &workflow.Checks{}
				c.State.Set(workflow.State{})
				p.BypassChecks = c
				return p
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return nil
			},
			wantNextState: states.End,
		},
		{
			name: "BypassChecks fail",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				pre := &workflow.Checks{}
				pre.State.Set(workflow.State{})
				cont := &workflow.Checks{}
				cont.State.Set(workflow.State{})
				p.PreChecks = pre
				p.ContChecks = cont
				return p
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return fmt.Errorf("error")
			},
			wantNextState: states.PlanPreChecks,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				Plan: test.plan,
			},
		}

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.PlanBypassChecks(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockBypassChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestPlanPreChecks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name          string
		plan          *workflow.Plan
		checksRunner  checksRunner
		wantNextState statemachine.State[Data]
	}{
		{
			name:          "PreChecks and ContChecks are nil",
			plan:          &workflow.Plan{},
			wantNextState: states.PlanStartContChecks,
		},
		{
			name: "PreChecks and ContChecks succeed",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				pre := &workflow.Checks{}
				pre.State.Set(workflow.State{})
				p.PreChecks = pre
				p.ContChecks = &workflow.Checks{}
				return p
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return nil
			},
			wantNextState: states.PlanStartContChecks,
		},
		{
			name: "PreChecks or ContChecks fail",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				pre := &workflow.Checks{}
				pre.State.Set(workflow.State{})
				p.PreChecks = pre
				p.ContChecks = &workflow.Checks{}
				return p
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return fmt.Errorf("error")
			},
			wantNextState: states.PlanDeferredChecks,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				Plan: test.plan,
			},
		}

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.PlanPreChecks(req)
		if methodName(test.wantNextState) == methodName(states.End) {
			if req.Data.err == nil {
				t.Errorf("TestPlanPreChecks(%s): req.Data.err = nil, want error", test.name)
			}
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockPreChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestPlanStartContChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action *workflow.Action
	}{
		{
			name: "ContChecks == nil",
		},
		{
			name: "ContChecks != nil",
			action: func() *workflow.Action {
				a := &workflow.Action{Plugin: plugins.Name, Req: plugins.Req{Arg: "error"}}
				a.State.Set(workflow.State{})
				return a
			}(),
		},
	}

	plug := &plugins.Plugin{AlwaysRespond: true, IsCheckPlugin: true}
	reg := registry.New()
	reg.Register(plug)

	for _, test := range tests {
		plug.ResetCounts()

		states := &States{store: &fakeUpdater{}}

		var contChecks *workflow.Checks
		if test.action != nil {
			contChecks = &workflow.Checks{
				Actions: []*workflow.Action{test.action},
			}
			contChecks.State.Set(workflow.State{})
		}

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				Plan: &workflow.Plan{
					ContChecks: contChecks,
				},
				contCheckResult: make(chan error, 1),
			},
		}

		req = states.PlanStartContChecks(req)
		if test.action != nil {
			<-req.Data.contCheckResult
			if req.Data.contCancel == nil {
				t.Errorf("TestPlanStartContChecks(%s): got req.Data.contCancel == nil, want req.Data.contCancel != nil", test.name)
			}
		}
		if methodName(req.Next) != methodName(states.ExecuteBlock) {
			t.Errorf("TestPlanStartContChecks(%s): got req.Next == %s, want req.Next == %s", test.name, methodName(req.Next), methodName(states.ExecuteSequences))
		}
	}
}

func TestExecuteBlocks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name          string
		block         block
		wantNextState statemachine.State[Data]
	}{
		{
			name:          "No more blocks",
			wantNextState: states.PlanPostChecks,
		},
		{
			name: "Have a block",
			block: block{
				block: func() *workflow.Block {
					b := &workflow.Block{}
					b.State.Set(workflow.State{})
					return b
				}(),
			},
			wantNextState: states.BlockBypassChecks,
		},
	}

	for _, test := range tests {
		states := &States{store: &fakeUpdater{}}
		var blocks []block
		if test.block.block != nil {
			blocks = append(blocks, test.block)
		}
		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				blocks: blocks,
			},
		}
		req = states.ExecuteBlock(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestExecuteBlocks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if len(req.Data.blocks) != 0 {
			if req.Data.blocks[0].block.State.Get().Status != workflow.Running {
				t.Errorf("TestExecuteBlocks(%s): got block state = %v, want %v", test.name, req.Data.blocks[0].block.State.Get().Status, workflow.Running)
			}
		}
	}
}

func TestBlockBypassChecks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name            string
		block           *workflow.Block
		checksRunner    checksRunner
		wantBlockStatus workflow.Status
		wantNextState   statemachine.State[Data]
	}{
		{
			name: "BypassChecks are nil",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				return b
			}(),
			wantNextState: states.BlockPreChecks,
		},
		{
			name: "BypassChecks succeed",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				c := &workflow.Checks{}
				c.State.Set(workflow.State{})
				b.BypassChecks = c
				return b
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return nil
			},
			wantBlockStatus: workflow.Completed,
			wantNextState:   states.BlockEnd,
		},
		{
			name: "BypassChecks fail",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				c := &workflow.Checks{}
				c.State.Set(workflow.State{})
				b.BypassChecks = c
				return b
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return fmt.Errorf("error")
			},
			wantBlockStatus: workflow.Running,
			wantNextState:   states.BlockPreChecks,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				blocks: []block{{block: test.block}},
			},
		}
		test.block.State.Set(workflow.State{})

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.BlockBypassChecks(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockBypassChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestBlockPreChecks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name            string
		block           *workflow.Block
		checksRunner    checksRunner
		wantBlockStatus workflow.Status
		wantNextState   statemachine.State[Data]
	}{
		{
			name: "PreChecks and ContChecks are nil",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				return b
			}(),
			wantNextState: states.BlockStartContChecks,
		},
		{
			name: "PreChecks and ContChecks succeed",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				pre := &workflow.Checks{}
				pre.State.Set(workflow.State{})
				cont := &workflow.Checks{}
				cont.State.Set(workflow.State{})
				b.PreChecks = pre
				b.ContChecks = cont
				return b
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return nil
			},
			wantNextState: states.BlockStartContChecks,
		},
		{
			name: "PreChecks or ContChecks fail",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				pre := &workflow.Checks{}
				pre.State.Set(workflow.State{})
				cont := &workflow.Checks{}
				cont.State.Set(workflow.State{})
				b.PreChecks = pre
				b.ContChecks = cont
				return b
			}(),
			checksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				return fmt.Errorf("error")
			},
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.BlockDeferredChecks,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				blocks: []block{{block: test.block}},
			},
		}
		test.block.State.Set(workflow.State{})

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.BlockPreChecks(req)
		if test.wantBlockStatus != workflow.NotStarted {
			if req.Data.err == nil {
				t.Errorf("TestBlockPreChecks(%s): req.Data.err = nil, want error", test.name)
			}
			if req.Data.blocks[0].block.State.Get().Status != test.wantBlockStatus {
				t.Errorf("TestBlockPreChecks(%s): got block status = %v, want %v", test.name, req.Data.blocks[0].block.State.Get().Status, test.wantBlockStatus)
			}
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockPreChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestBlockStartContChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action *workflow.Action
	}{
		{
			name: "ContChecks == nil",
		},
		{
			name: "ContChecks != nil",
			action: func() *workflow.Action {
				a := &workflow.Action{Plugin: plugins.Name, Req: plugins.Req{Arg: "error"}}
				a.State.Set(workflow.State{})
				return a
			}(),
		},
	}

	plug := &plugins.Plugin{AlwaysRespond: true, IsCheckPlugin: true}
	reg := registry.New()
	reg.Register(plug)

	for _, test := range tests {
		plug.ResetCounts()

		states := &States{store: &fakeUpdater{}}

		var contChecks *workflow.Checks
		if test.action != nil {
			contChecks = &workflow.Checks{
				Actions: []*workflow.Action{test.action},
			}
			contChecks.State.Set(workflow.State{})
		}

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				blocks: []block{
					{
						block: &workflow.Block{
							ContChecks: contChecks,
						},
						contCheckResult: make(chan error, 1),
					},
				},
			},
		}

		req = states.BlockStartContChecks(req)
		if test.action != nil {
			<-req.Data.blocks[0].contCheckResult
		}
		if methodName(req.Next) != methodName(states.ExecuteSequences) {
			t.Errorf("TestBlockStartContChecks(%s): got req.Next == %s, want req.Next == %s", test.name, methodName(req.Next), methodName(states.ExecuteSequences))
		}
	}
}

// TestExecuteSequences tests ExecuteSequences in a variety of scenarios with a concurrency of 1.
// We tests concurrency for this in TestExecuteConcurrentSequences.
func TestExecuteSequences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	failedAction := &workflow.Action{Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Sleep: 10 * time.Millisecond, Arg: "error"}}
	sequenceWithFailure := &workflow.Sequence{Actions: []*workflow.Action{failedAction}}

	successAction := &workflow.Action{Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Sleep: 10 * time.Millisecond, Arg: "success"}}
	sequenceWithSuccess := &workflow.Sequence{Actions: []*workflow.Action{successAction}}

	tests := []struct {
		name            string
		block           *workflow.Block
		contCheckFail   bool
		wantPluginCalls int
		wantStatus      workflow.Status
		wantErr         bool
	}{
		{
			name: "Success: Tolerated Failures are unlimited, everything fails",
			block: &workflow.Block{
				ToleratedFailures: -1,
				Concurrency:       1,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
				},
			},
			wantPluginCalls: 3,
		},
		{
			name: "Error: Exceed tolerated failures by after success 1",
			block: &workflow.Block{
				ToleratedFailures: 1,
				Concurrency:       1,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...),
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
				},
			},
			wantPluginCalls: 3,
			wantStatus:      workflow.Failed,
			wantErr:         true,
		},
		{
			name: "Error: Exceed tolerated failures before success",
			block: &workflow.Block{
				ToleratedFailures: 1,
				Concurrency:       1,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...), // We should die after this.
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...), // Never should be called.
				},
			},
			wantPluginCalls: 2,
			wantStatus:      workflow.Failed,
			wantErr:         true,
		},
		{
			name: "Error: Continuous Checks fail",
			block: &workflow.Block{
				ToleratedFailures: 1,
				Concurrency:       1,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...), // Never should be called.
				},
			},
			contCheckFail: true,
			wantStatus:    workflow.Failed,
			wantErr:       true,
		},
		{
			name: "Success",
			block: &workflow.Block{
				ToleratedFailures: 0,
				Concurrency:       1,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...),
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...),
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...),
				},
			},
			wantPluginCalls: 3,
		},
	}

	plug := &plugins.Plugin{AlwaysRespond: true}
	reg := registry.New()
	reg.Register(plug)

	for _, test := range tests {
		plug.ResetCounts()

		states := States{
			registry: reg,
			store:    &fakeUpdater{},
		}

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
		}
		req.Data.blocks = []block{{block: test.block}}
		test.block.State.Set(workflow.State{})
		if test.contCheckFail {
			req.Data.contCheckResult = make(chan error, 1)
			req.Data.contCheckResult <- fmt.Errorf("error")
			close(req.Data.contCheckResult)
		}

		for _, seq := range test.block.Sequences {
			seq.State.Set(workflow.State{})
			for _, action := range seq.Actions {
				action.State.Set(workflow.State{})
			}
		}
		req = states.ExecuteSequences(req)
		if test.wantErr != (req.Data.err != nil) {
			t.Errorf("TestExecuteSequences(%s): got err == %v, wantErr == %v", test.name, req.Data.err, test.wantErr)
		}
		if test.wantStatus != test.block.State.Get().Status {
			t.Errorf("TestExecuteSequences(%s): got status == %v, wantStatus == %v", test.name, test.block.State.Get().Status, test.wantStatus)
		}
		if plug.Calls.Load() != int64(test.wantPluginCalls) {
			t.Errorf("TestExecuteSequences(%s): got plugin calls == %v, want == %v", test.name, plug.Calls.Load(), test.wantPluginCalls)
		}
	}
}

// TestExecuteSequencesConcurrency test the concurrency limits for blocks to make sure it works.
func TestExecuteSequencesConcurrency(t *testing.T) {
	t.Parallel()

	build, err := builder.New("test", "test")
	if err != nil {
		panic(err)
	}

	build.AddBlock(
		builder.BlockArgs{
			Name:        "block0",
			Descr:       "block0",
			Concurrency: 3,
		},
	)

	for i := 0; i < 10; i++ {
		build.AddSequence(
			&workflow.Sequence{
				Name:  "seq",
				Descr: "seq",
			},
		)
		build.AddAction(
			&workflow.Action{
				Name:    "action",
				Descr:   "action",
				Plugin:  plugins.Name,
				Timeout: 10 * time.Second,
				Req:     plugins.Req{Sleep: 100 * time.Millisecond},
			},
		)
		build.Up()
	}

	plug := &plugins.Plugin{AlwaysRespond: true}
	reg := registry.New()
	reg.Register(plug)

	states := States{
		registry: reg,
		store:    &fakeUpdater{},
	}

	p, err := build.Plan()
	if err != nil {
		panic(err)
	}

	for _, seq := range p.Blocks[0].Sequences {
		seq.State.Set(workflow.State{})
		for _, action := range seq.Actions {
			action.State.Set(workflow.State{})
		}
	}

	req := statemachine.Request[Data]{
		Ctx: context.Background(),
		Data: Data{
			Plan:   p,
			blocks: []block{{block: p.Blocks[0]}},
		},
	}
	states.ExecuteSequences(req) // Ignore return value

	if plug.MaxCount.Load() != 3 {
		t.Errorf("TestExecuteSequencesConcurrency: expected MaxCount == 3, got %d", plug.MaxCount.Load())
	}
}

func TestBlockPostChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		block      block
		wantErr    bool
		wantStatus workflow.Status
	}{
		{
			name: "Success: No post checks",
			block: block{
				block: &workflow.Block{},
			},
			wantStatus: workflow.Running,
		},
		{
			name: "Error: PostChecks fail",
			block: block{
				block: func() *workflow.Block {
					b := &workflow.Block{}
					c := &workflow.Checks{Actions: []*workflow.Action{{Name: "error"}}}
					c.State.Set(workflow.State{})
					b.PostChecks = c
					return b
				}(),
			},
			wantStatus: workflow.Failed,
			wantErr:    true,
		},
		{
			name: "Success: Post checks succeed",
			block: block{
				block: func() *workflow.Block {
					b := &workflow.Block{}
					c := &workflow.Checks{Actions: []*workflow.Action{{Name: "success"}}}
					c.State.Set(workflow.State{})
					b.PostChecks = c
					return b
				}(),
			},
			wantStatus: workflow.Running,
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		reg := registry.New()
		store, err := sqlite.New(ctx, "", reg, sqlite.WithInMemory())
		if err != nil {
			panic(err)
		}
		states := &States{
			testChecksRunner: fakeRunChecksOnce,
			store:            store,
		}
		test.block.block.State.Set(workflow.State{Status: workflow.Running})

		req := statemachine.Request[Data]{
			Ctx: context.Background(),
			Data: Data{
				blocks: []block{test.block},
			},
		}

		req = states.BlockPostChecks(req)

		if test.wantErr != (req.Data.err != nil) {
			t.Errorf("TestBlockPostChecks(%s): got err == %v, want err == %v", test.name, req.Data.err, test.wantErr)
		}
		if req.Data.blocks[0].block.State.Get().Status != test.wantStatus {
			t.Errorf("TestBlockPostChecks(%s): got status == %v, want status == %v", test.name, req.Data.blocks[0].block.State.Get().Status, test.wantStatus)
		}
		if methodName(req.Next) != methodName(states.BlockDeferredChecks) {
			t.Errorf("TestBlockPostChecks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(states.BlockEnd))
		}
	}
}

func TestBlockEnd(t *testing.T) {
	t.Parallel()

	// This is simply used to get the name next State we expect.
	// We create new ones in the tests to avoid having a shared one.
	states := &States{}

	tests := []struct {
		name            string
		data            Data
		contCheckResult error
		wantErr         bool
		wantBlockStatus workflow.Status
		wantNextState   statemachine.State[Data]
		wantBlocksLen   int
	}{
		{
			name: "Error: contchecks failure",
			data: Data{
				blocks: []block{{block: &workflow.Block{ContChecks: &workflow.Checks{}}}},
			},
			contCheckResult: fmt.Errorf("error"),
			wantErr:         true,
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.PlanDeferredChecks,
			wantBlocksLen:   1,
		},
		{
			name: "Success: bypasschecks success",
			data: Data{
				blocks: []block{{block: func() *workflow.Block {
					b := &workflow.Block{}
					c := &workflow.Checks{}
					c.State.Set(workflow.State{Status: workflow.Completed})
					b.BypassChecks = c
					return b
				}()}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState:   states.ExecuteBlock,
		},
		{
			name: "Success: no more blocks",
			data: Data{
				blocks: []block{{}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState:   states.ExecuteBlock,
			wantBlocksLen:   0,
		},
		{
			name: "Success: more blocks",
			data: Data{
				blocks: []block{{}, {}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState:   states.ExecuteBlock,
			wantBlocksLen:   1,
		},
	}

	for _, test := range tests {
		states := &States{
			store: &fakeUpdater{},
		}
		for i, block := range test.data.blocks {
			if block.block == nil {
				b := &workflow.Block{}
				b.State.Set(workflow.State{Status: workflow.Running})
				block.block = b
			} else {
				block.block.State.Set(workflow.State{Status: workflow.Running})
			}
			test.data.blocks[i] = block
		}
		var ctx context.Context
		ctx, test.data.blocks[0].contCancel = context.WithCancel(context.Background())

		req := statemachine.Request[Data]{
			Ctx:  context.Background(),
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
		if block.State.Get().Status != test.wantBlockStatus {
			t.Errorf("TestBlockEnd(%s): got block status == %v, want block status == %v", test.name, block.State.Get().Status, test.wantBlockStatus)
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockEnd(%s): got next state == %v, want next state == %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if len(req.Data.blocks) != test.wantBlocksLen {
			t.Errorf("TestBlockEnd(%s): got blocks len == %v, want blocks len == %v", test.name, len(req.Data.blocks), test.wantBlocksLen)
		}
		if ctx.Err() == nil {
			if block.BypassChecks == nil {
				t.Errorf("TestBlockEnd(%s): context for continuous checks should have been cancelled", test.name)
			} else if block.BypassChecks.GetState().Status != workflow.Completed {
				t.Errorf("TestBlockEnd(%s): context for continuous checks should have been cancelled", test.name)
			}
		}
		if states.store.(*fakeUpdater).calls.Load() != 1 {
			t.Errorf("TestBlockEnd(%s): got store calls == %v, want store calls == 1", test.name, states.store.(*fakeUpdater).calls.Load())
		}
	}
}

func TestPlanPostChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		plan            *workflow.Plan
		contCheckResult error
		wantErr         bool
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
			contCheckResult: fmt.Errorf("error"),
			wantErr:         true,
		},
		{
			name: "Error: PostChecks fail",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				c := &workflow.Checks{Actions: []*workflow.Action{{Name: "error"}}}
				c.State.Set(workflow.State{})
				p.PostChecks = c
				return p
			}(),
			wantErr: true,
		},
		{
			name: "Success: Cont and Post checks succeed",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.ContChecks = &workflow.Checks{}
				c := &workflow.Checks{Actions: []*workflow.Action{{Name: "success"}}}
				c.State.Set(workflow.State{})
				p.PostChecks = c
				return p
			}(),
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		store := &noop.Vault{}

		states := &States{
			testChecksRunner: fakeRunChecksOnce,
			store:            store,
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
				Plan:            test.plan,
				contCheckResult: results,
				contCancel:      cancel,
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

func TestEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dataErr := fmt.Errorf("error")

	plan := &workflow.Plan{}
	plan.State.Set(workflow.State{Status: workflow.Running})
	data := Data{
		Plan:       plan,
		contCancel: cancel,
		err:        dataErr,
	}

	states := &States{
		store: &fakeUpdater{},
	}

	req := statemachine.Request[Data]{Data: data, Ctx: context.Background()}
	req = states.End(req)

	if ctx.Err() == nil {
		t.Errorf("TestEnd: contChecks context should have been cancelled")
	}
	if data.Plan.State.Get().Status != workflow.Completed {
		t.Errorf("TestEnd: plan status should have been set to completed")
	}
	if data.Plan.State.Get().End.IsZero() {
		t.Errorf("TestEnd: plan end time should have been set")
	}
	if req.Err == nil {
		t.Errorf("TestEnd: request error should have been set")
	}
	if req.Next != nil {
		t.Errorf("TestEnd: next state should have been nil")
	}
	if states.store.(*fakeUpdater).calls.Load() != 1 {
		t.Errorf("TestEnd: store.UpdatePlan() should have been called")
	}
}

// TestRuntimeUpdate tests the runtimeUpdate function to ensure it correctly
// updates the plan when more than 5 minutes have passed since the last update.
// This tests the fix for a bug where the time comparison was backwards
// (LastUpdate.Sub(now) instead of now.Sub(LastUpdate)).
func TestRuntimeUpdate(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		lastUpdate time.Time
		now        time.Time
		wantUpdate bool
	}{
		{
			name:       "Success: Update triggered when 6 minutes have passed",
			lastUpdate: baseTime,
			now:        baseTime.Add(6 * time.Minute),
			wantUpdate: true,
		},
		{
			name:       "Success: No update when only 4 minutes have passed",
			lastUpdate: baseTime,
			now:        baseTime.Add(4 * time.Minute),
			wantUpdate: false,
		},
		{
			name:       "Success: Update triggered when 5 minutes and 1 second have passed",
			lastUpdate: baseTime,
			now:        baseTime.Add(5*time.Minute + time.Second),
			wantUpdate: true,
		},
		{
			name:       "Success: No update when exactly 5 minutes have passed",
			lastUpdate: baseTime,
			now:        baseTime.Add(5 * time.Minute),
			wantUpdate: false,
		},
	}

	for _, test := range tests {
		updater := &fakeUpdater{}
		states := &States{
			store: updater,
			nower: func() time.Time { return test.now },
		}

		plan := &workflow.Plan{}
		plan.State.Set(workflow.State{Status: workflow.Running, Start: test.lastUpdate})

		states.runtimeUpdate(context.Background(), plan)

		gotUpdate := updater.calls.Load() > 0
		if gotUpdate != test.wantUpdate {
			t.Errorf("TestRuntimeUpdate(%s): got update = %v, want %v", test.name, gotUpdate, test.wantUpdate)
		}

		if test.wantUpdate && plan.RuntimeUpdate.Get().IsZero() {
			t.Errorf("TestRuntimeUpdate(%s): expected RuntimeUpdate to be set, but it was zero", test.name)
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
