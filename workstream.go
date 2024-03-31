package workstream

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"
	"github.com/google/uuid"
)

func init() {
	uuid.EnableRandPool()
}

type Workstream struct {
	running map[uuid.UUID]*workflow.Plan
	storage func(plan *workflow.Plan) error
}

func New() *Workstream {
	return &Workstream{}
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
	plan.Internal.SubmitTime = w.now()

	if err := w.storage(plan); err != nil {
		return uuid.Nil, fmt.Errorf("Failed to write plan to storage: %w", err)
	}

	return plan.Internal.ID, nil
}

func (w *Workstream) Start(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (w *Workstream) now() time.Time {
	return time.Now().UTC()
}
