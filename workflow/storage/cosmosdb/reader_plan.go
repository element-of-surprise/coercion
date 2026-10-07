package cosmosdb

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// fetchPlan fetches a plan by its id.
func (p reader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	k := key(id)
	res, err := p.client.ReadItem(ctx, k, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch plan: %w", err)
	}
	plan, err := p.docToPlan(ctx, &res)
	if err != nil && (isNotFound(err) || errors.IsNotFound(err)) {
		// The plan document exists, so the plan does; a missing sub-document means storage is damaged, not that the plan
		// is gone. %v, not %w, so callers do not see a not-found error and give up on a plan that still exists.
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageInconsistent, fmt.Errorf("plan(%s) is missing a sub-document: %v", id, err))
	}
	return plan, err
}

func (p reader) docToPlan(ctx context.Context, response *azcosmos.ItemResponse) (*workflow.Plan, error) {
	var err error
	var resp plansEntry
	if err = json.Unmarshal(response.Value, &resp); err != nil {
		return nil, err
	}

	plan := &workflow.Plan{
		ID:         resp.PlanID,
		GroupID:    resp.GroupID,
		Name:       resp.Name,
		Descr:      resp.Descr,
		SubmitTime: resp.SubmitTime,
		Reason:     resp.Reason,
	}
	plan.State.Set(workflow.State{
		Status: resp.StateStatus,
		Start:  resp.StateStart,
		End:    resp.StateEnd,
		ETag:   string(resp.ETag),
	})
	k := key(resp.PlanID)
	plan.BypassChecks, err = p.idToCheck(ctx, k, resp.BypassChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan bypasschecks: %w", err)
	}
	plan.PreChecks, err = p.idToCheck(ctx, k, resp.PreChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan prechecks: %w", err)
	}
	plan.ContChecks, err = p.idToCheck(ctx, k, resp.ContChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan contchecks: %w", err)
	}
	plan.PostChecks, err = p.idToCheck(ctx, k, resp.PostChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan postchecks: %w", err)
	}
	plan.DeferredChecks, err = p.idToCheck(ctx, k, resp.DeferredChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan deferredchecks: %w", err)
	}
	plan.DeferredActions, err = p.idToDeferredActions(ctx, k, resp.DeferredActions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get plan deferredactions: %w", err)
	}
	plan.Blocks, err = p.idsToBlocks(ctx, k, resp.Blocks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get blocks: %w", err)
	}
	return plan, nil
}
