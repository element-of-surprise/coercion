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
	a.id,
	a.key,
	a.planID,
	a.name,
	a.descr,
	a.plugin,
	a.timeout,
	a.retries,
	a.req,
	a.attempts,
	a.stateStatus,
	a.stateStart,
	a.stateEnd,
	a._etag
FROM actions a
WHERE a.type=5 AND ARRAY_CONTAINS(@ids, a.id)
ORDER BY a.pos ASC`

// idsToActions converts the "actions" field in a cosmosdb document to a list of workflow.Actions.
func (r reader) idsToActions(ctx context.Context, actionIDs []uuid.UUID) ([]*workflow.Action, error) {
	actions, err := r.fetchActionsByIDs(ctx, actionIDs)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

// fetchActionsByIDs fetches a list of actions by their IDs.
func (r reader) fetchActionsByIDs(ctx context.Context, ids []uuid.UUID) ([]*workflow.Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	actions := make([]*workflow.Action, 0, len(ids))
	parameters := []azcosmos.QueryParameter{
		{
			Name:  "@ids",
			Value: ids,
		},
	}

	pager := r.GetContainerClient().NewQueryItemsPager(fetchActionsByID, r.GetPK(), &azcosmos.QueryOptions{QueryParameters: parameters})
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

// docToAction converts a cosmosdb document to a workflow.Action.
func (r reader) docToAction(ctx context.Context, response []byte) (*workflow.Action, error) {
	var err error
	var resp actionsEntry
	err = json.Unmarshal(response, &resp)
	if err != nil {
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
