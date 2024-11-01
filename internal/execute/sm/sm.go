// Package sm holds the states of our executor statemachine.
package sm

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm/actions"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/gostdlib/concurrency/goroutines/pooled"
	"github.com/gostdlib/concurrency/prim/wait"
	"github.com/gostdlib/ops/statemachine"
)

var ErrInternalFailure = errors.New("internal failure")

// block is a wrapper around a workflow.Block that contains additional information for the statemachine.
type block struct {
	block *workflow.Block

	contCancel      context.CancelFunc
	contCheckResult chan error
}

// Data represents the data that is passed between states.
type Data struct {
	// Plan is the workflow.Plan that is being executed.
	Plan *workflow.Plan

	// blocks is a list of blocks that are being executed. These are removed as each block is completed.
	blocks []block
	// contCancel is the context.CancelFunc that will cancel the continuous check for the Plan.
	contCancel context.CancelFunc
	// contCheckResult is the channel that will receive the result of the continuous check for the Plan.
	contCheckResult chan error

	err error
}

// contChecks will check if any of the continuous checkes on the Plan or the current block have failed.
// If a check has failed, the type of the object that failed is returned (OTPlan or OTBlock).
func (d Data) contChecksPassing() (workflow.ObjectType, error) {
	if len(d.blocks) == 0 {
		select {
		case err := <-d.contCheckResult:
			return workflow.OTPlan, err
		default:
			return workflow.OTUnknown, nil
		}
	}
	select {
	case err := <-d.contCheckResult:
		return workflow.OTPlan, err
	case err := <-d.blocks[0].contCheckResult:
		return workflow.OTBlock, err
	default:
	}
	return workflow.OTUnknown, nil
}

type nower func() time.Time

// actionRunner is a function that runs an action. We use this to fake out the action runner in tests.
type actionRunner func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error

// checksRunner is a function that runs checks. We use this to fake out the check runner in tests.
type checksRunner func(ctx context.Context, checks *workflow.Checks) error

// actionsParallelRunner is a function that runs a list of actions in parallel. We use this to fake out the action runner in tests.
type actionsParallelRunner func(ctx context.Context, actions []*workflow.Action) error

// States is the statemachine that handles the execution of a Plan.
type States struct {
	store    storage.Vault
	registry *registry.Register

	actionsSM actions.Runner

	// nower is the function that returns the current time. This is set to time.Now by default.
	nower nower

	// checksRunner is the function that runs checks. If set, runChecksOnce calls this and returns.
	// We use this to fake out the check runner in tests.
	checksRunner checksRunner
	// actionsParallelRunner is the function that runs a list of actions in parallel. If set, runParrallelActions calls this and returns.
	actionsParallelRunner actionsParallelRunner
	// actionRunner is the function that runs an action. If set, runAction calls this and returns.
	// We use this to fake out the action runner in tests.
	actionRunner actionRunner
}

// New creates a new States statemachine.
func New(store storage.Vault, registry *registry.Register) (*States, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	s := &States{
		store:    store,
		registry: registry,
	}
	return s, nil
}

// Start starts execution of the Plan. This is the first state of the statemachine.
func (s *States) Start(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)

	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	plan.State.Status = workflow.Running
	plan.State.Start = s.now()

	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}

	req.Next = s.PlanBypassChecks
	return req
}

// PlanBypassChecks runs all the gates on the Plan. If any of the gates fail,
// or no gates are present, the Plan is executed.
func (s *States) PlanBypassChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	skip := s.runBypasses(req.Ctx, req.Data.Plan.BypassChecks)
	if skip {
		if req.Data.contCheckResult != nil {
			close(req.Data.contCheckResult)
		}
		req.Next = s.End
		return req
	}
	req.Next = s.PlanPreChecks
	return req
}

// PlanPreChecks runs all PreChecks and ContChecks on the Plan before proceeding.
func (s *States) PlanPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	err := s.runPreChecks(req.Ctx, req.Data.Plan.PreChecks, req.Data.Plan.ContChecks)
	if err != nil {
		req.Data.err = err
		req.Next = s.PlanDeferredChecks
		return req
	}

	req.Next = s.PlanStartContChecks

	return req
}

