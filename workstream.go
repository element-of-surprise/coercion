package workstream

import (
	"context"
	"fmt"

	"github.com/element-of-surprise/workstream/workflow"
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

// Submit submits a plan to the workstream for execution. It returns
func (w *Workstream) Submit(ctx context.Context, plan *workflow.Plan) (uuid.UUID, error) {
	if err := workflow.Validate(plan); err != nil {
		return uuid.UUID{}, fmt.Errorf("Plan did not validate: %s", err)
	}

	return nil
}

func (w *Workstream) Start(ctx context.Context, id uuid.UUID) error {
	return nil
}
