package cosmosdb

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// idToCheck reads a field from the statement and returns a workflow.Checks  object. stmt must be
// from a Plan or Block query.
func (p reader) idToCheck(ctx context.Context, id uuid.UUID) (*workflow.Checks, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	return p.fetchChecksByID(ctx, id)
}

// fetchChecksByID fetches a Checks object by its ID.
func (p reader) fetchChecksByID(ctx context.Context, id uuid.UUID) (*workflow.Checks, error) {
	res, err := p.GetContainerClient().ReadItem(ctx, p.GetPK(), id.String(), p.ItemOptions())
	if err != nil {
		// return p, fmt.Errorf("failed to read item through Cosmos DB API: %w", cosmosErr(err))
		return nil, fmt.Errorf("couldn't fetch checks by id: %w", err)
	}
	check, err := p.docToChecks(ctx, &res)
	if err != nil {
		return nil, err
	}
	if check == nil {
		return nil, fmt.Errorf("couldn't find checks by id(%s)", id)
	}
	return check, nil
}

func (p reader) docToChecks(ctx context.Context, response *azcosmos.ItemResponse) (*workflow.Checks, error) {
	var err error
	var resp checksEntry
	err = json.Unmarshal(response.Value, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal check: %w", err)
	}

	c := &workflow.Checks{
		ID:    resp.ID,
		Key:   resp.Key,
		Delay: time.Duration(resp.Delay),
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	c.Actions, err = p.idsToActions(ctx, resp.Actions)
	if err != nil {
		return nil, fmt.Errorf("couldn't get actions ids: %w", err)
	}

	return c, nil
}
