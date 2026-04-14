package cosmosdb

import (
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var (
	_ storage.DeferredActionsUpdater = deferredActionsUpdater{}
	_ storage.DeferBatchUpdater      = deferBatchUpdater{}
)

// deferredActionsUpdater implements storage.DeferredActionsUpdater.
type deferredActionsUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateDeferredActions implements storage.DeferredActionsUpdater.UpdateDeferredActions.
func (u deferredActionsUpdater) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", da.State.Get().Status)
	patch.AppendReplace("/stateStart", da.State.Get().Start)
	patch.AppendReplace("/stateEnd", da.State.Get().End)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if da.State.Get().ETag != "" {
		etag := da.State.Get().ETag
		ifMatchEtag = (*azcore.ETag)(&etag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(da.GetPlanID())
	resp, err := patchItemWithRetry(ctx, u.client, k, da.ID.String(), patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	state := da.State.Get()
	state.ETag = string(resp.ETag)
	da.State.Set(state)
	return nil
}

// deferBatchUpdater implements storage.DeferBatchUpdater.
type deferBatchUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateDeferBatch implements storage.DeferBatchUpdater.UpdateDeferBatch.
func (u deferBatchUpdater) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", b.State.Get().Status)
	patch.AppendReplace("/stateStart", b.State.Get().Start)
	patch.AppendReplace("/stateEnd", b.State.Get().End)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if b.State.Get().ETag != "" {
		etag := b.State.Get().ETag
		ifMatchEtag = (*azcore.ETag)(&etag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(b.GetPlanID())
	resp, err := patchItemWithRetry(ctx, u.client, k, b.ID.String(), patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	state := b.State.Get()
	state.ETag = string(resp.ETag)
	b.State.Set(state)
	return nil
}
