package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/gostdlib/ops/retry/exponential"
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

func newUpdater(mu *sync.Mutex, client Client, r reader) updater {
	return updater{
		planUpdater:     planUpdater{mu: mu, Client: client},
		checksUpdater:   checksUpdater{mu: mu, Client: client},
		blockUpdater:    blockUpdater{mu: mu, Client: client},
		sequenceUpdater: sequenceUpdater{mu: mu, Client: client},
		actionUpdater:   actionUpdater{mu: mu, Client: client, reader: r},
	}
}

func patchItemWithRetry(ctx context.Context, cc ContainerClient, pk azcosmos.PartitionKey, id string, patch azcosmos.PatchOperations, itemOpt *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	var resp azcosmos.ItemResponse
	var err error
	patchItem := func(ctx context.Context, r exponential.Record) error {
		resp, err = cc.PatchItem(ctx, pk, id, patch, itemOpt)
		if err != nil {
			if !isRetriableError(err) || r.Attempt >= 5 {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(ctx, patchItem); err != nil {
		return azcosmos.ItemResponse{}, fmt.Errorf("failed to patch item through Cosmos DB API: %w", err)
	}
	return resp, nil
}
