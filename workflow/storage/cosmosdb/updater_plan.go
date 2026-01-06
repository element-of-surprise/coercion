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

var _ storage.PlanUpdater = planUpdater{}

type planPatcher interface {
	creatorClient
	patchItemer
}

// planUpdater implements the storage.PlanUpdater interface.
type planUpdater struct {
	mu           *sync.RWMutex
	client       planPatcher
	defaultIOpts *azcosmos.ItemOptions

	private.Storage
}

// UpdatePlan implements storage.PlanUpdater.UpdatePlan().
func (u planUpdater) UpdatePlan(ctx context.Context, p *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/reason", p.Reason)
	patch.AppendReplace("/stateStatus", p.State.Get().Status)
	patch.AppendReplace("/stateStart", p.State.Get().Start)
	patch.AppendReplace("/stateEnd", p.State.Get().End)
	patch.AppendReplace("/submitTime", p.SubmitTime)

	itemOpt := itemOptions(u.defaultIOpts)
	var ifMatchEtag *azcore.ETag = nil
	if p.State.Get().ETag != "" {
		etag := p.State.Get().ETag
		ifMatchEtag = (*azcore.ETag)(&etag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	resp, err := patchPlan(ctx, u.client, p, patch, itemOpt)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeStorageUpdate, err)
	}

	state := p.State.Get()
	state.ETag = string(resp.ETag)
	p.State.Set(state)

	return nil
}
