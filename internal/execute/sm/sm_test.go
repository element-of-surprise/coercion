package sm

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/noop"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/values/chans"
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
		Ctx: t.Context(),
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
			name: "Success: nil BypassChecks move to PlanPreChecks",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				return p
			}(),
			wantNextState: states.PlanPreChecks,
		},
		{
			name: "Success: passing BypassChecks skip the Plan to End",
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
			name: "Success: failing BypassChecks move to PlanPreChecks",
			plan: func() *workflow.Plan {
				p := &workflow.Plan{}
				p.State.Set(workflow.State{})
				c := &workflow.Checks{}
				c.State.Set(workflow.State{})
				p.BypassChecks = c
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
			Ctx: t.Context(),
			Data: Data{
				Plan: test.plan,
			},
		}

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.PlanBypassChecks(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestPlanBypassChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

// preChecksPlan returns a Plan whose PreChecks and ContChecks each have one action with the given name, for
// fakeRunChecksOnce: "error" fails the checks.
func preChecksPlan(pre, cont string) *workflow.Plan {
	p := &workflow.Plan{}
	p.PreChecks = &workflow.Checks{Actions: []*workflow.Action{{Name: pre}}}
	p.PreChecks.State.Set(workflow.State{})
	p.ContChecks = &workflow.Checks{Actions: []*workflow.Action{{Name: cont}}}
	p.ContChecks.State.Set(workflow.State{})
	return p
}

// preChecksBlock returns a block whose PreChecks and ContChecks each have one action with the given name, for
// fakeRunChecksOnce: "error" fails the checks.
func preChecksBlock(pre, cont string) *workflow.Block {
	b := &workflow.Block{}
	b.State.Set(workflow.State{})
	b.PreChecks = &workflow.Checks{Actions: []*workflow.Action{{Name: pre}}}
	b.PreChecks.State.Set(workflow.State{})
	b.ContChecks = &workflow.Checks{Actions: []*workflow.Action{{Name: cont}}}
	b.ContChecks.State.Set(workflow.State{})
	return b
}

func TestPlanPreChecks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name          string
		plan          *workflow.Plan
		checksRunner  checksRunner
		wantErr       bool
		wantNextState statemachine.State[Data]
	}{
		{
			name:          "Success: nil PreChecks and ContChecks move to PlanStartContChecks",
			plan:          &workflow.Plan{},
			wantNextState: states.PlanStartContChecks,
		},
		{
			name: "Success: passing PreChecks and ContChecks move to PlanStartContChecks",
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
			name:          "Error: failing PreChecks with passing ContChecks move to PlanDeferredActions",
			plan:          preChecksPlan("error", "pass"),
			checksRunner:  fakeRunChecksOnce,
			wantErr:       true,
			wantNextState: states.PlanDeferredActions,
		},
		{
			name:          "Error: failing ContChecks with passing PreChecks move to PlanDeferredActions",
			plan:          preChecksPlan("pass", "error"),
			checksRunner:  fakeRunChecksOnce,
			wantErr:       true,
			wantNextState: states.PlanDeferredActions,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: t.Context(),
			Data: Data{
				Plan: test.plan,
			},
		}

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.PlanPreChecks(req)
		switch {
		case req.Data.err == nil && test.wantErr:
			t.Errorf("TestPlanPreChecks(%s): got err == nil, want err != nil", test.name)
		case req.Data.err != nil && !test.wantErr:
			t.Errorf("TestPlanPreChecks(%s): got err == %s, want err == nil", test.name, req.Data.err)
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestPlanPreChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

// limitedOne returns a Context whose pool is a Limited pool with one slot, as a caller could hand the executor.
func limitedOne(t *testing.T) context.Context {
	t.Helper()
	return context.SetPool(t.Context(), context.Pool(t.Context()).Limited(t.Context(), "limitedOne", 1))
}

// slotFree reports whether ctx's pool can still take a job, waiting at most two seconds for a slot.
func slotFree(t *testing.T, ctx context.Context) bool {
	t.Helper()
	submitCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	return context.Pool(ctx).Submit(submitCtx, func() {})
}

func TestPlanStartContChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		contChecks bool
		// canceled cancels the request Context before the state runs, so the pool refuses the ContChecks.
		canceled bool
		wantErr  bool
	}{
		{
			name: "Success: with no ContChecks the result channel is closed",
		},
		{
			// Regression: the lifelong ContChecks loop was submitted to the request's pool, so a caller's Limited
			// pool lost a slot for the life of the Plan.
			name:       "Success: ContChecks run outside the caller's Limited pool",
			contChecks: true,
		},
		{
			// Regression: a refused Submit was ignored, so nothing ever closed the result channel and the drain in
			// PlanPostChecks hung.
			name:       "Error: ContChecks the pool refuses report an error and close the result channel",
			contChecks: true,
			canceled:   true,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		states := &States{
			store:            &fakeUpdater{},
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error { return nil },
		}

		var contChecks *workflow.Checks
		if test.contChecks {
			contChecks = &workflow.Checks{Delay: time.Millisecond}
			contChecks.State.Set(workflow.State{})
		}

		ctx := limitedOne(t)
		reqCtx := ctx
		if test.canceled {
			var cancel context.CancelFunc
			reqCtx, cancel = context.WithCancel(ctx)
			cancel()
		}

		req := statemachine.Request[Data]{
			Ctx: reqCtx,
			Data: Data{
				Plan:            &workflow.Plan{ContChecks: contChecks},
				contCheckResult: make(chan error, 1),
			},
		}

		req = states.PlanStartContChecks(req)
		if methodName(req.Next) != methodName(states.ExecuteBlock) {
			t.Errorf("TestPlanStartContChecks(%s): got req.Next == %s, want req.Next == %s", test.name, methodName(req.Next), methodName(states.ExecuteBlock))
		}
		if test.contChecks && req.Data.contCancel == nil {
			t.Errorf("TestPlanStartContChecks(%s): got req.Data.contCancel == nil, want req.Data.contCancel != nil", test.name)
		}
		if test.contChecks && !test.canceled && !slotFree(t, ctx) {
			t.Errorf("TestPlanStartContChecks(%s): the ContChecks hold the caller's only Limited pool slot", test.name)
		}
		if req.Data.contCancel != nil {
			req.Data.contCancel()
		}

		waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		var err error
		for e := range chans.Iter(waitCtx, req.Data.contCheckResult) {
			if e != nil {
				err = e
			}
		}
		closed := waitCtx.Err() == nil
		cancel()
		if !closed {
			t.Errorf("TestPlanStartContChecks(%s): the result channel was not closed", test.name)
			continue
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestPlanStartContChecks(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestPlanStartContChecks(%s): got err == %s, want err == nil", test.name, err)
		}
	}
}

func TestExecuteBlocks(t *testing.T) {
	t.Parallel()

	states := &States{} // Used to get the method name of a state for wantNextState

	tests := []struct {
		name          string
		block         block
		recovered     bool
		wantErr       bool
		wantStatus    workflow.Status
		wantNextState statemachine.State[Data]
	}{
		{
			name:          "Success: with no more blocks the Plan moves to PlanPostChecks",
			wantNextState: states.PlanPostChecks,
		},
		{
			name: "Success: a new block is marked Running and moves to BlockBypassChecks",
			block: block{
				block: func() *workflow.Block {
					b := &workflow.Block{}
					b.State.Set(workflow.State{})
					return b
				}(),
			},
			wantStatus:    workflow.Running,
			wantNextState: states.BlockBypassChecks,
		},
		{
			// Regression: a recovered Running block ran its Running sequences before its checks restarted, so they
			// ran unguarded by its continuous checks. It must go to its checks first and leave the sequences for
			// ExecuteSequences (see TestExecuteBlockRecovered).
			name: "Success: a recovered Running block goes to BlockBypassChecks without running its sequences",
			block: block{
				block: func() *workflow.Block {
					bypass := &workflow.Checks{}
					bypass.State.Set(workflow.State{})
					seq := newSequenceWithStateAndActionsRecov(&workflow.State{Status: workflow.Running}, []*workflow.Action{{Name: "error"}})
					return newBlockWithStateSeqsChecks(&workflow.State{Status: workflow.Running}, []*workflow.Sequence{seq}, bypass, nil, nil, nil)
				}(),
			},
			recovered:     true,
			wantStatus:    workflow.Running,
			wantNextState: states.BlockBypassChecks,
		},
	}

	for _, test := range tests {
		states := &States{store: &fakeUpdater{}, testActionRunner: fakeActionRunner}
		var blocks []block
		if test.block.block != nil {
			blocks = append(blocks, test.block)
		}
		req := statemachine.Request[Data]{
			Ctx: t.Context(),
			Data: Data{
				blocks:    blocks,
				recovered: test.recovered,
			},
		}
		req = states.ExecuteBlock(req)
		if test.wantErr != (req.Data.err != nil) {
			t.Errorf("TestExecuteBlocks(%s): got err == %v, want err == %v", test.name, req.Data.err, test.wantErr)
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestExecuteBlocks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if len(req.Data.blocks) != 0 {
			if req.Data.blocks[0].block.State.Get().Status != test.wantStatus {
				t.Errorf("TestExecuteBlocks(%s): got block state = %v, want %v", test.name, req.Data.blocks[0].block.State.Get().Status, test.wantStatus)
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
			name: "Success: nil BypassChecks move to BlockPreChecks",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				return b
			}(),
			wantBlockStatus: workflow.Running,
			wantNextState:   states.BlockPreChecks,
		},
		{
			name: "Success: passing BypassChecks skip the block to BlockEnd, which completes it",
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
			wantBlockStatus: workflow.Running,
			wantNextState:   states.BlockEnd,
		},
		{
			name: "Success: failing BypassChecks move to BlockPreChecks",
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
			Ctx: t.Context(),
			Data: Data{
				blocks: []block{{block: test.block, contCheckResult: make(chan error, 1)}},
			},
		}
		// ExecuteBlock marks a block Running before BlockBypassChecks. BlockBypassChecks never changes it; BlockEnd
		// completes a bypassed block.
		test.block.State.Set(workflow.State{Status: workflow.Running})

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.BlockBypassChecks(req)
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockBypassChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
		if got := test.block.State.Get().Status; got != test.wantBlockStatus {
			t.Errorf("TestBlockBypassChecks(%s): got block status = %v, want %v", test.name, got, test.wantBlockStatus)
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
		wantErr         bool
	}{
		{
			name: "Success: nil PreChecks and ContChecks move to BlockStartContChecks",
			block: func() *workflow.Block {
				b := &workflow.Block{}
				b.State.Set(workflow.State{})
				return b
			}(),
			wantNextState: states.BlockStartContChecks,
		},
		{
			name: "Success: passing PreChecks and ContChecks move to BlockStartContChecks",
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
			name:            "Error: failing PreChecks with passing ContChecks fail the block and move to BlockDeferredChecks",
			block:           preChecksBlock("error", "pass"),
			checksRunner:    fakeRunChecksOnce,
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.BlockDeferredChecks,
			wantErr:         true,
		},
		{
			name:            "Error: failing ContChecks with passing PreChecks fail the block and move to BlockDeferredChecks",
			block:           preChecksBlock("pass", "error"),
			checksRunner:    fakeRunChecksOnce,
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.BlockDeferredChecks,
			wantErr:         true,
		},
	}

	for _, test := range tests {
		req := statemachine.Request[Data]{
			Ctx: t.Context(),
			Data: Data{
				blocks: []block{{block: test.block}},
			},
		}
		test.block.State.Set(workflow.State{})

		states := &States{store: &fakeUpdater{}, testChecksRunner: test.checksRunner}
		req = states.BlockPreChecks(req)
		switch {
		case req.Data.err == nil && test.wantErr:
			t.Errorf("TestBlockPreChecks(%s): got err == nil, want err != nil", test.name)
		case req.Data.err != nil && !test.wantErr:
			t.Errorf("TestBlockPreChecks(%s): got err == %s, want err == nil", test.name, req.Data.err)
		}
		if got := req.Data.blocks[0].block.State.Get().Status; got != test.wantBlockStatus {
			t.Errorf("TestBlockPreChecks(%s): got block status = %v, want %v", test.name, got, test.wantBlockStatus)
		}
		if methodName(req.Next) != methodName(test.wantNextState) {
			t.Errorf("TestBlockPreChecks(%s): got next state = %v, want %v", test.name, methodName(req.Next), methodName(test.wantNextState))
		}
	}
}

func TestBlockStartContChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		contChecks bool
	}{
		{
			name: "Success: with no ContChecks the result channel is closed",
		},
		{
			// Regression: the lifelong ContChecks loop was submitted to the request's pool, so a caller's Limited
			// pool lost a slot for the life of the block.
			name:       "Success: ContChecks run outside the caller's Limited pool",
			contChecks: true,
		},
	}

	for _, test := range tests {
		states := &States{
			store:            &fakeUpdater{},
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error { return nil },
		}

		var contChecks *workflow.Checks
		if test.contChecks {
			contChecks = &workflow.Checks{Delay: time.Millisecond}
			contChecks.State.Set(workflow.State{})
		}

		ctx := limitedOne(t)
		req := statemachine.Request[Data]{
			Ctx: ctx,
			Data: Data{
				blocks: []block{
					{
						block:           &workflow.Block{ContChecks: contChecks},
						contCheckResult: make(chan error, 1),
					},
				},
			},
		}

		req = states.BlockStartContChecks(req)
		if methodName(req.Next) != methodName(states.ExecuteSequences) {
			t.Errorf("TestBlockStartContChecks(%s): got req.Next == %s, want req.Next == %s", test.name, methodName(req.Next), methodName(states.ExecuteSequences))
		}
		if test.contChecks && !slotFree(t, ctx) {
			t.Errorf("TestBlockStartContChecks(%s): the ContChecks hold the caller's only Limited pool slot", test.name)
		}
		h := req.Data.blocks[0]
		if h.contCancel != nil {
			h.contCancel()
		}

		waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		for range chans.Iter(waitCtx, h.contCheckResult) {
		}
		closed := waitCtx.Err() == nil
		cancel()
		if !closed {
			t.Errorf("TestBlockStartContChecks(%s): the result channel was not closed", test.name)
		}
	}
}

// TestRunContChecks is a regression test: runContChecks sent every result with a blocking send on a one slot channel,
// while the only reader during a block's sequences polls it once per sequence launch. After one buffered pass the
// checker parked, so the checks stopped running for the rest of a long sequence and a later failure was never seen.
func TestRunContChecks(t *testing.T) {
	t.Parallel()

	const failOn = 5

	var calls atomic.Int64
	s := &States{
		testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
			if calls.Add(1) >= failOn {
				return fmt.Errorf("error")
			}
			return nil
		},
	}
	checks := &workflow.Checks{Delay: time.Millisecond}
	results := make(chan error, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	context.Pool(ctx).Submit(ctx, func() { s.runContChecks(ctx, checks, results) })

	// Nothing reads results while the checks pass, as during a long sequence.
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < failOn {
		if time.Now().After(deadline) {
			t.Fatalf("TestRunContChecks: checks ran %d times with no reader, want %d", calls.Load(), failOn)
		}
		time.Sleep(time.Millisecond)
	}

	waitCtx, waitCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer waitCancel()
	var got error
	for err := range chans.Iter(waitCtx, results) {
		if err != nil {
			got = err
		}
	}
	if waitCtx.Err() != nil {
		t.Fatalf("TestRunContChecks: result channel was not closed after the checks failed")
	}
	if got == nil {
		t.Errorf("TestRunContChecks: got no failure from the checks, want the failure on call %d", failOn)
	}
}

// TestExecuteSequences tests ExecuteSequences in a variety of scenarios with a concurrency of 1.
// We tests concurrency for this in TestExecuteConcurrentSequences.
func TestExecuteSequences(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	failedAction := &workflow.Action{Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Sleep: 10 * time.Millisecond, Arg: "error"}}
	sequenceWithFailure := &workflow.Sequence{Actions: []*workflow.Action{failedAction}}

	successAction := &workflow.Action{Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Sleep: 10 * time.Millisecond, Arg: "success"}}
	sequenceWithSuccess := &workflow.Sequence{Actions: []*workflow.Action{successAction}}

	slowAction := &workflow.Action{Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Sleep: 500 * time.Millisecond, Arg: "success"}}
	sequenceSlow := &workflow.Sequence{Actions: []*workflow.Action{slowAction}}

	tests := []struct {
		name            string
		block           *workflow.Block
		contCheckFail   bool
		wantPluginCalls int
		wantStatus      workflow.Status
		wantErr         bool
	}{
		{
			name: "Success: unlimited tolerated failures let every sequence fail",
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
			name: "Error: a failure after a success exceeds one tolerated failure",
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
			name: "Error: two failures exceed one tolerated failure before a success runs",
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
			// Regression: ExecuteSequences returned as soon as the tolerated failures were exceeded, leaving the slow
			// sequence it had already started running and writing while the block and Plan were finished and stored.
			name: "Error: exceeding the tolerated failures waits for a sequence already running",
			block: &workflow.Block{
				ToleratedFailures: 0,
				Concurrency:       2,
				Sequences: []*workflow.Sequence{
					clone.Sequence(ctx, sequenceSlow, cloneOpts...),
					clone.Sequence(ctx, sequenceWithFailure, cloneOpts...),
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...), // Gets a slot after the failure, never runs.
					clone.Sequence(ctx, sequenceWithSuccess, cloneOpts...), // Never launched.
				},
			},
			wantPluginCalls: 2,
			wantStatus:      workflow.Failed,
			wantErr:         true,
		},
		{
			name: "Error: failing ContChecks fail the block before any sequence runs",
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
			name: "Success: every sequence succeeds",
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
			Ctx: t.Context(),
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
		// Nothing ExecuteSequences started may still be running once it returns: the states after it write the block
		// and Plan as final.
		if n := plug.Running.Load(); n != 0 {
			t.Errorf("TestExecuteSequences(%s): got %d plugin calls still running after return, want 0", test.name, n)
		}
		for i, seq := range test.block.Sequences {
			if seq.State.Get().Status == workflow.Running {
				t.Errorf("TestExecuteSequences(%s): sequence %d is still Running after return", test.name, i)
			}
		}
	}
}

