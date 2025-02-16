package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"
)

// executeTransactionalBatcher provides a method to execute a batch of transactions.
// Implemented by *azcosmos.ContainerClient.
type executeTransactionalBatcher interface {
	// ExecuteTransactionalBatch executes a transactional batch in the Azure Cosmos DB service.
	ExecuteTransactionalBatch(ctx context.Context, b azcosmos.TransactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error)
}

type creatorReader interface {
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
}

// creator implements the storage.creator interface.
type creator struct {
	mu     *sync.RWMutex
	client executeTransactionalBatcher
	pkStr  string
	pk     azcosmos.PartitionKey

	reader creatorReader

	private.Storage
}

// Create writes Plan data to storage, and all underlying data.
func (c creator) Create(ctx context.Context, plan *workflow.Plan) error {
	if plan == nil {
		return fmt.Errorf("plan cannot be nil")
	}

	if plan.ID == uuid.Nil {
		return fmt.Errorf("plan ID cannot be nil")
	}

	exist, err := c.reader.Exists(ctx, plan.ID)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if exist {
		return fmt.Errorf("plan with ID(%s) already exists", plan.ID)
	}

	commitPlan := func(ctx context.Context, r exponential.Record) error {
		if err = c.commitPlan(ctx, plan); err != nil {
			if !isRetriableError(err) {
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
