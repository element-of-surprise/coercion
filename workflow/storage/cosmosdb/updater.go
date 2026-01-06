package cosmosdb

import (
	"fmt"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/go-json-experiment/json"

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

func newUpdater(mu *sync.RWMutex, client planPatcher, defaultIOpts *azcosmos.ItemOptions) updater {
	uo := updater{}

	uo.planUpdater = planUpdater{
		mu:           mu,
		client:       client,
		defaultIOpts: defaultIOpts,
	}
	uo.checksUpdater = checksUpdater{
		mu:           mu,
		client:       client,
		defaultIOpts: defaultIOpts,
	}
	uo.blockUpdater = blockUpdater{
		mu:           mu,
		client:       client,
		defaultIOpts: defaultIOpts,
	}
	uo.sequenceUpdater = sequenceUpdater{
		mu:           mu,
		client:       client,
		defaultIOpts: defaultIOpts,
	}
	uo.actionUpdater = actionUpdater{
		mu:           mu,
		client:       client,
		defaultIOpts: defaultIOpts,
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

// patchPlan patches the plan in the database and updates the search index.
func patchPlan(ctx context.Context, client planPatcher, plan *workflow.Plan, patch azcosmos.PatchOperations, itemOpt *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	resp, err := patchItemWithRetry(ctx, client, key(plan), plan.GetID().String(), patch, itemOpt)
	if err != nil {
		return azcosmos.ItemResponse{}, fmt.Errorf("failed to patch plan through Cosmos DB API: %w", err)
	}
	_, err = replaceSearch(ctx, client, plan)
	return resp, err
}

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

// replace search replaces the search entry for the plan.
func replaceSearch(ctx context.Context, client creatorClient, plan *workflow.Plan) (azcosmos.TransactionalBatchResponse, error) {
	var resp azcosmos.TransactionalBatchResponse
	var err error

	se := searchEntry{
		PartitionKey: searchKeyStr,
		Name:         plan.Name,
		Descr:        plan.Descr,
		ID:           plan.ID,
		GroupID:      plan.GroupID,
		SubmitTime:   plan.SubmitTime,
		StateStatus:  plan.State.Get().Status,
		StateStart:   plan.State.Get().Start,
		StateEnd:     plan.State.Get().End,
	}
	b, err := json.Marshal(se)
	if err != nil {
		return azcosmos.TransactionalBatchResponse{}, fmt.Errorf("failed to marshal search record: %w", err)
	}

	searchBatch := client.NewTransactionalBatch(searchKey)
	searchBatch.ReplaceItem(plan.GetID().String(), b, emptyItemOptions)

	replaceSearch := func(ctx context.Context, r exponential.Record) error {
		resp, err = client.ExecuteTransactionalBatch(ctx, searchBatch, emptyBatchOptions)
		if err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), replaceSearch); err != nil {
		return azcosmos.TransactionalBatchResponse{}, fmt.Errorf("failed to patch item through Cosmos DB API: %w", err)
	}
	return resp, nil
}
