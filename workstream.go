/*
Package workstream provides a workflow engine that can execute complex workflows using
reusable plugins. This is designed for localized workflows and not workflows on shared mediums.
Aka, there are no policy engines, emergency stop systems or centralization mechanisms that keep
teams from running over each other.

[TBD: Add more details]
*/
package workstream

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/workstream/internal/execute"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"
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
	Err  error
}

// Workstream provides a way to submit and execute workflow.Plans. You only need one Workstream
// per application. It is safe to use concurrently.
type Workstream struct {
	exec  *execute.Plans
	store storage.Vault
}

// New creates a new Workstream.
func New(ctx context.Context, store storage.Vault) (*Workstream, error) {
	if store == nil {
		return nil, fmt.Errorf("storage is required")
	}
	exec, err := execute.New(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	return &Workstream{exec: exec}, nil
}

type defaulter interface {
	defaults()
}

// Submit submits a workflow.Plan to the Workstream for execution. It returns the UUID of the plan.
// If the plan is invalid, an error is returned. The plan is not executed on Submit(), you must use
// Start() to begin execution. Using the Plan object after submitting it results in undefined behavior.
// To get the status of the plan, use the Status method.
func (w *Workstream) Submit(ctx context.Context, plan *workflow.Plan) (uuid.UUID, error) {
	if err := workflow.Validate(plan); err != nil {
		return uuid.Nil, fmt.Errorf("Plan did not validate: %s", err)
	}

	for item := range walk.Plan(context.WithoutCancel(ctx), plan) {
		if def, ok := item.Value.(defaulter); ok {
			def.defaults()
		}
	}
	plan.SubmitTime = w.now()

	if err := w.store.Create(ctx, plan); err != nil {
		return uuid.Nil, fmt.Errorf("Failed to write plan to storage: %w", err)
	}

	return plan.ID, nil
}

// Start begins execution of a plan with the given id. The plan must have been submitted to the workstream.
func (w *Workstream) Start(ctx context.Context, id uuid.UUID) error {
	return w.exec.Start(ctx, id)
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
					ch <-Result[*workflow.Plan]{Data: nil, Err: err}
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
