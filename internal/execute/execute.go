// Package executes validates Plan objects, checks Plugins can run in this environment (via Plugins.Init()) and
// allows execution of the Plan objects by starting a statemachine that runs a Plan to completion.
package execute

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/gostdlib/concurrency/prim/wait"
	"github.com/gostdlib/ops/statemachine"
)

var (
	ErrNotFound = errors.New("not found")
)

// runner runs a Plan through the statemachine.
// In production this is the statemachine.Run function.
type runner func(name string, req statemachine.Request[sm.Data], options ...statemachine.Option[sm.Data]) (statemachine.Request[sm.Data], error)

// validator validates a workflow.Object.
type validator func(walk.Item) error

// Plans handles execution of workflow.Plan instances for a Workstream.
type Plans struct {
	// registry is the registry of plugins that can be used to execute Plans.
	registry *registry.Register
	// store is the storage backend for the Plans.
	store storage.Vault

	// states is the statemachine that runs the Plans.
	states *sm.States

	mu       sync.Mutex // protects stoppers
	waiters  map[uuid.UUID]chan struct{}
	stoppers map[uuid.UUID]context.CancelFunc

	// runner is the function that runs the statemachine.
	// In production this is the statemachine.Run function.
	runner runner

	// validators is a list of validators that are run on a Plan before it is started.
	validators []validator
}

// New creates a new Executor. This should only be created once.
func New(ctx context.Context, store storage.Vault, reg *registry.Register) (*Plans, error) {
	e := &Plans{
		registry: reg,
		store:    store,
		waiters:  map[uuid.UUID]chan struct{}{},
		stoppers: map[uuid.UUID]context.CancelFunc{},
		runner:   statemachine.Run[sm.Data],
	}

	if err := e.initPlugins(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}

	var err error
	e.states, err = sm.New(store, e.registry)
	if err != nil {
		return nil, err
	}

	e.addValidators()

	return e, nil
}

func (e *Plans) addValidators() {
	e.validators = []validator{
		e.validateID,
		e.validateState,
		e.validatePlan,
		e.validateAction,
	}
}

// initPlugins initializes all plugins in the registry to make sure they
// meet the preconditions for execution.
func (e *Plans) initPlugins(ctx context.Context) error {
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

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	e.mu.Lock()
	e.stoppers[plan.ID] = cancel
	e.waiters[plan.ID] = make(chan struct{})
	e.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			e.mu.Lock()
			delete(e.stoppers, plan.ID)
			close(e.waiters[plan.ID])
			delete(e.waiters, plan.ID)
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
		e.runner(plan.Name, req)
	}()

	return nil
}

func (e *Plans) now() time.Time {
	return time.Now().UTC()
}

// Wait waits for a Plan to finish execution. Cancelling the Context will stop waiting and
// return context.Canceled. If the Plan is not found, this will return ErrNotFound.
func (e *Plans) Wait(ctx context.Context, id uuid.UUID) error {
	e.mu.Lock()
	waiter, ok := e.waiters[id]
	e.mu.Unlock()

	if !ok {
		return ErrNotFound
	}

	select {
	case <-ctx.Done():
		return context.Canceled
	case <-waiter:
		return nil
	}
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
func (p *Plans) validateStartState(ctx context.Context, plan *workflow.Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}

	for item := range walk.Plan(context.WithoutCancel(ctx), plan) {
		for _, v := range p.validators {
			if err := v(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Plans) validatePlan(i walk.Item) error {
	if i.Value.Type() != workflow.OTPlan {
		return nil
	}

	plan := i.Value.(*workflow.Plan)
	if plan.SubmitTime.IsZero() {
		return fmt.Errorf("Plan.SubmitTime is zero")
	}
	if plan.Reason != workflow.FRUnknown {
		return fmt.Errorf("Plan.Reason is not FRUnknown")
	}
	return nil
}

func (p *Plans) validateAction(i walk.Item) error {
	if i.Value.Type() != workflow.OTAction {
		return nil
	}

	action := i.Value.(*workflow.Action)

	if action.Attempts != nil {
		return fmt.Errorf("action(%s).Attempts was non-nil", action.Name)
	}

	plug := p.registry.Plugin(action.Plugin)
	if plug == nil {
		return fmt.Errorf("plugin(%s) not found", action.Plugin)
	}

	switch i.Chain[len(i.Chain)-1].Type() {
	case workflow.OTCheck:
		if !plug.IsCheck() {
			return fmt.Errorf("plugin(%s) is not a check plugin, but in a Checks object", action.Plugin)
		}
	}
	return nil
}

// validateID validates that the object has a non-nil ID.
func (e *Plans) validateID(i walk.Item) error {
	const v7 = uuid.Version(byte(7))

	if hasID, ok := i.Value.(ider); ok {
		if hasID.GetID() == uuid.Nil {
			return fmt.Errorf("Object(%T): ID is nil", i.Value)
		}
		if hasID.GetID().Version() != v7 {
			return fmt.Errorf("Object(%T): ID is not a V7 UUID", i.Value)
		}
		return nil
	}
	return fmt.Errorf("Object(%T): does not implement ider", i.Value)
}

// validateState validates that the object is in a valid state to be started.
func (e *Plans) validateState(i walk.Item) error {
	if get, ok := i.Value.(getStater); ok {
		state := get.GetState()
		if state == nil {
			return fmt.Errorf("Object(%T).State is nil", i.Value)
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
		return nil
	}
	return fmt.Errorf("Object(%T): does not implement getStater", i.Value)
}