// PlanStartContChecks starts the ContChecks of the Plan.
func (s *States) PlanStartContChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	if req.Data.Plan.ContChecks != nil {
		var ctx context.Context
		ctx, req.Data.contCancel = context.WithCancel(req.Ctx)

		go func() {
			s.runContChecks(ctx, req.Data.Plan.ContChecks, req.Data.contCheckResult)
		}()
	} else {
		close(req.Data.contCheckResult)
	}

	req.Next = s.ExecuteBlock
	return req
}

// ExecuteBlock executes the current block.
func (s *States) ExecuteBlock(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No more blocks, the Plan is done.
	if len(req.Data.blocks) == 0 {
		req.Next = s.PlanPostChecks
		return req
	}

	h := req.Data.blocks[0]

	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if err := after(req.Ctx, h.block.EntranceDelay); err != nil {
		h.block.State.Status = workflow.Stopped
		req.Data.err = err
		req.Next = s.PlanDeferredChecks
		return req
	}

	h.block.State.Status = workflow.Running
	h.block.State.Start = s.now()
	req.Next = s.BlockBypassChecks
	return req
}

// BlockBypassChecks runs all the gates on the Block. If any of the gates fail,
// or no gates are present, the Block is executed.
func (s *States) BlockBypassChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.BypassChecks == nil {
		req.Next = s.BlockPreChecks
		return req
	}
	skip := s.runBypasses(req.Ctx, h.block.BypassChecks)
	if skip {
		if h.contCheckResult != nil {
			close(h.contCheckResult)
		}
		req.Next = s.BlockEnd
		return req
	}
	req.Next = s.BlockPreChecks
	return req
}

// BlockPreChecks runs all PreChecks and ContChecks on the current block before proceeding.
func (s *States) BlockPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.PreChecks == nil {
		req.Next = s.BlockStartContChecks
		return req
	}

	err := s.runPreChecks(req.Ctx, h.block.PreChecks, h.block.ContChecks)
	if err != nil {
		h.block.State.Status = workflow.Failed
		req.Data.err = err
		req.Next = s.BlockDeferredChecks
		return req
	}

	req.Next = s.BlockStartContChecks

	return req
}

// BlockStartContChecks starts the ContChecks of the current block.
func (s *States) BlockStartContChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.ContChecks == nil {
		close(h.contCheckResult)
		req.Next = s.ExecuteSequences
		return req
	}

	var ctx context.Context
	ctx, h.contCancel = context.WithCancel(context.WithoutCancel(req.Ctx))
	// This re-assignment happens only here because a block is a stack object
	// and the other fields are all pointers that are assigned at the beginning of the block.
	// But contextCanel is not, so it needs to be re-assigned here.
	req.Data.blocks[0] = h

	go func() {
		s.runContChecks(ctx, h.block.ContChecks, h.contCheckResult)
	}()

	req.Next = s.ExecuteSequences
	return req
}

