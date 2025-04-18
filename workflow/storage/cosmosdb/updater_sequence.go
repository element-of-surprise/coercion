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

var _ storage.SequenceUpdater = sequenceUpdater{}

// sequenceUpdater implements the storage.sequenceUpdater interface.
type sequenceUpdater struct {
	mu     *sync.RWMutex
	client patchItemer

	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdateSequence implements storage.SequenceUpdater.UpdateSequence().
func (u sequenceUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", seq.State.Status)
	patch.AppendReplace("/stateStart", seq.State.Start)
	patch.AppendReplace("/stateEnd", seq.State.End)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if seq.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&seq.State.ETag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	k := key(seq.GetPlanID())
	resp, err := patchItemWithRetry(ctx, u.client, k, seq.ID.String(), patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	seq.State.ETag = string(resp.ETag)

	return nil
}
