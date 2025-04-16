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

var _ storage.ChecksUpdater = checksUpdater{}

// checksUpdater implements the storage.checksUpdater interface.
type checksUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateChecks implements storage.ChecksUpdater.UpdateCheck().
func (u checksUpdater) UpdateChecks(ctx context.Context, check *workflow.Checks) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", check.State.Status)
	patch.AppendReplace("/stateStart", check.State.Start)
	patch.AppendReplace("/stateEnd", check.State.End)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if check.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&check.State.ETag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(check.GetPlanID())

	resp, err := patchItemWithRetry(ctx, u.client, k, check.ID.String(), patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	check.State.ETag = string(resp.ETag)

	return nil
}
