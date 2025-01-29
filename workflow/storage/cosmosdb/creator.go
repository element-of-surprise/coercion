package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"github.com/gostdlib/ops/retry/exponential"
)

// creator implements the storage.creator interface.
type creator struct {
	mu *sync.Mutex
	Client
	reader reader

	private.Storage
}

// Create writes Plan data to storage, and all underlying data.
func (u creator) Create(ctx context.Context, plan *workflow.Plan) error {
	if plan == nil {
		return fmt.Errorf("plan cannot be nil")
	}

	if plan.ID == uuid.Nil {
		return fmt.Errorf("plan ID cannot be nil")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	exist, err := u.reader.Exists(ctx, plan.ID)
	if err != nil {
		return err
	}

	if exist {
		return fmt.Errorf("plan with ID(%s) already exists", plan.ID)
	}

	commitPlan := func(ctx context.Context, r exponential.Record) error {
		if err = u.commitPlan(ctx, plan); err != nil {
			if !isRetriableError(err) || r.Attempt >= 5 {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), commitPlan); err != nil {
		return fmt.Errorf("failed to commit plan: %w", err)
	}
	return nil
}
