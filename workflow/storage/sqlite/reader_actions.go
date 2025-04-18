package sqlite

import (
	"fmt"
	"reflect"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fieldToActions converts the "actions" field in a sqlite row to a list of workflow.Actions.
func (r reader) fieldToActions(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) ([]*workflow.Action, error) {
	ids, err := fieldToIDs("actions", stmt)
	if err != nil {
		return nil, fmt.Errorf("couldn't read action ids: %w", err)
	}

	actions, err := r.fetchActionsByIDs(ctx, conn, ids)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

// fetchActionsByIDs fetches a list of actions by their IDs.
func (r reader) fetchActionsByIDs(ctx context.Context, conn *sqlite.Conn, ids []uuid.UUID) ([]*workflow.Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	actions := make([]*workflow.Action, 0, len(ids))

	query, args := replaceWithIDs(fetchActionsByID, "$ids", ids)

	err := sqlitex.Execute(
		conn,
		query,
		&sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				a, err := r.actionRowToAction(ctx, stmt)
				if err != nil {
					return fmt.Errorf("couldn't convert row to action: %w", err)
				}
				actions = append(actions, a)
				return nil
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

var emptyAttemptsJSON = []byte(`[]`)

// actionRowToAction converts a sqlite row to a workflow.Action.
func (r reader) actionRowToAction(ctx context.Context, stmt *sqlite.Stmt) (*workflow.Action, error) {
	var err error
	a := &workflow.Action{}

	a.ID, err = uuid.Parse(stmt.GetText("id"))
	if err != nil {
		return nil, fmt.Errorf("couldn't parse action id: %w", err)
	}

	planID, err := uuid.Parse(stmt.GetText("plan_id"))
	if err != nil {
		return nil, fmt.Errorf("couldn't parse action id: %w", err)
	}
	a.SetPlanID(planID)

	k := stmt.GetText("key")
	if k != "" {
		a.Key, err = uuid.Parse(k)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse action key: %w", err)
		}
	}
	a.Name = stmt.GetText("name")
	a.Descr = stmt.GetText("descr")
	a.Plugin = stmt.GetText("plugin")
	a.Timeout = time.Duration(stmt.GetInt64("timeout"))
	a.Retries = int(stmt.GetInt64("retries"))
	a.State, err = fieldToState(stmt)
	if err != nil {
		return nil, fmt.Errorf("actionRowToAction: %w", err)
	}

	plug := r.reg.Plugin(a.Plugin)
	if plug == nil {
		return nil, fmt.Errorf("couldn't find plugin %s", a.Plugin)
	}

	b := fieldToBytes("req", stmt)
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
	b = fieldToBytes("attempts", stmt)
	if len(b) > 0 {
		a.Attempts, err = decodeAttempts(b, plug)
		if err != nil {
			return nil, fmt.Errorf("couldn't decode attempts: %w", err)
		}
	}
	return a, nil
}
