/*
Package coercion provides a workflow engine that can execute complex workflows using
reusable plugins. This is designed for localized workflows and not workflows on shared mediums.
Aka, there are no policy engines, emergency stop systems or centralization mechanisms that keep
teams from running over each other.

[TBD: Add more details]
*/
package coercion

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
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
}

// Option is an optional argument for New(). For future use.
type Option func(*Workstream) error

// New creates a new Workstream.
func New(ctx context.Context, reg *registry.Register, store storage.Vault, options ...Option) (*Workstream, error) {
	if store == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry is required")
	}

	ws := &Workstream{reg: reg, store: store}
	for _, o := range options {
		if err := o(ws); err != nil {
			return nil, err
		}
	}

	exec, err := execute.New(ctx, store, reg)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
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
		return uuid.Nil, fmt.Errorf("Plan did not validate: %s", err)
	}

	for item := range walk.Plan(context.WithoutCancel(ctx), plan) {
		if def, ok := item.Value.(defaulter); ok {
			def.Defaults()
		}
	}
	plan.SubmitTime = w.now()

	if err := w.store.Create(ctx, plan); err != nil {
		return uuid.Nil, fmt.Errorf("Failed to write plan to storage: %w", err)
	}

	return plan.ID, nil
}

// requestDefaults finds all request objects in the plan and calls their Defaults() method.
func (w *Workstream) requestDefaults(ctx context.Context, plan *workflow.Plan) {
	for item := range walk.Plan(ctx, plan) {
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
	for item := range walk.Plan(ctx, plan) {
		if item.Value.Type() == workflow.OTAction {
			a := item.Action()
			if a.HasRegister() {
				return fmt.Errorf("action(%s) had register set, which is not allowed", a.Name)
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

// Status returns a channel that will receive updates on the status of the plan with the given id. The interval
// is the time between updates. The channel will be closed when the plan is complete or an error occurs.
// If the Context is canceled, the channel will be closed and the final Result will have Err set. Otherwise, regardless
// of the final status of the Plan, the last Result will have Err set to nil.
func (w *Workstream) Status(ctx context.Context, id uuid.UUID, interval time.Duration) chan Result[*workflow.Plan] {
	ch := make(chan Result[*workflow.Plan], 1)

	t := time.NewTicker(interval)

	go func() {
		defer close(ch)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				ch <- Result[*workflow.Plan]{Data: nil, Err: ctx.Err()}
			case <-t.C:
				plan, err := w.store.Read(ctx, id)
				if err != nil {
					ch <- Result[*workflow.Plan]{Data: nil, Err: err}
					return
				}
				ch <- Result[*workflow.Plan]{Data: plan, Err: nil}
				if plan.State.Status != workflow.Running {
					return
				}
			}
		}
	}()
	return ch
}

func (w *Workstream) now() time.Time {
	return time.Now().UTC()
}
