package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/gostdlib/base/retry/exponential"
)

var _ storage.Updater = updater{}

// updater implements the storage.updater interface.
type updater struct {
	planUpdater
	checksUpdater
	blockUpdater
	sequenceUpdater
	actionUpdater

	reader reader
	private.Storage
}

func newUpdater(mu *sync.RWMutex, client client, r reader) updater {
	return updater{
		planUpdater:     planUpdater{mu: mu, client: client},
		checksUpdater:   checksUpdater{mu: mu, client: client},
		blockUpdater:    blockUpdater{mu: mu, client: client},
		sequenceUpdater: sequenceUpdater{mu: mu, client: client},
		actionUpdater:   actionUpdater{mu: mu, client: client, reader: r},
	}
}

func patchItemWithRetry(ctx context.Context, cc containerUpdater, pk azcosmos.PartitionKey, id string, patch azcosmos.PatchOperations, itemOpt *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	var resp azcosmos.ItemResponse
	var err error
	patchItem := func(ctx context.Context, r exponential.Record) error {
		resp, err = cc.PatchItem(ctx, pk, id, patch, itemOpt)
		if err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), patchItem); err != nil {
		return azcosmos.ItemResponse{}, fmt.Errorf("failed to patch item through Cosmos DB API: %w", err)
	}
	return resp, nil
}
