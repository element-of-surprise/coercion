/*
Package coercion provides a workflow engine that can execute complex workflows using
reusable plugins. This is designed for localized workflows and not workflows on shared mediums.
Aka, there are no policy engines, emergency stop systems or centralization mechanisms that keep
teams from running over each other.

Use of this package encourages using github.com/gostdlib/base/init.Service() in your main after your flag parsing.
*/
package coercion

import (
	"fmt"
	"iter"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
)

// This makes UUID generation much faster.
func init() {
	uuid.EnableRandPool()
}

// Result is a generic result returned in a stream of results.
type Result[T any] struct {
	// Data is the data to be returned.
	Data T
	// Err is an error in the stream. Data will be its type's zero value.
	Err error
}

// Workstream provides a way to submit and execute workflow.Plans. You only need one Workstream
// per application. It is safe to use concurrently.
type Workstream struct {
	reg   *registry.Register
	exec  *execute.Plans
	store storage.Vault

	execOptions []execute.Option
}

// Option is an optional argument for New(). For future use.
type Option func(*Workstream) error

// WithMaxLastUpdate sets the maximum amount of time that can pass between updates to a Plan.
// If a Plan has not been updated in this amount of time, it is considered stale and cannot be recovered.
// If this is not set, the default is 30 minutes.
func WithMaxLastUpdate(d time.Duration) Option {
	return func(w *Workstream) error {
		w.execOptions = append(w.execOptions, execute.WithMaxLastUpdate(d))
		return nil
	}
}

// WithMaxSubmit sets the maximum amount of time that can pass between submission and start of a Plan.
// If a Plan has not been started in this amount of time, it is considered stale and cannot be started.
// If this is not set, the default is 30 minutes.
func WithMaxSubmit(d time.Duration) Option {
	return func(p *Workstream) error {
		p.execOptions = append(p.execOptions, execute.WithMaxSubmit(d))
		return nil
	}
}

// New creates a new Workstream.
func New(ctx context.Context, reg *registry.Register, store storage.Vault, options ...Option) (*Workstream, error) {
	if store == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("storage is required"))
	}
	if reg == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("registry is required"))
	}

	// Some storage systems may need to recover from a previous state after a crash.
	if r, ok := store.(storage.Recovery); ok {
		if err := r.Recovery(ctx); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
		}
	}

	ws := &Workstream{reg: reg, store: store}
	for _, o := range options {
		if err := o(ws); err != nil {
			return nil, err
		}
	}

	exec, err := execute.New(ctx, store, reg, ws.execOptions...)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
	}
	ws.exec = exec

	return ws, nil
}

type defaulter interface {
	Defaults()
}

// Submit submits a workflow.Plan to the Workstream for execution. It returns the UUID of the plan.
// If the plan is invalid, an error is returned. The plan is not executed on Submit(), you must use
// Start() to begin execution. Using the Plan object after submitting it results in undefined behavior.
// To get the status of the plan, use the Status method.
func (w *Workstream) Submit(ctx context.Context, plan *workflow.Plan) (uuid.UUID, error) {
	if err := w.populateRegistry(ctx, plan); err != nil {
		return uuid.Nil, err
	}
	w.requestDefaults(ctx, plan)

	if err := workflow.Validate(plan); err != nil {
		return uuid.Nil, err
	}

	for item := range walk.Plan(plan) {
		if def, ok := item.Value.(defaulter); ok {
			def.Defaults()
		}
	}
	plan.SubmitTime = w.now()

	if err := w.store.Create(ctx, plan); err != nil {
		return uuid.Nil, err
	}

	return plan.ID, nil
}

type setPlanIDer interface {
	SetPlanID(uuid.UUID)
}

// requestDefaults finds all request objects in the plan and calls their Defaults() method.
func (w *Workstream) requestDefaults(ctx context.Context, plan *workflow.Plan) {
	for item := range walk.Plan(plan) {
		if item.Value.Type() != workflow.OTPlan {
			item.Value.(setPlanIDer).SetPlanID(plan.ID)
		}
		switch item.Value.Type() {
		case workflow.OTAction:
			a := item.Action()
			if v, ok := a.Req.(defaulter); ok {
				v.Defaults()
			}
		}
	}
}

func (w *Workstream) populateRegistry(ctx context.Context, plan *workflow.Plan) error {
	for item := range walk.Plan(plan) {
		if item.Value.Type() == workflow.OTAction {
			a := item.Action()
			if a.HasRegister() {
				return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("action(%s) had register set, which is not allowed", a.Name))
			}
			a.SetRegister(w.reg)
		}
	}
	return nil
}

// Start begins execution of a plan with the given id. The plan must have been submitted to the workstream.
func (w *Workstream) Start(ctx context.Context, id uuid.UUID) error {
	return w.exec.Start(ctx, id)
}

// Plan returns the plan with the given id. If the plan does not exist, an error is returned.
func (w *Workstream) Plan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	return w.store.Read(ctx, id)
}

// Wait waits for the plan with the given id to complete and returns the Plan's final state.
// If the plan does not exist, an error is returned. If the context is canceled, the error
// will be context.Canceled.
func (w *Workstream) Wait(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	err := w.exec.Wait(ctx, id)
	if err != nil {
		if err == context.Canceled || err == context.DeadlineExceeded {
			return nil, context.Canceled
		}
		if err != execute.ErrNotFound {
			return nil, err
		}
	}
	return w.store.Read(ctx, id)
}

// Status returns an iterator that will receive updates on the status of the plan with the given id. The interval
// is the time between updates. Iteration will terminate when the plan is complete or an error occurs.
// If the Context is canceled, this will stop iteration. It is not necessary to cancel the iterator, but it will
// be running in the background until the interval expires. Regardless of the final status of the Plan,
// the last Result will have Err set to nil.
func (w *Workstream) Status(ctx context.Context, id uuid.UUID, interval time.Duration) iter.Seq[Result[*workflow.Plan]] {
	return func(yield func(Result[*workflow.Plan]) bool) {
		t := time.NewTicker(interval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				plan, err := w.store.Read(ctx, id)
				if err != nil {
					yield(Result[*workflow.Plan]{Data: nil, Err: err})
					return
				}
				if !yield(Result[*workflow.Plan]{Data: plan, Err: nil}) {
					return
				}
				if plan.State.Status != workflow.Running {
					return
				}
			}
		}
	}
}

func (w *Workstream) now() time.Time {
	return time.Now().UTC()
}
