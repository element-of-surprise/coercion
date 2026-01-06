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

var _ storage.BlockUpdater = blockUpdater{}

// blockUpdater implements the storage.blockUpdater interface.
type blockUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateBlock implements storage.Blockupdater.UpdateBlock().
func (u blockUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", block.State.Get().Status)
	patch.AppendReplace("/stateStart", block.State.Get().Start)
	patch.AppendReplace("/stateEnd", block.State.Get().End)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if block.State.Get().ETag != "" {
		etag := block.State.Get().ETag
		ifMatchEtag = (*azcore.ETag)(&etag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(block.GetPlanID())

	resp, err := patchItemWithRetry(ctx, u.client, k, block.ID.String(), patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	state := block.State.Get()
	state.ETag = string(resp.ETag)
	block.State.Set(state)

	return nil
}
