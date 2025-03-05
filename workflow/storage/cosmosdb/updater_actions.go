package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.ActionUpdater = actionUpdater{}

// actionUpdater implements the storage.actionUpdater interface.
type actionUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateAction implements storage.ActionUpdater.UpdateAction().
func (u actionUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", action.State.Status)
	patch.AppendReplace("/stateStart", action.State.Start)
	patch.AppendReplace("/stateEnd", action.State.End)
	attempts, err := encodeAttempts(action.Attempts)
	if err != nil {
		return fmt.Errorf("can't encode action.Attempts: %w", err)
	}
	patch.AppendSet("/attempts", attempts)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if action.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&action.State.ETag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(action.GetPlanID())

	resp, err := patchItemWithRetry(ctx, u.client, k, action.ID.String(), patch, itemOpt)
	if err != nil {
		return err
	}

	action.State.ETag = string(resp.ETag)

	return nil
}
