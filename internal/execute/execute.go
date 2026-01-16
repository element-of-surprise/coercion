// Package executes validates Plan objects, checks Plugins can run in this environment (via Plugins.Init()) and
// allows execution of the Plan objects by starting a statemachine that runs a Plan to completion.
package execute

import (
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"
)

var (
	ErrNotFound = errors.New("not found")
)

// runner runs a Plan through the statemachine.
// In production this is the statemachine.Run function.
type runner func(name string, req statemachine.Request[sm.Data], options ...statemachine.Option) (statemachine.Request[sm.Data], error)

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

	waiters  sync.ShardedMap[uuid.UUID, chan struct{}]
	stoppers sync.ShardedMap[uuid.UUID, context.CancelFunc]

	// runner is the function that runs the statemachine.
	// In production this is the statemachine.Run function.
	runner runner

	// validators is a list of validators that are run on a Plan before it is started.
	validators []validator

	// maxLastUpdate is the maximum amount of time that can pass between updates to a Plan
	// before it is considered stale and cannot be recovered.
	maxLastUpdate time.Duration
	// maxSubmitTime is the maximum amount of time that can pass between submission and start of a Plan.
	maxSubmit time.Duration
	// recovery is true if recovery is allowed.
	recovery bool
}

// Option is an option for configuring a Plans via New.
type Option func(*Plans) error

// WithMaxLastUpdate sets the maximum amount of time that can pass between updates to a Plan.
// If a Plan has not been updated in this amount of time, it is considered stale and cannot be recovered.
// If this is not set, the default is 30 minutes.
func WithMaxLastUpdate(d time.Duration) Option {
	return func(p *Plans) error {
		p.maxLastUpdate = d
		return nil
	}
}

// WithMaxSubmit sets the maximum amount of time that can pass between submission and start of a Plan.
// If a Plan has not been started in this amount of time, it is considered stale and cannot be started.
// If this is not set, the default is 30 minutes.
func WithMaxSubmit(d time.Duration) Option {
	return func(p *Plans) error {
		p.maxSubmit = d
		return nil
	}
}

// WithNoRecovery disables recovery of Plans that are in a Running state.
func WithNoRecovery() Option {
	return func(p *Plans) error {
		p.recovery = false
		return nil
	}
}

// New creates a new Executor. This should only be created once.
func New(ctx context.Context, store storage.Vault, reg *registry.Register, options ...Option) (*Plans, error) {
	e := &Plans{
		registry: reg,
		store:    store,
		waiters:  sync.ShardedMap[uuid.UUID, chan struct{}]{},
		// Note: stoppers isn't currently utilized, this is for when we need to expose a Stop function.
		stoppers:      sync.ShardedMap[uuid.UUID, context.CancelFunc]{},
		runner:        statemachine.Run[sm.Data],
		maxLastUpdate: 30 * time.Minute,
		maxSubmit:     30 * time.Minute,
		recovery:      true,
	}

	for _, o := range options {
		if err := o(e); err != nil {
			return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
		}
	}

	if err := e.initPlugins(ctx); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
	}

	var err error
	e.states, err = sm.New(store, e.registry)
	if err != nil {
		return nil, err
	}

	e.addValidators()

	if e.recovery {
		if err = e.recover(ctx); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
		}
	}

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
	g := context.Pool(ctx).Group()
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
// Please use Stop to stop execution of a Plan. If the plan has already been started, this will return nil.
func (e *Plans) Start(ctx context.Context, id uuid.UUID) error {
	plan, err := e.store.Read(ctx, id)
	if err != nil {
		return err
	}
	if plan == nil {
		return ErrNotFound
	}

	planStatus := plan.State.Get().Status
	switch {
	case planStatus == workflow.NotStarted:
		if err := e.validateStartState(plan); err != nil {
			return err
		}
	case planStatus == workflow.Running:
		return nil
	case planStatus > workflow.Running:
		return nil
	}

	// This ensures that before we return that this will be running.
	state := plan.State.Get()
	state.Status = workflow.Running
	state.Start = e.now()
	plan.State.Set(state)
	if err := e.store.UpdatePlan(ctx, plan); err != nil {
		return err
	}

	e.runPlan(ctx, plan, nil)

	return nil
}

