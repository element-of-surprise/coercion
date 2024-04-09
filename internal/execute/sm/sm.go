// Package sm holds the states of our executor statemachine.
package sm

import (
	"context"
	"fmt"
	"log"
	"path"
	"reflect"
	"time"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"

	"github.com/gostdlib/concurrency/goroutines/pooled"
	"github.com/gostdlib/concurrency/prim/wait"
	"github.com/gostdlib/ops/retry/exponential"
	"github.com/gostdlib/ops/statemachine"
)

// registry represents a registry of plugins
type registry interface {
	// Plugin returns a plugin by name. If the plugin is not found, nil is returned.
	Plugin(name string) plugins.Plugin
}

// block is a wrapper around a workflow.Block that contains additional information for the statemachine.
type block struct  {
	block *workflow.Block

	contCancel context.CancelFunc
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
}

// contChecks will check if any of the continuous checkes on the Plan or the current block have failed.
// If a check has failed, the type of the object that failed is returned (OTPlan or OTBlock).
func (d Data) contChecks() (workflow.ObjectType, error) {
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

// States is the statemachine that handles the execution of a Plan.
type States struct {
	store storage.Vault
	registry registry

	nower nower
}

// New creates a new States statemachine.
func New(store storage.Vault, registry registry) (*States, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	return &States{
		store: store,
		registry: registry,
	}, nil
}

// Start starts execution of the Plan. This is the first state of the statemachine.
func (s *States) Start(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan

	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	plan.State.Status = workflow.Running
	plan.State.Start = s.now()

	if err := s.store.Create(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}

	req.Next = s.PlanPreChecks
	return req
}

// PlanPreChecks runs all PreChecks and ContChecks on the Plan before proceeding.
func (s *States) PlanPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	_, err := s.runPreChecks(req.Ctx, req.Data.Plan.PreChecks, req.Data.Plan.ContChecks)
	if err != nil {
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

	h.block.State.Status = workflow.Running
	if err := s.store.Block().UpdateBlock(req.Ctx, h.block); err != nil {
		log.Printf("failed to write Block: %v", err)
		return req
	}
	req.Next = s.BlockPreChecks
	return req
}

// BlockPreChecks runs all PreChecks and ContChecks on the current block before proceeding.
func (s *States) BlockPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.ContChecks != nil {
		_, err := s.runPreChecks(req.Ctx, h.block.PreChecks, h.block.ContChecks)
		if err != nil {
			h.block.State.Status = workflow.Failed
			return req
		}
	}

	req.Next = s.BlockStartContChecks

	return req
}

// BlockStartContChecks starts the ContChecks of the current block.
func (s *States) BlockStartContChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.ContChecks != nil {
		var ctx context.Context
		ctx, h.contCancel = context.WithCancel(context.WithoutCancel(req.Ctx))

		go func() {
			s.runContChecks(ctx, h.block.ContChecks, h.contCheckResult)
		}()
	}

	req.Next = s.ExecuteSequences
	return req
}

// ExecuteSequences executes the sequences of the current block.
func (s *States) ExecuteSequences(req statemachine.Request[Data]) statemachine.Request[Data ]{
	h := req.Data.blocks[0]

	name := path.Join(req.Data.Plan.Name, h.block.Name,"ExecuteSequences")

	pool, err := pooled.New(name, h.block.Concurrency)
	if err != nil {
		panic("bug: failed to create pool")
	}
	defer pool.Close()

	failures := 0
	for i := 0; i < len(h.block.Sequences); i++ {
		seq := h.block.Sequences[i]
		if h.block.ToleratedFailures >= 0 && failures > h.block.ToleratedFailures {
			h.block.State.Status = workflow.Failed
			return req
		}

		if _, err := req.Data.contChecks(); err != nil {
			h.block.State.Status = workflow.Failed
			return req
		}

		err := pool.Submit(
			req.Ctx,
			func(ctx context.Context) {
				if err := s.execSeq(ctx, seq); err != nil {
					failures++
				}
			},
		)
		if err != nil {
			panic("Bug: pool.Submit should never fail")
		}
	}

	req.Next = s.BlockPostChecks
	return req
}

func (s *States) BlockPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	if h.block.ContChecks != nil {
		// Cancel the ContChecks if they are still running and wait for the final result.
		h.contCancel()
		if err := <-h.contCheckResult; err != nil {
			h.block.State.Status = workflow.Failed
			return req
		}
	}

	err := s.runChecks(req.Ctx, h.block.PostChecks.Actions)
	if err != nil {
		h.block.State.Status = workflow.Failed
		return req
	}

	req.Next = s.BlockEnd
	return req
}

// BlockEnd ends the current block and moves to the next block.
func (s *States) BlockEnd(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	// Stop our cont checks if they are still running, get the final result.
	h.contCancel()
	if err := <-h.contCheckResult; err == nil {
		h.block.State.Status = workflow.Completed
	}else{
		h.block.State.Status = workflow.Failed
	}

	if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
		log.Printf("failed to write Block: %v", err)
		return req
	}
	req.Data.blocks = req.Data.blocks[1:]
	req.Next = s.ExecuteBlock
	return req
}

