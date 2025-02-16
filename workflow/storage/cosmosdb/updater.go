package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/gostdlib/base/retry/exponential"
)

var _ storage.Updater = updater{}

// patchItemer provides patching methods for storage entries. Implemented by azcosmos.ContainerClient.
type patchItemer interface {
	PatchItem(context.Context, azcosmos.PartitionKey, string, azcosmos.PatchOperations, *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
}

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

func newUpdater(mu *sync.RWMutex, client patchItemer, pk azcosmos.PartitionKey, defaultIO *azcosmos.ItemOptions) updater {
	uo := updater{}

	uo.planUpdater = planUpdater{
		mu:        mu,
		client:    client,
		pk:        pk,
		defaultIO: defaultIO,
	}
	uo.checksUpdater = checksUpdater{
		mu:        mu,
		client:    client,
		pk:        pk,
		defaultIO: defaultIO,
	}
	uo.blockUpdater = blockUpdater{
		mu:        mu,
		client:    client,
		pk:        pk,
		defaultIO: defaultIO,
	}
	uo.sequenceUpdater = sequenceUpdater{
		mu:        mu,
		client:    client,
		pk:        pk,
		defaultIO: defaultIO,
	}
	uo.actionUpdater = actionUpdater{
		mu:        mu,
		client:    client,
		pk:        pk,
		defaultIO: defaultIO,
	}
	return uo
}

// UpdateObject provides a way to update any of the workflow.Object types instead of having to do the type detection yourself.
func (u updater) UpdateObject(ctx context.Context, o workflow.Object) error {
	switch o.Type() {
	case workflow.OTPlan:
		return u.UpdatePlan(ctx, o.(*workflow.Plan))
	case workflow.OTBlock:
		return u.UpdateBlock(ctx, o.(*workflow.Block))
	case workflow.OTCheck:
		return u.UpdateChecks(ctx, o.(*workflow.Checks))
	case workflow.OTAction:
		return u.UpdateAction(ctx, o.(*workflow.Action))
	case workflow.OTSequence:
		return u.UpdateSequence(ctx, o.(*workflow.Sequence))
	}
	// If UpdateObject is passed a bad object, the whole program is screwed.
	panic(fmt.Sprintf("bug: cannot update object type %T", o))
}

type patchItem func(ctx context.Context, cc patchItemer, pk azcosmos.PartitionKey, id string, patch azcosmos.PatchOperations, itemOpt *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)

func patchItemWithRetry(ctx context.Context, cc patchItemer, pk azcosmos.PartitionKey, id string, patch azcosmos.PatchOperations, itemOpt *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
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
