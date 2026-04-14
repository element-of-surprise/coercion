package cosmosdb

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

func (p reader) idToDeferredActions(ctx context.Context, planID azcosmos.PartitionKey, id uuid.UUID) (*workflow.DeferredActions, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	res, err := p.client.ReadItem(ctx, planID, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch DeferredActions by id: %w", err)
	}

	var resp deferredActionsEntry
	if err := json.Unmarshal(res.Value, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DeferredActions: %w", err)
	}

	da := &workflow.DeferredActions{
		ID: resp.ID,
	}
	da.State.Set(workflow.State{
		Status: resp.StateStatus,
		Start:  resp.StateStart,
		End:    resp.StateEnd,
		ETag:   string(resp.ETag),
	})
	da.SetPlanID(resp.PlanID)

	da.OnFailure, err = p.idsToDeferBatches(ctx, planID, resp.OnFailure)
	if err != nil {
		return nil, fmt.Errorf("idToDeferredActions(onFailure): %w", err)
	}
	da.OnSuccess, err = p.idsToDeferBatches(ctx, planID, resp.OnSuccess)
	if err != nil {
		return nil, fmt.Errorf("idToDeferredActions(onSuccess): %w", err)
	}
	return da, nil
}

func (p reader) idsToDeferBatches(ctx context.Context, planID azcosmos.PartitionKey, ids []uuid.UUID) ([]*workflow.DeferBatch, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]*workflow.DeferBatch, 0, len(ids))
	for _, id := range ids {
		b, err := p.fetchDeferBatchByID(ctx, planID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (p reader) fetchDeferBatchByID(ctx context.Context, planID azcosmos.PartitionKey, id uuid.UUID) (*workflow.DeferBatch, error) {
	res, err := p.client.ReadItem(ctx, planID, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch DeferBatch by id: %w", err)
	}

	var resp deferBatchesEntry
	if err := json.Unmarshal(res.Value, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DeferBatch: %w", err)
	}

	b := &workflow.DeferBatch{
		FailElement: resp.FailElement,
	}
	b.ID = resp.ID
	b.Key = resp.Key
	b.Name = resp.Name
	b.Descr = resp.Descr
	b.State.Set(workflow.State{
		Status: resp.StateStatus,
		Start:  resp.StateStart,
		End:    resp.StateEnd,
		ETag:   string(resp.ETag),
	})
	b.SetPlanID(resp.PlanID)

	b.Actions, err = p.idsToActions(ctx, planID, resp.Actions)
	if err != nil {
		return nil, fmt.Errorf("fetchDeferBatchByID(actions): %w", err)
	}
	return b, nil
}