// TestExecuteSequencesConcurrency test the concurrency limits for blocks to make sure it works.
func TestExecuteSequencesConcurrency(t *testing.T) {
	t.Parallel()

	build, err := builder.New("test", "test")
	if err != nil {
		t.Fatalf("TestExecuteSequencesConcurrency: builder.New: %s", err)
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
		t.Fatalf("TestExecuteSequencesConcurrency: build.Plan: %s", err)
	}

	for _, seq := range p.Blocks[0].Sequences {
		seq.State.Set(workflow.State{})
		for _, action := range seq.Actions {
			action.State.Set(workflow.State{})
		}
	}

	req := statemachine.Request[Data]{
		Ctx: t.Context(),
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
		// The block is never stored, so a store that checks the rows it updates would reject every write.
		store := &noop.Vault{}
		states := &States{
			testChecksRunner: fakeRunChecksOnce,
			store:            store,
		}
		test.block.block.State.Set(workflow.State{Status: workflow.Running})

		req := statemachine.Request[Data]{
			Ctx: t.Context(),
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
			t.Errorf("TestBlockPostChecks(%s): got next == %v, want next == %v", test.name, methodName(req.Next), methodName(states.BlockDeferredChecks))
		}
	}
}

func TestBlockEnd(t *testing.T) {
	t.Parallel()

	// This is simply used to get the name next State we expect.
	// We create new ones in the tests to avoid having a shared one.
	states := &States{}

	tests := []struct {
		name string
		data Data
		// blockStatus is the status of every block when BlockEnd is called. It defaults to Running.
		blockStatus workflow.Status
		// contUnstarted leaves the ContChecks result channel open and contCancel nil, as when BlockStartContChecks
		// never ran.
		contUnstarted   bool
		contCheckResult error
		wantErr         bool
		wantBlockStatus workflow.Status
		wantNextState   statemachine.State[Data]
		wantBlocksLen   int
	}{
		{
			name: "Error: a ContChecks failure fails the block and goes to PlanDeferredActions",
			data: Data{
				blocks: []block{{block: &workflow.Block{ContChecks: &workflow.Checks{}}}},
			},
			contCheckResult: fmt.Errorf("error"),
			wantErr:         true,
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.PlanDeferredActions,
			wantBlocksLen:   1,
		},
		{
			// Regression: BlockEnd ranged over the ContChecks result channel even when the PreChecks failed before
			// BlockStartContChecks ran, so nothing ever sent on or closed it and BlockEnd hung. The error, the Failed
			// block and the unstarted ContChecks are one input, not three: they are the state failed PreChecks leave.
			name: "Error: a block whose PreChecks failed before its ContChecks started ends without waiting on them",
			data: Data{
				blocks: []block{{block: &workflow.Block{ContChecks: &workflow.Checks{}}}},
				err:    fmt.Errorf("prechecks failed"),
			},
			blockStatus:     workflow.Failed,
			contUnstarted:   true,
			wantErr:         true,
			wantBlockStatus: workflow.Failed,
			wantNextState:   states.PlanDeferredActions,
			wantBlocksLen:   1,
		},
		{
			name: "Success: completed BypassChecks complete the block and go to ExecuteBlock",
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
			name: "Success: the last block completes and leaves no blocks",
			data: Data{
				blocks: []block{{}},
			},
			wantBlockStatus: workflow.Completed,
			wantNextState:   states.ExecuteBlock,
			wantBlocksLen:   0,
		},
		{
			name: "Success: a block completes and leaves the remaining blocks",
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
		status := test.blockStatus
		if status == workflow.NotStarted {
			status = workflow.Running
		}
		for i, block := range test.data.blocks {
			if block.block == nil {
				block.block = &workflow.Block{}
			}
			block.block.State.Set(workflow.State{Status: status})
			test.data.blocks[i] = block
		}

		ctx := t.Context()
		req := statemachine.Request[Data]{
			Ctx:  t.Context(),
			Data: test.data,
		}

		req.Data.blocks[0].contCheckResult = make(chan error, 1)
		if !test.contUnstarted {
			ctx, req.Data.blocks[0].contCancel = context.WithCancel(t.Context())
			if test.contCheckResult != nil {
				req.Data.blocks[0].contCheckResult <- test.contCheckResult
			}
			close(req.Data.blocks[0].contCheckResult)
		}

		// We store this here because blocks is shrunk after the call.
		block := req.Data.blocks[0].block

		// BlockEnd runs on the pool so a hang fails the test instead of stalling it.
		done := make(chan statemachine.Request[Data], 1)
		context.Pool(t.Context()).Submit(t.Context(), func() { done <- states.BlockEnd(req) })
		waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		got, r := chans.Get(waitCtx, done)
		cancel()
		if !r.OK() {
			t.Errorf("TestBlockEnd(%s): BlockEnd did not return", test.name)
			continue
		}
		req = got

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
		bypassed := block.BypassChecks != nil && block.BypassChecks.GetState().Status == workflow.Completed
		if !test.contUnstarted && !bypassed && ctx.Err() == nil {
			t.Errorf("TestBlockEnd(%s): context for continuous checks should have been cancelled", test.name)
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
		store := &noop.Vault{}

		states := &States{
			testChecksRunner: fakeRunChecksOnce,
			store:            store,
		}
		// We cancel a context for continuous checks that are running. This
		// is used to simulate that we signal the continuous checks to stop.
		ctx, cancel := context.WithCancel(t.Context())

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
			Ctx: t.Context(),
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

// TestRuntimeUpdate tests the runtimeUpdate function to ensure it correctly updates the plan when more than 5 minutes
// have passed since the last update. This tests the fix for a bug where the time comparison was backwards
// (LastUpdate.Sub(now) instead of now.Sub(LastUpdate)). It is also a regression test: the 5 minutes was fixed, so with
// a maxLastUpdate under about 5 minutes a Plan alive at a crash could be aged out as abandoned on restart. A shorter
// maxLastUpdate must write within a third of it.
func TestRuntimeUpdate(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		maxLastUpdate time.Duration
		lastUpdate    time.Time
		now           time.Time
		wantUpdate    bool
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
		{
			name:          "Success: Update triggered when 6 minutes have passed with the default maxLastUpdate of 30 minutes",
			maxLastUpdate: 30 * time.Minute,
			lastUpdate:    baseTime,
			now:           baseTime.Add(6 * time.Minute),
			wantUpdate:    true,
		},
		{
			name:          "Success: Update triggered when 2 minutes have passed with a maxLastUpdate of 3 minutes",
			maxLastUpdate: 3 * time.Minute,
			lastUpdate:    baseTime,
			now:           baseTime.Add(2 * time.Minute),
			wantUpdate:    true,
		},
		{
			name:          "Success: No update when 30 seconds have passed with a maxLastUpdate of 3 minutes",
			maxLastUpdate: 3 * time.Minute,
			lastUpdate:    baseTime,
			now:           baseTime.Add(30 * time.Second),
			wantUpdate:    false,
		},
	}

	for _, test := range tests {
		updater := &fakeUpdater{}
		states := &States{
			store:         updater,
			nower:         func() time.Time { return test.now },
			maxLastUpdate: test.maxLastUpdate,
		}

		plan := &workflow.Plan{}
		plan.State.Set(workflow.State{Status: workflow.Running, Start: test.lastUpdate})

		states.runtimeUpdate(t.Context(), plan)

		gotUpdate := updater.calls.Load() > 0
		if gotUpdate != test.wantUpdate {
			t.Errorf("TestRuntimeUpdate(%s): got update = %v, want %v", test.name, gotUpdate, test.wantUpdate)
		}

		if test.wantUpdate && plan.RuntimeUpdate.Get().IsZero() {
			t.Errorf("TestRuntimeUpdate(%s): expected RuntimeUpdate to be set, but it was zero", test.name)
		}
	}
}

func TestHeartbeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		maxLastUpdate time.Duration
		wantWrite     time.Duration
		wantTick      time.Duration
	}{
		{
			name:      "Success: no maxLastUpdate uses the defaults",
			wantWrite: 5 * time.Minute,
			wantTick:  10 * time.Second,
		},
		{
			name:          "Success: the default maxLastUpdate of 30 minutes uses the defaults",
			maxLastUpdate: 30 * time.Minute,
			wantWrite:     5 * time.Minute,
			wantTick:      10 * time.Second,
		},
		{
			name:          "Success: a maxLastUpdate of 3 minutes writes after a minute",
			maxLastUpdate: 3 * time.Minute,
			wantWrite:     time.Minute,
			wantTick:      10 * time.Second,
		},
		{
			name:          "Success: a maxLastUpdate of 15 seconds writes and checks every 5 seconds",
			maxLastUpdate: 15 * time.Second,
			wantWrite:     5 * time.Second,
			wantTick:      5 * time.Second,
		},
		{
			name:          "Success: a maxLastUpdate too short to divide still checks with a positive period",
			maxLastUpdate: time.Nanosecond,
			wantWrite:     0,
			wantTick:      time.Millisecond,
		},
	}

	for _, test := range tests {
		write, tick := heartbeat(test.maxLastUpdate)
		if write != test.wantWrite {
			t.Errorf("TestHeartbeat(%s): got write %v, want %v", test.name, write, test.wantWrite)
		}
		if tick != test.wantTick {
			t.Errorf("TestHeartbeat(%s): got tick %v, want %v", test.name, tick, test.wantTick)
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

// TestContChecksPassing includes a regression test: contChecksPassing used a select over the Plan and block result
// channels, and a closed channel (no ContChecks) or a waiting pass is always ready, so Go's random choice missed a
// waiting failure on the other channel about half the time. Each case is polled many times to catch that.
func TestContChecksPassing(t *testing.T) {
	t.Parallel()

	const polls = 200

	closed := func() chan error {
		ch := make(chan error, 1)
		close(ch)
		return ch
	}
	holding := func(err error) func() chan error {
		return func() chan error {
			ch := make(chan error, 1)
			ch <- err
			return ch
		}
	}
	empty := func() chan error { return make(chan error, 1) }
	none := func() chan error { return nil }
	fail := fmt.Errorf("error")

	tests := []struct {
		name     string
		plan     func() chan error
		block    func() chan error
		noBlocks bool
		wantType workflow.ObjectType
		wantErr  bool
	}{
		{
			name:  "Success: empty Plan and block channels report no failure",
			plan:  empty,
			block: empty,
		},
		{
			name:  "Success: a closed Plan channel and a waiting block pass report no failure",
			plan:  closed,
			block: holding(nil),
		},
		{
			name:  "Success: a missing Plan channel and an empty block channel report no failure",
			plan:  none,
			block: empty,
		},
		{
			name:     "Success: a missing Plan channel with no blocks reports no failure",
			plan:     none,
			noBlocks: true,
		},
		{
			name:     "Error: a closed Plan channel does not hide a block failure",
			plan:     closed,
			block:    holding(fail),
			wantType: workflow.OTBlock,
			wantErr:  true,
		},
		{
			name:     "Error: a waiting Plan pass does not hide a block failure",
			plan:     holding(nil),
			block:    holding(fail),
			wantType: workflow.OTBlock,
			wantErr:  true,
		},
		{
			name:     "Error: a closed block channel does not hide a Plan failure",
			plan:     holding(fail),
			block:    closed,
			wantType: workflow.OTPlan,
			wantErr:  true,
		},
		{
			name:     "Error: a missing block channel does not hide a Plan failure",
			plan:     holding(fail),
			block:    none,
			wantType: workflow.OTPlan,
			wantErr:  true,
		},
		{
			name:     "Error: a Plan failure with no blocks is reported",
			plan:     holding(fail),
			noBlocks: true,
			wantType: workflow.OTPlan,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		for i := 0; i < polls; i++ {
			d := Data{contCheckResult: test.plan()}
			if !test.noBlocks {
				d.blocks = []block{{block: &workflow.Block{}, contCheckResult: test.block()}}
			}
			ot, err := d.contChecksPassing()
			if (err != nil) != test.wantErr || ot != test.wantType {
				// One report per case is enough; the rest of the polls would repeat it.
				t.Errorf("TestContChecksPassing(%s): poll %d: got (%v, %v), want object type %v and err != nil == %v", test.name, i, ot, err, test.wantType, test.wantErr)
				break
			}
		}
	}
}

// TestEndContChecks is a regression test for two bugs. On the failure paths into End (a failed block skips
// PlanPostChecks) End cancelled the Plan's ContChecks but never drained their result channel. A pass already in flight
// kept running while the final state was worked out and written, and the failure it produced was lost. And End stopped
// the heartbeat before it cancelled and drained that pass. A pass runs detached from cancellation and can run as long
// as its actions' timeouts and retries allow, so the Plan sat Running in storage with no RuntimeUpdate refresh while
// work was still running, and a restart in that window could age a live Plan out. The heartbeat must stop only once no
// pass is in flight, and still before the final state is worked out.
func TestEndContChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// inFlight starts a ContChecks pass that End must cancel and drain.
		inFlight bool
		// fail makes the in-flight pass fail.
		fail bool
		// noContChecks gives the Plan no ContChecks, and hands End a contCancel it must still call.
		noContChecks bool
		// handedErr is an error from an earlier state that End is handed.
		handedErr  error
		wantStatus workflow.Status
		wantReason workflow.FailureReason
		// wantWrites is how many storage writes End makes.
		wantWrites int32
		wantErr    bool
	}{
		{
			name:       "Success: with no ContChecks pass in flight, End stops the heartbeat while the Plan is still Running",
			wantStatus: workflow.Completed,
			wantWrites: 2,
		},
		{
			// Regression: the heartbeat must be stopped before the final state is worked out or written, or a heartbeat
			// write could store the finished Plan before its objects.
			name:         "Error: End handed an error from an earlier state cancels the ContChecks, writes the Plan once and returns the error",
			noContChecks: true,
			handedErr:    fmt.Errorf("error"),
			wantStatus:   workflow.Completed,
			wantWrites:   1,
			wantErr:      true,
		},
		{
			name:       "Success: End waits for a passing ContChecks pass in flight and stops the heartbeat after it",
			inFlight:   true,
			wantStatus: workflow.Completed,
			wantWrites: 2,
		},
		{
			name:       "Error: End waits for a failing ContChecks pass in flight, stops the heartbeat after it and fails the Plan with FRContCheck",
			inFlight:   true,
			fail:       true,
			wantStatus: workflow.Failed,
			wantReason: workflow.FRContCheck,
			wantWrites: 2,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		var running atomic.Int64
		states := &States{
			store: &fakeUpdater{},
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				running.Add(1)
				defer running.Add(-1)
				time.Sleep(300 * time.Millisecond)
				if test.fail {
					checks.State.Set(workflow.State{Status: workflow.Failed})
					return fmt.Errorf("error")
				}
				checks.State.Set(workflow.State{Status: workflow.Completed})
				return nil
			},
		}

		plan := &workflow.Plan{}
		plan.State.Set(workflow.State{Status: workflow.Running})
		req := statemachine.Request[Data]{Ctx: t.Context(), Data: Data{Plan: plan, err: test.handedErr}}
		contCtx, contCancel := context.WithCancel(t.Context())
		if test.noContChecks {
			req.Data.contCancel = contCancel
		} else {
			cont := &workflow.Checks{Delay: time.Hour}
			cont.State.Set(workflow.State{Status: workflow.Completed})
			plan.ContChecks = cont
			req.Data.contCheckResult = make(chan error, 1)
		}
		if test.inFlight {
			req = states.PlanStartContChecks(req)
			deadline := time.Now().Add(5 * time.Second)
			for running.Load() == 0 {
				if time.Now().After(deadline) {
					t.Fatalf("TestEndContChecks(%s): the ContChecks never started", test.name)
				}
				time.Sleep(time.Millisecond)
			}
		}

		stopped := false
		var inFlightAtStop int64
		var statusAtStop workflow.Status
		var writesAtStop int32
		req.Data.stopHeartbeat = func() {
			stopped = true
			inFlightAtStop = running.Load()
			statusAtStop = plan.State.Get().Status
			writesAtStop = states.store.(*fakeUpdater).calls.Load()
		}

		req = states.End(req)
		contCancel()

		switch {
		case req.Err == nil && test.wantErr:
			t.Errorf("TestEndContChecks(%s): got err == nil, want err != nil", test.name)
		case req.Err != nil && !test.wantErr:
			t.Errorf("TestEndContChecks(%s): got err == %s, want err == nil", test.name, req.Err)
		}
		if n := running.Load(); n != 0 {
			t.Errorf("TestEndContChecks(%s): got %d ContChecks runs still in flight after End, want 0", test.name, n)
		}
		if got := plan.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestEndContChecks(%s): got Plan status %v, want %v", test.name, got, test.wantStatus)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestEndContChecks(%s): got Plan reason %v, want %v", test.name, plan.Reason, test.wantReason)
		}
		if plan.State.Get().End.IsZero() {
			t.Errorf("TestEndContChecks(%s): Plan end time was not set", test.name)
		}
		if req.Next != nil {
			t.Errorf("TestEndContChecks(%s): got a next state, want nil", test.name)
		}
		if got := states.store.(*fakeUpdater).calls.Load(); got != test.wantWrites {
			t.Errorf("TestEndContChecks(%s): got %d storage writes, want %d", test.name, got, test.wantWrites)
		}
		if test.noContChecks && contCtx.Err() == nil {
			t.Errorf("TestEndContChecks(%s): End did not cancel the ContChecks", test.name)
		}
		if !stopped {
			t.Errorf("TestEndContChecks(%s): heartbeat was not stopped", test.name)
			continue
		}
		if inFlightAtStop != 0 {
			t.Errorf("TestEndContChecks(%s): heartbeat stopped with %d ContChecks passes still in flight, want 0", test.name, inFlightAtStop)
		}
		if statusAtStop != workflow.Running || writesAtStop != 0 {
			t.Errorf("TestEndContChecks(%s): heartbeat stopped with Plan status %v after %d writes, want Running and 0 writes", test.name, statusAtStop, writesAtStop)
		}
	}
}

// TestRunChecksOnceRerun is a regression test: ContChecks run the same Checks on every pass, so the test plugin
// executes the same Req again. It closed Req.Started on every Execute, so the second pass panicked on a closed channel.
func TestRunChecksOnceRerun(t *testing.T) {
	t.Parallel()

	plug := &plugins.Plugin{AlwaysRespond: true, IsCheckPlugin: true}
	reg := registry.New()
	reg.Register(plug)

	started := make(chan struct{})
	checks := &workflow.Checks{
		Actions: []*workflow.Action{
			{Name: "check", Plugin: plugins.Name, Timeout: 10 * time.Second, Req: plugins.Req{Started: started}},
		},
	}
	checks.State.Set(workflow.State{})
	for _, a := range checks.Actions {
		a.State.Set(workflow.State{})
	}

	// fakeUpdater clones what it stores and cannot clone a channel in a Req.
	states := &States{registry: reg, store: &noop.Vault{}}
	for i := 0; i < 2; i++ {
		if err := states.runChecksOnce(t.Context(), checks); err != nil {
			t.Fatalf("TestRunChecksOnceRerun: pass %d: got err == %s, want err == nil", i, err)
		}
	}
	if _, ok, closed := chans.TryGet(started); ok || !closed {
		t.Errorf("TestRunChecksOnceRerun: Req.Started is not closed after the checks ran")
	}
	if got := plug.Calls.Load(); got != 2 {
		t.Errorf("TestRunChecksOnceRerun: got %d plugin calls, want 2", got)
	}
}

// TestExecuteSequencesContWait is a regression test. ExecuteSequences polled the continuous checks before it waited for
// a free slot in the block's Limited pool, and the launched sequence re-checked only the tolerated failures. With a
// Concurrency of one, ContChecks that failed while the next sequence waited for the slot did not stop it from starting.
func TestExecuteSequencesContWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// contFail fails the ContChecks once the next sequence's launch has polled them and waits for the slot.
		contFail   bool
		wantRan    map[string]int
		wantStatus workflow.Status
		wantErr    bool
	}{
		{
			name:       "Success: ContChecks that pass while the next sequence waits for a slot let it run",
			wantRan:    map[string]int{"first": 1, "second": 1},
			wantStatus: workflow.Running,
		},
		{
			name:       "Error: ContChecks that fail while the next sequence waits for a slot keep it from starting",
			contFail:   true,
			wantRan:    map[string]int{"first": 1},
			wantStatus: workflow.Failed,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		// Two waiting passes: launching the first sequence polls one, launching the second polls the other. The first
		// sequence waits until both are gone, so the second's launch has polled the checks before they fail.
		results := make(chan error, 2)
		results <- nil
		results <- nil

		var mu sync.Mutex
		ran := map[string]int{}
		s := &States{
			store: &fakeUpdater{},
			testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
				mu.Lock()
				ran[a.Name]++
				mu.Unlock()
				if a.Name != "first" {
					return nil
				}
				deadline := time.Now().Add(5 * time.Second)
				for len(results) != 0 {
					if time.Now().After(deadline) {
						t.Errorf("TestExecuteSequencesContWait(%s): the second sequence's launch never polled the ContChecks", test.name)
						return nil
					}
					time.Sleep(time.Millisecond)
				}
				if test.contFail {
					results <- fmt.Errorf("ContChecks failed")
				}
				return nil
			},
		}

		var seqs []*workflow.Sequence
		for _, name := range []string{"first", "second"} {
			seq := &workflow.Sequence{ID: workflow.NewV7(), Name: name, Actions: []*workflow.Action{{ID: workflow.NewV7(), Name: name}}}
			seq.State.Set(workflow.State{})
			seq.Actions[0].State.Set(workflow.State{})
			seqs = append(seqs, seq)
		}
		b := &workflow.Block{ID: workflow.NewV7(), Concurrency: 1, Sequences: seqs}
		b.State.Set(workflow.State{Status: workflow.Running})

		req := statemachine.Request[Data]{
			Ctx:  t.Context(),
			Data: Data{blocks: []block{{block: b, contCheckResult: results}}},
		}
		req = s.ExecuteSequences(req)
		switch {
		case req.Data.err == nil && test.wantErr:
			t.Errorf("TestExecuteSequencesContWait(%s): got err == nil, want err != nil", test.name)
		case req.Data.err != nil && !test.wantErr:
			t.Errorf("TestExecuteSequencesContWait(%s): got err == %s, want err == nil", test.name, req.Data.err)
		}

		mu.Lock()
		if diff := pretty.Compare(test.wantRan, ran); diff != "" {
			t.Errorf("TestExecuteSequencesContWait(%s): action runs -want +got:\n%s", test.name, diff)
		}
		mu.Unlock()
		if got := b.State.Get().Status; got != test.wantStatus {
			t.Errorf("TestExecuteSequencesContWait(%s): got block status %v, want %v", test.name, got, test.wantStatus)
		}
	}
}