// PlanPostChecks stops the ContChecks and runs the PostChecks of the current plan.
func (s *States) PlanPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	if req.Data.Plan.ContChecks != nil {
		req.Data.contCancel()
		if err := <-req.Data.contCheckResult; err != nil {
			return req
		}
	}

	if err := s.runChecks(req.Ctx, req.Data.Plan.PostChecks.Actions); err != nil {
		return req
	}
	return req
}

// runPreChecks runs all PreChecks and ContChecks. This is a helper function for PlanPreChecks and BlockPreChecks.
func (s *States) runPreChecks(ctx context.Context, preChecks *workflow.Checks, contChecks *workflow.Checks) (preFail bool, err error) {
	if preChecks == nil && contChecks == nil {
		return false, nil
	}

	g := wait.Group{}

	preCheckFail := false
	if preChecks != nil {
		g.Go(ctx, func(ctx context.Context) error{
			if err := s.runChecks(ctx, preChecks.Actions); err != nil {
				preCheckFail = true
				return err
			}
			return nil
		})
	}

	if contChecks != nil {
		g.Go(ctx, func(ctx context.Context) error{
			return s.runChecks(ctx, contChecks.Actions)
		})
	}

	if err := g.Wait(ctx); err != nil {
		if preCheckFail {
			return true, err
		}
		return false, err
	}
	return false, nil
}

// runContChecks runs the ContChecks in a loop with a delay between each run until the Context is cancelled.
// It writes the final result to the given channel. If a check fails before the Context is cancelled, the
// error is written to the channel and the function returns.
func (s *States) runContChecks(ctx context.Context, checks *workflow.Checks, resultCh chan error) {
	defer close(resultCh)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(checks.Delay):
			resetActions(checks.Actions)
			if err := s.store.UpdateChecks(ctx, checks); err != nil {
				log.Fatalf("failed to write ContChecks: %v", err)
			}

			if err := s.runChecks(ctx, checks.Actions); err != nil {
				resultCh <- err
				return
			}
			if err := s.store.UpdateChecks(ctx, checks); err != nil {
				log.Fatalf("failed to write ContChecks: %v", err)
			}
		}
	}
}

// runChecks runs the given actions in parallel and waits for all of them to finish.
func (s *States) runChecks(ctx context.Context, actions []*workflow.Action) error {
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
	if err := s.store.UpdateSequence(ctx, seq); err != nil {
		log.Fatalf("failed to write Sequence: %v", err)
	}
	defer func() {
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
func (s *States) runAction(ctx context.Context, action *workflow.Action, writer storage.ActionUpdater) error {
	action.State.Start = s.now()
	action.State.Status = workflow.Running
	if err := writer.UpdateAction(ctx, action); err != nil {
		log.Fatalf("failed to write Action: %v", err)
	}
	defer func() {
		if err := writer.UpdateAction(ctx, action); err != nil {
			log.Fatalf("failed to write Action: %v", err)
		}
	}()
	defer func() {
		action.State.End = s.now()
	}()


	p := s.registry.Plugin(action.Plugin)
	// This is defense in depth. The plugin should be checked when the Plan is created.
	if p == nil {
		action.State.Status = workflow.Failed
		return fmt.Errorf("plugin %s not found", action.Plugin)
	}

	backoff, err := exponential.New(
		exponential.WithPolicy(p.RetryPolicy()),
	)

	err = backoff.Retry(
		ctx,
		func(ctx context.Context, record exponential.Record) error {
			if len(action.Attempts) > action.Retries {
				return exponential.ErrPermanent
			}

			defer func() {
				if err := writer.UpdateAction(ctx, action); err != nil {
					log.Fatalf("failed to write Action: %v", err)
				}
			}()

			attempt := workflow.Attempt{
				Start: s.now(),
			}

			runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), action.Timeout)
			attempt.Resp, attempt.Err = p.Execute(runCtx, action.Req)
			cancel()

			// We make sure the response is the expected type. If not, we return a permanent error.
			// This case means the plugin is not behaving as expected and we should avoid conversion panics
			// by not returning the junk they gave us.
			if attempt.Err == nil {
				expect := p.Response()
				if !isType(attempt.Resp, expect) {
					attempt.Resp = nil
					attempt.Err = &plugins.Error{
						Message: fmt.Sprintf("plugin(%s) returned a type %T but expected %T", p.Name(), attempt.Resp, expect),
						Permanent: true,
					}
				}
			}
			action.Attempts = append(action.Attempts, &attempt)
			if attempt.Err.Permanent {
				return fmt.Errorf("%w %w", attempt.Err, exponential.ErrPermanent)
			}
			return attempt.Err
		},
	)

	if err != nil {
		action.State.Status = workflow.Failed
		return err
	}

	action.State.Status = workflow.Completed
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
