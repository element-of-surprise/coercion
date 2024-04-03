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

// This makes UUUID generation much faster.
func init() {
	uuid.EnableRandPool()
}

type Workstream struct {
	exec  *execute.Plans
	store storage.ReadWriter
}

func New(ctx context.Context, store storage.ReadWriter) (*Workstream, error) {
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

// Submit submits a plan to the workstream for execution. It returns the UUID of the plan.
// If the plan is invalid, an error is returned. The plan is not executed, you must use
// Start to begin execution. Using the plan after submitting it results in undefined behavior.
func (w *Workstream) Submit(ctx context.Context, plan *workflow.Plan) (uuid.UUID, error) {
	if err := workflow.Validate(plan); err != nil {
		return uuid.Nil, fmt.Errorf("Plan did not validate: %s", err)
	}

	for item := range walk.Plan(context.WithoutCancel(ctx), plan) {
		if def, ok := item.Value.(defaulter); ok {
			def.defaults()
		}
	}
	plan.Internals.SubmitTime = w.now()

	if err := w.store.Write(ctx, plan); err != nil {
		return uuid.Nil, fmt.Errorf("Failed to write plan to storage: %w", err)
	}

	return plan.Internals.Internal.ID, nil
}

// Start begins execution of a plan with the given id. The plan must have been submitted to the workstream.
func (w *Workstream) Start(ctx context.Context, id uuid.UUID) error {
	return w.exec.Start(ctx, id)
}

func (w *Workstream) now() time.Time {
	return time.Now().UTC()
}
