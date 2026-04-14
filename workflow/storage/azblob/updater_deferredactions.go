package azblob

import (
	"github.com/go-json-experiment/json"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

var (
	_ storage.DeferredActionsUpdater = deferredActionsUpdater{}
	_ storage.DeferBatchUpdater      = deferBatchUpdater{}
)

// deferredActionsUpdater implements storage.DeferredActionsUpdater.
type deferredActionsUpdater struct {
	mu       *planlocks.Group
	prefix   string
	client   blobops.Ops
	endpoint string

	private.Storage
}

// UpdateDeferredActions implements storage.DeferredActionsUpdater.UpdateDeferredActions.
func (u deferredActionsUpdater) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	u.mu.Lock(da.GetPlanID())
	defer u.mu.Unlock(da.GetPlanID())

	return blockUpdater{mu: u.mu, prefix: u.prefix, client: u.client, endpoint: u.endpoint}.
		updateObject(ctx, da, func(pos int) ([]byte, error) {
			entry, err := deferredActionsToEntry(da)
			if err != nil {
				return nil, err
			}
			return json.Marshal(entry)
		})
}

// deferBatchUpdater implements storage.DeferBatchUpdater.
type deferBatchUpdater struct {
	mu       *planlocks.Group
	prefix   string
	client   blobops.Ops
	endpoint string

	private.Storage
}

// UpdateDeferBatch implements storage.DeferBatchUpdater.UpdateDeferBatch.
func (u deferBatchUpdater) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	u.mu.Lock(b.GetPlanID())
	defer u.mu.Unlock(b.GetPlanID())

	return blockUpdater{mu: u.mu, prefix: u.prefix, client: u.client, endpoint: u.endpoint}.
		updateObject(ctx, b, func(pos int) ([]byte, error) {
			entry, err := deferBatchToEntry(b)
			if err != nil {
				return nil, err
			}
			return json.Marshal(entry)
		})
}
