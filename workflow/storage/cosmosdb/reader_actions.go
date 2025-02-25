package cosmosdb

import (
	"context"
	"fmt"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

const fetchActionsByID = `
SELECT
	c.id,
	c.key,
	c.planID,
	c.name,
	c.descr,
	c.plugin,
	c.timeout,
	c.retries,
	c.req,
	c.attempts,
	c.stateStatus,
	c.stateStart,
	c.stateEnd,
	c._etag
FROM c
WHERE c.type=@objectType AND ARRAY_CONTAINS(@ids, c.id)
ORDER BY c.pos ASC`

// idsToActions converts the "actions" field in a cosmosdb document to a list of *workflow.Actions.
func (r reader) idsToActions(ctx context.Context, planID azcosmos.PartitionKey, actionIDs []uuid.UUID) ([]*workflow.Action, error) {
	actions, err := r.fetchActionsByIDs(ctx, planID, actionIDs)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

// fetchActionsByIDs fetches a list of actions by their IDs.
func (r reader) fetchActionsByIDs(ctx context.Context, planID azcosmos.PartitionKey, ids []uuid.UUID) ([]*workflow.Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	actions := make([]*workflow.Action, 0, len(ids))
	parameters := []azcosmos.QueryParameter{
		{
			Name:  "@ids",
			Value: ids,
		},
		{
			Name:  "@objectType",
			Value: int64(workflow.OTAction),
		},
	}

	pager := r.client.NewQueryItemsPager(fetchActionsByID, planID, &azcosmos.QueryOptions{QueryParameters: parameters})
	for pager.More() {
		res, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("problem listing actions: %w", err)
		}
		for _, item := range res.Items {
			action, err := r.docToAction(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("problem listing items in actions: %w", err)
			}
			actions = append(actions, action)
		}
	}

	return actions, nil
}

// docToAction converts a cosmosdb document to a *workflow.Action.
func (r reader) docToAction(ctx context.Context, response []byte) (*workflow.Action, error) {
	var err error
	var resp actionsEntry
	if err = json.Unmarshal(response, &resp); err != nil {
		return nil, err
	}

	a := &workflow.Action{
		ID:      resp.ID,
		Key:     resp.Key,
		Name:    resp.Name,
		Descr:   resp.Descr,
		Plugin:  resp.Plugin,
		Timeout: resp.Timeout,
		Retries: resp.Retries,
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	a.SetPlanID(resp.PlanID)

	plug := r.reg.Plugin(a.Plugin)
	if plug == nil {
		return nil, fmt.Errorf("couldn't find plugin %s", a.Plugin)
	}

	b := resp.Req
	if len(b) > 0 {
		req := plug.Request()
		if req != nil {
			if reflect.TypeOf(req).Kind() != reflect.Pointer {
				if err := json.Unmarshal(b, &req); err != nil {
					return nil, fmt.Errorf("couldn't unmarshal request: %w", err)
				}
			} else {
				if err := json.Unmarshal(b, req); err != nil {
					return nil, fmt.Errorf("couldn't unmarshal request: %w", err)
				}
			}
			a.Req = req
		}
	}
	b = resp.Attempts
	if len(b) > 0 {
		a.Attempts, err = decodeAttempts(b, plug)
		if err != nil {
			return nil, fmt.Errorf("couldn't decode attempts: %w", err)
		}
	}
	return a, nil
}