// TestExecuteSequencesLateContFailure is a regression test. ExecuteSequences polled the Plan's ContChecks only when
// launching a sequence, so a failure that arrived while a block's last sequence ran, after every launch had polled, was
// missed: the block ended Completed and the next block ran its BypassChecks and PreChecks before a launch noticed the
// failure. The failure must fail the block that was running when it arrived, and no later block may start. The same
// held for a failure that arrived after the block's sequences, during its PostChecks: it must fail the Plan before the
// next block starts.
func TestExecuteSequencesLateContFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// contFailIn names the action during which the Plan's ContChecks fail: "first", the first block's only
		// sequence, after its launch polled, or "first post", the first block's PostChecks. Empty, they keep passing.
		contFailIn     string
		wantRan        map[string]int
		wantStatuses   []workflow.Status
		wantPlanStatus workflow.Status
		wantReason     workflow.FailureReason
		wantErr        bool
	}{
		{
			name:           "Success: ContChecks that keep passing let both blocks run to completion",
			wantRan:        map[string]int{"first": 1, "first post": 1, "bypass": 1, "pre": 1, "second": 1},
			wantStatuses:   []workflow.Status{workflow.Completed, workflow.Completed},
			wantPlanStatus: workflow.Completed,
		},
		{
			name:           "Error: ContChecks that fail during the first block's last sequence fail it and the second block never starts",
			contFailIn:     "first",
			wantRan:        map[string]int{"first": 1},
			wantStatuses:   []workflow.Status{workflow.Failed, workflow.NotStarted},
			wantPlanStatus: workflow.Failed,
			wantReason:     workflow.FRContCheck,
			wantErr:        true,
		},
		{
			name:           "Error: ContChecks that fail during the first block's PostChecks fail the Plan and the second block never starts",
			contFailIn:     "first post",
			wantRan:        map[string]int{"first": 1, "first post": 1},
			wantStatuses:   []workflow.Status{workflow.Completed, workflow.NotStarted},
			wantPlanStatus: workflow.Failed,
			wantReason:     workflow.FRContCheck,
			wantErr:        true,
		},
	}

	for _, test := range tests {
		// The Plan's ContChecks loop is stood in for by this channel, so the test decides exactly when a result
		// arrives. Stopping the "loop" closes it, as runContChecks does on return.
		results := make(chan error, 1)
		var stop sync.Once
		contCancel := func() { stop.Do(func() { close(results) }) }

		contChecks := &workflow.Checks{ID: workflow.NewV7()}
		contChecks.State.Set(workflow.State{Status: workflow.Completed})

		var mu sync.Mutex
		ran := map[string]int{}
		count := func(name string) {
			mu.Lock()
			ran[name]++
			mu.Unlock()
		}

		newSeq := func(name string) *workflow.Sequence {
			seq := &workflow.Sequence{ID: workflow.NewV7(), Name: name, Actions: []*workflow.Action{{ID: workflow.NewV7(), Name: name}}}
			seq.State.Set(workflow.State{})
			seq.Actions[0].State.Set(workflow.State{})
			return seq
		}
		newChecks := func(name string) *workflow.Checks {
			c := &workflow.Checks{ID: workflow.NewV7(), Actions: []*workflow.Action{{ID: workflow.NewV7(), Name: name}}}
			c.State.Set(workflow.State{})
			return c
		}

		first := &workflow.Block{ID: workflow.NewV7(), Name: "first", Concurrency: 1, PostChecks: newChecks("first post"), Sequences: []*workflow.Sequence{newSeq("first")}}
		first.State.Set(workflow.State{})
		second := &workflow.Block{
			ID:           workflow.NewV7(),
			Name:         "second",
			Concurrency:  1,
			BypassChecks: newChecks("bypass"),
			PreChecks:    newChecks("pre"),
			Sequences:    []*workflow.Sequence{newSeq("second")},
		}
		second.State.Set(workflow.State{})

		plan := &workflow.Plan{ID: workflow.NewV7(), ContChecks: contChecks, Blocks: []*workflow.Block{first, second}}
		plan.State.Set(workflow.State{Status: workflow.Running})

		// contFail has the Plan's ContChecks fail. The send does not block: nothing else sends, and the polls before it
		// found the channel empty.
		contFail := func(name string) {
			if name == test.contFailIn {
				contChecks.State.Set(workflow.State{Status: workflow.Failed})
				results <- fmt.Errorf("ContChecks failed")
			}
		}

		s := &States{
			store: &fakeUpdater{},
			testActionRunner: func(ctx context.Context, a *workflow.Action, updater storage.ActionUpdater) error {
				count(a.Name)
				// This runs inside the sequence, after ExecuteSequences polled the ContChecks to launch it.
				contFail(a.Name)
				return nil
			},
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				name := checks.Actions[0].Name
				count(name)
				contFail(name)
				// The bypass fails so the second block runs; its PreChecks pass.
				if name == "bypass" {
					checks.State.Set(workflow.State{Status: workflow.Failed})
					return fmt.Errorf("bypass not met")
				}
				checks.State.Set(workflow.State{Status: workflow.Completed})
				return nil
			},
		}

		data := Data{Plan: plan, contCheckResult: results, contCancel: contCancel}
		for _, b := range plan.Blocks {
			data.blocks = append(data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
		}

		_, err := statemachine.Run("TestExecuteSequencesLateContFailure", statemachine.Request[Data]{Ctx: t.Context(), Data: data, Next: s.ExecuteBlock})
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestExecuteSequencesLateContFailure(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestExecuteSequencesLateContFailure(%s): got err == %s, want err == nil", test.name, err)
		}

		mu.Lock()
		if diff := pretty.Compare(test.wantRan, ran); diff != "" {
			t.Errorf("TestExecuteSequencesLateContFailure(%s): runs -want +got:\n%s", test.name, diff)
		}
		mu.Unlock()
		gotStatuses := []workflow.Status{first.State.Get().Status, second.State.Get().Status}
		if diff := pretty.Compare(test.wantStatuses, gotStatuses); diff != "" {
			t.Errorf("TestExecuteSequencesLateContFailure(%s): block statuses -want +got:\n%s", test.name, diff)
		}
		if got := plan.State.Get().Status; got != test.wantPlanStatus {
			t.Errorf("TestExecuteSequencesLateContFailure(%s): got Plan status %v, want %v", test.name, got, test.wantPlanStatus)
		}
		if plan.Reason != test.wantReason {
			t.Errorf("TestExecuteSequencesLateContFailure(%s): got Plan reason %v, want %v", test.name, plan.Reason, test.wantReason)
		}
	}
}

