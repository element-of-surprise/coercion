package cosmosdb

import (
	"fmt"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// idToCheck reads a field from the CosmosDB docuemnt and returns a *workflow.Checks  object.
// The document must be from a Plan or Block query.
func (p reader) idToCheck(ctx context.Context, planID azcosmos.PartitionKey, id uuid.UUID) (*workflow.Checks, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	return p.fetchChecksByID(ctx, planID, id)
}

// fetchChecksByID fetches a Checks object by its ID.
func (p reader) fetchChecksByID(ctx context.Context, planID azcosmos.PartitionKey, id uuid.UUID) (*workflow.Checks, error) {
	res, err := p.client.ReadItem(ctx, planID, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch checks by id: %w", err)
	}
	check, err := p.docToChecks(ctx, planID, &res)
	if err != nil {
		return nil, err
	}
	if check == nil {
		return nil, fmt.Errorf("couldn't find checks by id(%s)", id)
	}
	return check, nil
}

func (p reader) docToChecks(ctx context.Context, planID azcosmos.PartitionKey, response *azcosmos.ItemResponse) (*workflow.Checks, error) {
	var err error
	var resp checksEntry
	if err = json.Unmarshal(response.Value, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal check: %w", err)
	}

	c := &workflow.Checks{
		ID:    resp.ID,
		Key:   resp.Key,
		Delay: time.Duration(resp.DelayISO8601),
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	c.SetPlanID(resp.PlanID)

	c.Actions, err = p.idsToActions(ctx, planID, resp.Actions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get actions ids: %w", err)
	}

	return c, nil
}
