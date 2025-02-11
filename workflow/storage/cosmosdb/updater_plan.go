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

var _ storage.PlanUpdater = planUpdater{}

// planUpdater implements the storage.PlanUpdater interface.
type planUpdater struct {
	mu *sync.RWMutex
	client

	private.Storage
}

// UpdatePlan implements storage.PlanUpdater.UpdatePlan().
func (u planUpdater) UpdatePlan(ctx context.Context, p *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/reason", p.Reason)
	patch.AppendReplace("/stateStatus", p.State.Status)
	patch.AppendReplace("/stateStart", p.State.Start)
	patch.AppendReplace("/stateEnd", p.State.End)

	itemOpt := u.itemOptions()
	var ifMatchEtag *azcore.ETag = nil
	if p.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&p.State.ETag)
	}
	itemOpt.IfMatchEtag = ifMatchEtag

	resp, err := patchItemWithRetry(ctx, u.getUpdater(), u.getPK(), p.ID.String(), patch, itemOpt)
	if err != nil {
		return err
	}

	p.State.ETag = string(resp.ETag)

	return nil
}
