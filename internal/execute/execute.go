package execute

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/element-of-surprise/workstream/internal/execute/sm"
	"github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/gostdlib/concurrency/prim/wait"
	"github.com/gostdlib/ops/statemachine"
)

type registry interface {
	Plugin(name string) plugins.Plugin
	Plugins() chan plugins.Plugin
}

// Plans handles execution of workflow.Plan instances for a Workstream.
type Plans struct {
	registry registry
	store    storage.ReadWriter

	states *sm.States

	mu       sync.Mutex // protects stoppers
	stoppers map[uuid.UUID]context.CancelFunc
}

// New creates a new Executor. This should only be created once.
func New(ctx context.Context, store storage.ReadWriter) (*Plans, error) {
	e := &Plans{}

	if err := e.initPlugins(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	e.states = sm.New(store, e.registry)

	return e, nil
}

// initPlugins initializes all plugins in the registry to make sure they
// meet the preconditions for execution.
func (e *Plans) initPlugins(ctx context.Context) error {
	if e.registry == nil {
		e.registry = plugins.Registry
	}

	g := wait.Group{}
	for plugin := range e.registry.Plugins() {
		plugin := plugin
		g.Go(ctx, func(ctx context.Context) error {
			err := plugin.Init()
			if err != nil {
				return fmt.Errorf("plugin(%s) failed to initialize: %w", plugin.Name(), err)
			}
			return nil
		})
	}

	return g.Wait(ctx)
}

// Start stats a previously Submitted Plan by its ID. Cancelling the Context will not Stop execution.
// Please use Stop to stop execution of a Plan.
func (e *Plans) Start(ctx context.Context, id uuid.UUID) error {
	plan, err := e.store.ReadPlan(ctx, id)
	if err != nil {
		return err
	}

	if err := e.validateStartState(ctx, plan); err != nil {
		return fmt.Errorf("invalid plan state: %w", err)
	}

	go func() {
		runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()

		e.mu.Lock()
		e.stoppers[plan.Internals.Internal.ID] = cancel
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			delete(e.stoppers, plan.Internals.Internal.ID)
			e.mu.Unlock()
		}()

		req := statemachine.Request[sm.Data]{
			Ctx: runCtx,
			Data: sm.Data{
				Plan: plan,
			},
			Next: e.states.Start,
		}

		// NOTE: We are not handling the error here, as we are not returning it to the caller
		// and doesn't actually matter. All errors are encapsulated in the Plan's state.
		statemachine.Run(plan.Name, req)

		e.writePlanState(ctx, plan)
	}()

	return nil
}

func (e *Plans) now() time.Time {
	return time.Now().UTC()
}

// getInternal provides an interface for grabbing the Internal struct from workflow objects.
// This is used to validate that the starting state of the plan is correct before starting it.
type getInternal interface {
	GetInternal() *workflow.Internal
}

// validateStartState validates that the plan is in a valid state to be started.
// TODO(element-of-surprise): Add validation for check vs non-check actions.
func (e *Plans) validateStartState(ctx context.Context, p *workflow.Plan) error {
	for item := range walk.Plan(context.WithoutCancel(ctx), p) {
		if get, ok := item.Value.(getInternal); ok {
			internal := get.GetInternal()
			if internal == nil {
				return fmt.Errorf("internal is nil")
			}
			if internal.ID == uuid.Nil {
				return fmt.Errorf("internal ID is nil")
			}
			if internal.Status != workflow.NotStarted {
				return fmt.Errorf("internal status is not NotStarted")
			}
			if !internal.Start.IsZero() {
				return fmt.Errorf("internal start is not zero")
			}
			if !internal.End.IsZero() {
				return fmt.Errorf("internal end is not zero")
			}
		}
		if action, ok := item.Value.(*workflow.Action); ok {
			if action.Attempts != nil {
				return fmt.Errorf("action(%s).Attempts was non-nil", action.Name)
			}

			p := e.registry.Plugin(action.Plugin)
			if p == nil {
				return fmt.Errorf("plugin(%s) not found", action.Plugin)
			}
		}
	}
	return nil
}

// writePlanState looks at teh current state of the Plan and figures out what the final
// state should be. It then writes that state to the storage.
func (e *Plans) writePlanState(ctx context.Context, p *workflow.Plan) error {

forLoop:
	for item := range walk.Plan(context.WithoutCancel(ctx), p) {
		switch item.Value.Type() {
		case workflow.OTPlan:
			switch workflow.Failed {
			case p.PreChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRPreCheck
				break forLoop
			case p.ContChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRContCheck
				break forLoop
			case p.PostChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRPostCheck
			}
		case workflow.OTBlock:
			b := item.Block()
			switch workflow.Failed {
			case b.PreChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRBlock
				break forLoop
			case b.ContChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRBlock
				break forLoop
			case b.PostChecks.Internal.Status:
				p.Internals.Internal.Status = workflow.Failed
				p.Internals.Reason = workflow.FRBlock
			}
		}
	}
	return e.store.Write(ctx, p)
}