// ExecuteSequences executes the sequences of the current block.
func (s *States) ExecuteSequences(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	failures := atomic.Int64{}

	exceededFailures := func() bool {
		if h.block.ToleratedFailures >= 0 && failures.Load() > int64(h.block.ToleratedFailures) {
			return true
		}
		return false
	}

	// So the limiter is pretty standard, but you might be asking why we have one if the pool is already limiting.
	// Its because g.Go() that uses the pool is going to fire off whatever you give it, even if it blocks on waiting for the pool
	// to have room. So if we call g.Go(), and it blocks and in one that is currently running we go over the failures, we will
	// still end up running the one we just queued up. So we use the limiter to block the g.Go() from even being called.
	limiter := make(chan struct{}, h.block.Concurrency)

	pool, err := pooled.New("", h.block.Concurrency)
	if err != nil {
		panic("bug: failed to create pool: " + err.Error())
	}
	defer pool.Close()

	g := wait.Group{
		Pool: pool,
		Name: "ExecuteSequences",
	}

	for i := 0; i < len(h.block.Sequences); i++ {
		seq := h.block.Sequences[i]

		if _, err := req.Data.contChecksPassing(); err != nil {
			h.block.State.Status = workflow.Failed
			req.Data.err = err
			req.Next = s.BlockDeferredChecks
			return req
		}

		if exceededFailures() {
			h.block.State.Status = workflow.Failed
			req.Data.err = fmt.Errorf("block(%s) has exceeded the tolerated failures", h.block.Name)
			req.Next = s.BlockDeferredChecks
			return req
		}

		limiter <- struct{}{}
		g.Go(
			req.Ctx,
			func(ctx context.Context) error {
				defer func() { <-limiter }()

				// Defense in depth to make sure we don't run more than we should.
				if exceededFailures() {
					return fmt.Errorf("exceeded tolerated failures")
				}

				err := s.execSeq(ctx, seq)
				if err != nil {
					failures.Add(1)
				}
				return err
			},
		)
	}

	waitCtx := context.WithoutCancel(req.Ctx)
	g.Wait(waitCtx) // We don't care about the error here, we just want to wait for all sequences to finish.'

	// Need to recheck in case the last sequence failed and sent us over the edge.
	if h.block.ToleratedFailures >= 0 && failures.Load() > int64(h.block.ToleratedFailures) {
		h.block.State.Status = workflow.Failed
		req.Data.err = fmt.Errorf("block(%s) has exceeded the tolerated failures", h.block.Name)
		req.Next = s.BlockDeferredChecks
		return req
	}

	req.Next = s.BlockPostChecks
	return req
}

// BlockPostChecks runs all PostChecks on the current block.
func (s *States) BlockPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	req.Next = s.BlockDeferredChecks
	if h.block.PostChecks == nil {
		return req
	}

	err := s.runChecksOnce(req.Ctx, h.block.PostChecks)
	if err != nil {
		h.block.State.Status = workflow.Failed
		req.Data.err = err
		return req
	}
	return req
}

// BlockDeferredChecks runs all DeferredChecks on the current block before proceeding.
func (s *States) BlockDeferredChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	req.Next = s.BlockEnd

	if h.block.DeferredChecks == nil {
		return req
	}

	err := s.runChecksOnce(req.Ctx, h.block.DeferredChecks)
	if err != nil {
		h.block.State.Status = workflow.Failed
		req.Data.err = err
		return req
	}

	return req
}

// BlockEnd ends the current block and moves to the next block.
func (s *States) BlockEnd(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	defer func() {
		h.block.State.End = s.now()
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if h.block.BypassChecks != nil && h.block.BypassChecks.State.Status == workflow.Completed {
		h.block.State.Status = workflow.Completed
	} else {
		// For safety reasons, we always check this so we don't get goroutine leaks.
		if h.contCancel != nil {
			h.contCancel()
		}

		// Stop our cont checks if they are still running, get the final result.
		if h.block.ContChecks != nil {
			var err error
			for err = range h.contCheckResult {
				if err != nil {
					break
				}
			}
			if err != nil {
				h.block.State.Status = workflow.Failed
				req.Data.err = err
				req.Next = s.PlanDeferredChecks
				return req
			}
		}

		if h.block.State.Status == workflow.Running {
			h.block.State.Status = workflow.Completed
		} else {
			h.block.State.Status = workflow.Failed
			req.Next = s.PlanDeferredChecks
			return req
		}

		if err := after(req.Ctx, h.block.ExitDelay); err != nil {
			h.block.State.Status = workflow.Stopped
			req.Data.err = err
			req.Next = s.PlanDeferredChecks
			return req
		}
	}

	if len(req.Data.blocks) == 1 {
		req.Data.blocks = nil
	} else {
		req.Data.blocks = req.Data.blocks[1:]
	}
	req.Next = s.ExecuteBlock
	return req
}

// PlanPostChecks stops the ContChecks and runs the PostChecks of the current plan.
func (s *States) PlanPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No matter what the outcome here is, we go to the end state.
	req.Next = s.PlanDeferredChecks

	// We always checks this to avoid programmer mistakes that lead to a goroutine lea
	// 	// We always checks this to avoid programmer mistakes that lead to a goroutine leak.
	if req.Data.contCancel != nil {
		req.Data.contCancel()
	}

	if req.Data.Plan.ContChecks != nil {
		for err := range req.Data.contCheckResult {
			if err != nil {
				req.Data.err = err
				return req
			}
		}
	}

	if req.Data.Plan.PostChecks != nil {
		if err := s.runChecksOnce(req.Ctx, req.Data.Plan.PostChecks); err != nil {
			req.Data.err = err
			return req
		}
	}
	return req
}