// TestRunContChecksStopped is a regression test. Once stopped, runContChecks chose at random between its stopped
// Context and a due ticker, so about half the time it started a new pass after PlanPostChecks or BlockEnd had stopped
// it: after all the blocks were done. A pass in flight when they are stopped may finish, but no new one may start.
func TestRunContChecksStopped(t *testing.T) {
	t.Parallel()

	// The bug picked the new pass about half the time, so this many runs all but guarantee catching it.
	const runs = 200

	for i := 0; i < runs; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int64
		s := &States{
			testChecksRunner: func(ctx context.Context, checks *workflow.Checks) error {
				calls.Add(1)
				// Stop the checks while this pass is in flight, as PlanPostChecks does after the last block.
				cancel()
				return nil
			},
		}
		s.runContChecks(ctx, &workflow.Checks{Delay: time.Nanosecond}, make(chan error, 1))
		cancel()
		if got := calls.Load(); got != 1 {
			t.Errorf("TestRunContChecksStopped: run %d: got %d passes, want 1 (no pass may start once the checks are stopped)", i, got)
			return
		}
	}
}

// TestAfter is also a regression test: after waited with !r.OK(), so a timer that had fired by the time the Context
// was done counted as the delay passing, and the block went on to run after its Context ended.
func TestAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		// cancelled runs after with a Context that is already done. The run is repeated, since the bug only showed when
		// the timer had fired before after looked at it.
		cancelled bool
		wantErr   bool
	}{
		{
			name: "Success: no delay returns at once",
		},
		{
			name: "Success: the delay passes",
			d:    time.Millisecond,
		},
		{
			name:      "Success: no delay returns at once even with a done Context",
			cancelled: true,
		},
		{
			name:      "Error: a done Context ends the delay even if the timer fired",
			d:         time.Nanosecond,
			cancelled: true,
			wantErr:   true,
		},
		{
			name:      "Error: a done Context ends a long delay",
			d:         time.Hour,
			cancelled: true,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		runs := 1
		if test.cancelled {
			runs = 200
		}
		for i := 0; i < runs; i++ {
			ctx, cancel := context.WithCancel(t.Context())
			if test.cancelled {
				cancel()
			}
			err := after(ctx, test.d)
			cancel()

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestAfter(%s): run %d: got err == nil, want err != nil", test.name, i)
			case err != nil && !test.wantErr:
				t.Errorf("TestAfter(%s): run %d: got err == %s, want err == nil", test.name, i, err)
			}
			if t.Failed() {
				break
			}
		}
	}
}