// recover recovers a Plan that is in a Running state in storage and restarts it from where it left off.
// This is used when the Executor starts up.
func (e *Plans) recover(ctx context.Context) error {
	recovery := recover{
		maxAge: e.maxLastUpdate,
		store:  e.store,
	}

	// Get a list of all Plans that need to be recovered.
	req := statemachine.Request[recoverData]{Ctx: ctx, Next: recovery.start}
	var err error
	req, err = statemachine.Run[recoverData]("recover", req)
	if err != nil {
		return err
	}

	if len(req.Data.plans) == 0 {
		context.Log(ctx).Info(ctx, "coercion: no plans to recover")
		return nil
	}

	// recoveryStarted is used to wait for all the recovered plans to start running.
	// runPlan starts its own goroutine and this is used to signal when the plan has started.
	recoveryStarted := make([]chan struct{}, 0, len(req.Data.plans))
	for _, plan := range req.Data.plans {
		context.Log(ctx).Info(ctx, "coercion: recovered plan", "id", plan.ID, "status", plan.State.Get().Status)
		w := make(chan struct{})
		recoveryStarted = append(recoveryStarted, w)
		e.runPlan(ctx, plan, w)
	}

	for _, rs := range recoveryStarted {
		<-rs
	}

	return nil
}

// runPlan runs a Plan through the statemachine. This is a non-blocking call.
func (e *Plans) runPlan(ctx context.Context, plan *workflow.Plan, recoveryStarted chan struct{}) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	e.stoppers.Set(plan.ID, cancel)
	e.waiters.Set(plan.ID, make(chan struct{}))

	context.Pool(ctx).Submit(
		ctx,
		func() {
			defer func() {
				cancel()
				e.stoppers.Del(plan.ID)
				waiter, _ := e.waiters.Get(plan.ID)
				close(waiter)
				e.waiters.Del(plan.ID)
			}()

			next := e.states.Start
			if recoveryStarted != nil {
				next = e.states.Recovery
			}

			req := statemachine.Request[sm.Data]{
				Ctx: runCtx,
				Data: sm.Data{
					Plan:            plan,
					RecoveryStarted: recoveryStarted,
				},
				Next: next,
			}

			// NOTE: We are not handling the error here, as we are not returning it to the caller
			// and doesn't actually matter. All errors are encapsulated in the Plan's state.
			if _, err := e.runner(plan.Name, req); err != nil {
				context.Log(ctx).Error(ctx, "plan execution failed", "id", plan.ID, "error", err)
			}
		},
	)
}

func (e *Plans) now() time.Time {
	return time.Now().UTC()
}

// Wait waits for a Plan to finish execution. Cancelling the Context will stop waiting and
// return context.Canceled.
func (e *Plans) Wait(ctx context.Context, id uuid.UUID) error {
	waiter, ok := e.waiters.Get(id)
	if !ok {
		// Plan is not running, check if it exists.
		plan, err := e.store.Read(ctx, id)
		if err != nil {
			return err
		}
		switch plan.GetState().Status {
		case workflow.NotStarted:
			return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan(%s) is not started", id))
		case workflow.Running:
			return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("bug: plan(%s) has a running state, but isn't in the waiters", id))
		}
		return nil
	}

	select {
	case <-ctx.Done():
		return context.Canceled
	case <-waiter:
		return nil
	}
}

// getSetStates provides an interface for grabbing the State struct from workflow objects and setting them.
type getSetStates interface {
	GetState() workflow.State
	SetState(state workflow.State)
}

type ider interface {
	GetID() uuid.UUID
	SetID(uuid.UUID)
}

// validateStartState validates that the plan is in a valid state to be started.
func (p *Plans) validateStartState(plan *workflow.Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if p.maxSubmit == 0 {
		return fmt.Errorf("maxSubmit is zero")
	}
	if plan.SubmitTime.IsZero() {
		return fmt.Errorf("Plan.SubmitTime is zero")
	}

	if plan.SubmitTime.Add(p.maxSubmit).Before(time.Now()) {
		return fmt.Errorf("plan is stale, submit time is too old")
	}

	for item := range walk.Plan(plan) {
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

	if len(action.Attempts.Get()) != 0 {
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
	if get, ok := i.Value.(getSetStates); ok {
		state := get.GetState()
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
