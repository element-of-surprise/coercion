package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
)

// creatorClient provides a method to execute a batch of transactions.
// Implemented by *azcosmos.ContainerClient.
type creatorClient interface {
	NewTransactionalBatch(partitionKey azcosmos.PartitionKey) azcosmos.TransactionalBatch
	ExecuteTransactionalBatch(ctx context.Context, b azcosmos.TransactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error)
}

type creatorReader interface {
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
}

// creator implements the storage.creator interface.
type creator struct {
	mu     *sync.RWMutex
	client creatorClient
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

	return c.commitPlan(ctx, plan)
}
