package cosmosdb

import (
	"context"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.BlockUpdater = blockUpdater{}

// blockUpdater implements the storage.blockUpdater interface.
type blockUpdater struct {
	mu *sync.RWMutex
	client

	private.Storage
}

// UpdateBlock implements storage.Blockupdater.UpdateBlock().
func (u blockUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", block.State.Status)
	patch.AppendReplace("/stateStart", block.State.Start)
	patch.AppendReplace("/stateEnd", block.State.End)

	itemOpt := u.itemOptions()
	var ifMatchEtag *azcore.ETag = nil
	if block.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&block.State.ETag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	resp, err := patchItemWithRetry(ctx, u.getUpdater(), u.getPK(), block.ID.String(), patch, itemOpt)
	if err != nil {
		return err
	}

	block.State.ETag = string(resp.ETag)

	return nil
}