// PlanDeferredChecks runs the DeferredChecks.
func (s *States) PlanDeferredChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No matter what the outcome here is, we go to the end state.
	req.Next = s.End

	if req.Data.Plan.DeferredChecks == nil {
		return req
	}
	if err := s.runChecksOnce(req.Ctx, req.Data.Plan.DeferredChecks); err != nil {
		req.Data.err = err
		return req
	}
	return req
}

// End is the final state of the state machine. This is always the last state, regardless of errors.
// This will do the calculations of the final state of the Plan.
func (s *States) End(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan
	defer func() {
		plan.State.End = s.now()
		if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	// Extra cancel, defense in depth.
	if req.Data.contCancel != nil {
		req.Data.contCancel()
	}

	// Runs a new statemachine to calculate the final state of the Plan.
	f := finalStates{}
	req.Next = f.start

	var err error
	req, err = statemachine.Run("finalStates", req)
	if err != nil {
		if errors.Is(err, ErrInternalFailure) {
			log.Println("Plan object did not come out in expected state: %w", err)
		}
	}
	req.Next = nil

	// Promote Data.err to the request if it is not nil.
	if req.Data.err != nil {
		req.Err = req.Data.err
	}

	return req
}

// runBypasses runs all gates in the Plan. If any gate fails, the Plan proceeds. If there
// are no gates, this function returns false as the plan should proceed.
func (s *States) runBypasses(ctx context.Context, bypasses *workflow.Checks) (skip bool) {
	if bypasses == nil {
		return false
	}

	g := wait.Group{}

	g.Go(ctx, func(cts context.Context) error {
		return s.runChecksOnce(cts, bypasses)
	})

	if err := g.Wait(ctx); err != nil {
		return false
	}
	return true
}

// runPreChecks runs all PreChecks and ContChecks. This is a helper function for PlanPreChecks and BlockPreChecks.
func (s *States) runPreChecks(ctx context.Context, preChecks *workflow.Checks, contChecks *workflow.Checks) error {
	if preChecks == nil && contChecks == nil {
		return nil
	}

	g := wait.Group{}

	if preChecks != nil {
		g.Go(ctx, func(ctx context.Context) error {
			return s.runChecksOnce(ctx, preChecks)
		})
	}

	if contChecks != nil {
		g.Go(ctx, func(ctx context.Context) error {
			return s.runChecksOnce(ctx, contChecks)
		})
	}

	return g.Wait(ctx)
}

// runContChecks runs the ContChecks in a loop with a delay between each run until the Context is cancelled.
// It writes the final result to the given channel. If a check fails before the Context is cancelled, the
// error is written to the channel and the function returns.
func (s *States) runContChecks(ctx context.Context, checks *workflow.Checks, resultCh chan error) {
	defer close(resultCh)

	// If the delay is less than or equal to 0, we set it to 1ns to avoid a panic,
	// since time.NewTicker panics if the duration is less than or equal to 0.
	delay := checks.Delay
	if delay <= 0 {
		delay = time.Nanosecond
	}

	t := time.NewTicker(delay)
	defer t.Stop()

	for {
		t.Reset(delay)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := s.runChecksOnce(ctx, checks)
			resultCh <- err
			if err != nil {
				return
			}
		}
	}
}

