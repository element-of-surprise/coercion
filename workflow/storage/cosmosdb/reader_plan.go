package cosmosdb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// fetchPlan fetches a plan by its id.
func (p reader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	res, err := p.getContainerClient().ReadItem(ctx, p.getPK(), id.String(), p.itemOptions())
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch plan: %w", err)
	}
	return p.docToPlan(ctx, &res)
}

func (p reader) docToPlan(ctx context.Context, response *azcosmos.ItemResponse) (*workflow.Plan, error) {
	var err error
	var resp plansEntry
	if err = json.Unmarshal(response.Value, &resp); err != nil {
		return nil, err
	}

	plan := &workflow.Plan{
		ID:         resp.ID,
		GroupID:    resp.GroupID,
		Name:       resp.Name,
		Descr:      resp.Descr,
		SubmitTime: resp.SubmitTime,
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	plan.BypassChecks, err = p.idToCheck(ctx, resp.BypassChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan bypasschecks: %w", err)
	}
	plan.PreChecks, err = p.idToCheck(ctx, resp.PreChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan prechecks: %w", err)
	}
	plan.ContChecks, err = p.idToCheck(ctx, resp.ContChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan contchecks: %w", err)
	}
	plan.PostChecks, err = p.idToCheck(ctx, resp.PostChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan postchecks: %w", err)
	}
	plan.DeferredChecks, err = p.idToCheck(ctx, resp.DeferredChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan deferredchecks: %w", err)
	}
	plan.Blocks, err = p.idsToBlocks(ctx, resp.Blocks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get blocks: %w", err)
	}
	return plan, nil
}
