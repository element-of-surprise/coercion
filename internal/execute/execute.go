// Package executes validates Plan objects, checks Plugins can run in this environment (via Plugins.Init()) and
// allows execution of the Plan objects by starting a statemachine that runs a Plan to completion.
package execute

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/element-of-surprise/workstream/internal/execute/sm"
	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/gostdlib/concurrency/prim/wait"
	"github.com/gostdlib/ops/statemachine"
)

// Plans handles execution of workflow.Plan instances for a Workstream.
type Plans struct {
	registry *registry.Register
	store    storage.Vault

	states *sm.States

	mu       sync.Mutex // protects stoppers
	stoppers map[uuid.UUID]context.CancelFunc
}

// New creates a new Executor. This should only be created once.
func New(ctx context.Context, store storage.Vault) (*Plans, error) {
	e := &Plans{}

	if err := e.initPlugins(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	var err error
	e.states, err = sm.New(store, e.registry)
	if err != nil {
		return nil, err
	}

	return e, nil
}

// initPlugins initializes all plugins in the registry to make sure they
// meet the preconditions for execution.
func (e *Plans) initPlugins(ctx context.Context) error {
	if e.registry == nil {
		e.registry = registry.Plugins
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

// Start starts a previously Submitted Plan by its ID. Cancelling the Context will not Stop execution.
// Please use Stop to stop execution of a Plan.
func (e *Plans) Start(ctx context.Context, id uuid.UUID) error {
	plan, err := e.store.Read(ctx, id)
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
		e.stoppers[plan.ID] = cancel
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			delete(e.stoppers, plan.ID)
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
	}()

	return nil
}

func (e *Plans) now() time.Time {
	return time.Now().UTC()
}

// getStater provides an interface for grabbing the State struct from workflow objects.
// This is used to validate that the starting state of the plan is correct before starting it.
type getStater interface {
	GetState() *workflow.State
}

type ider interface {
	GetID() uuid.UUID
	SetID(uuid.UUID)
}

// validateStartState validates that the plan is in a valid state to be started.
// TODO(element-of-surprise): Add validation for check vs non-check actions.
// TODO(element-of-surprise): Add validation for no-delays on non-ContChecks.
func (e *Plans) validateStartState(ctx context.Context, plan *workflow.Plan) error {
	for item := range walk.Plan(context.WithoutCancel(ctx), plan) {
		if hasID, ok := item.Value.(ider); ok {
			if hasID.GetID() == uuid.Nil {
				return fmt.Errorf("Object(%T): ID is nil", item.Value)
			}
		}else{
			return fmt.Errorf("Object(%T): does not implement ider", item.Value)
		}

		if get, ok := item.Value.(getStater); ok {
			state := get.GetState()
			if state == nil {
				return fmt.Errorf("Object(%T).State is nil")
			}
			if state.Status != workflow.NotStarted {
				return fmt.Errorf("internal status is not NotStarted")
			}
			if !state.Start.IsZero() {
				return fmt.Errorf("internal start is not zero")
			}
			if !state.End.IsZero() {
				return fmt.Errorf("internal end is not zero")
			}
		}
		if action, ok := item.Value.(*workflow.Action); ok {
			if action.Attempts != nil {
				return fmt.Errorf("action(%s).Attempts was non-nil", action.Name)
			}

			plug := e.registry.Plugin(action.Plugin)
			if plug == nil {
				return fmt.Errorf("plugin(%s) not found", action.Plugin)
			}
		}
	}
	return nil
}