// runContChecksOnce runs Checks once and writes the result to the store.
func (s *States) runChecksOnce(ctx context.Context, checks *workflow.Checks) error {
	if s.checksRunner != nil {
		return s.checksRunner(ctx, checks)
	}

	resetActions(checks.Actions)

	checks.State.Start = s.now()

	if err := s.store.UpdateChecks(ctx, checks); err != nil {
		log.Fatalf("failed to write ContChecks: %v", err)
	}
	defer func() {
		if err := s.store.UpdateChecks(ctx, checks); err != nil {
			log.Fatalf("failed to write ContChecks: %v", err)
		}
	}()
	defer func() {
		checks.State.End = s.now()
	}()

	if err := s.runActionsParallel(ctx, checks.Actions); err != nil {
		checks.State.Status = workflow.Failed
		return err
	}
	checks.State.Status = workflow.Completed
	return nil
}

// runActionsParallel runs a list of actions in parallel.
func (s *States) runActionsParallel(ctx context.Context, actions []*workflow.Action) error {
	if s.actionsParallelRunner != nil {
		return s.actionsParallelRunner(ctx, actions)
	}
	// Yes, we loop twice, but actions is small and we only want to write to the store once.
	for _, action := range actions {
		action.State.Status = workflow.Running
		action.State.Start = s.now()
		if err := s.store.UpdateAction(ctx, action); err != nil {
			log.Fatalf("failed to write Action: %v", err)
		}
	}

	g := wait.Group{}

	// Run the actions in parallel.
	for _, action := range actions {
		action := action

		g.Go(ctx, func(ctx context.Context) (err error) {
			return s.runAction(ctx, action, s.store)
		})
	}
	return g.Wait(ctx)
}

// execSeq executes a sequence of actions. Any Job failures fail the Sequnence. The Job may retry
// based on the retry policy.
func (s *States) execSeq(ctx context.Context, seq *workflow.Sequence) error {
	seq.State.Status = workflow.Running
	seq.State.Start = s.now()
	if err := s.store.UpdateSequence(ctx, seq); err != nil {
		log.Fatalf("failed to write Sequence: %v", err)
	}
	defer func() {
		seq.State.End = s.now()
		if err := s.store.UpdateSequence(ctx, seq); err != nil {
			log.Fatalf("failed to write Sequence: %v", err)
		}
	}()

	for _, action := range seq.Actions {
		if err := s.runAction(ctx, action, s.store); err != nil {
			seq.State.Status = workflow.Failed
			return err
		}
	}

	seq.State.Status = workflow.Completed
	return nil
}

// runAction runs an action and returns the response or an error. If the response is not the expected
// type, it returns a permanent error that prevents retries.
func (s *States) runAction(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
	if s.actionRunner != nil {
		return s.actionRunner(ctx, action, updater)
	}

	ctx = context.SetActionID(ctx, action.ID)

	req := statemachine.Request[actions.Data]{
		Ctx: ctx,
		Data: actions.Data{
			Action:   action,
			Updater:  updater,
			Registry: s.registry,
		},
		Next: s.actionsSM.Start,
	}
	_, err := statemachine.Run("run action statemachine", req)
	if err != nil {
		return err
	}
	return nil
}

// now returns the current time in UTC. If a nower is set, it uses that to get the time.
func (s *States) now() time.Time {
	if s.nower == nil {
		return time.Now().UTC()
	}
	return s.nower().UTC()
}

// resetActions adjusts all the actions to their initial un-started state.
// This is used by the ContChecks to reset the actions before each run.
func resetActions(actions []*workflow.Action) {
	for _, action := range actions {
		action.State.Status = workflow.NotStarted
		action.State.Start = time.Time{}
		action.State.End = time.Time{}
		action.Attempts = nil
	}
}

func isType(a, b interface{}) bool {
	return reflect.TypeOf(a) == reflect.TypeOf(b)
}

func after(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
	}
	return nil
}
