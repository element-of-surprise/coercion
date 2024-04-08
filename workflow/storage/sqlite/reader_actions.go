package sqlite

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fieldToActions converts the "actions" field in a sqlite row to a list of workflow.Actions.
func (p *planReader) fieldToActions(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) ([]*workflow.Action, error) {
	ids, err := fieldToIDs("actions", stmt)
	if err != nil {
		return nil, fmt.Errorf("couldn't read action ids: %w", err)
	}

	actions, err := p.fetchActionsByIDs(ctx, conn, ids)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

// fetchActionsByIDs fetches a list of actions by their IDs.
func (p *planReader) fetchActionsByIDs(ctx context.Context, conn *sqlite.Conn, ids []uuid.UUID) ([]*workflow.Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	actions := make([]*workflow.Action, 0, len(ids))

	do := func(conn *sqlite.Conn) (err error) {
		param := idSearchFromUUID(ids)

		err = sqlitex.Execute(
			conn,
			fetchActionsByID,
			&sqlitex.ExecOptions{
				Named: map[string]any{
					"$ids": param,
				},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					a, err := p.actionRowToAction(ctx, stmt)
					if err != nil {
						return fmt.Errorf("couldn't convert row to action: %w", err)
					}
					actions = append(actions, a)
					return nil
				},
			},
		)
		if err != nil {
			return fmt.Errorf("couldn't fetch actions by ids: %w", err)
		}
		return nil
	}

	if err := do(conn); err != nil {
		return nil, fmt.Errorf("couldn't fetch actions by ids: %w", err)
	}
	return actions, nil
}

var emptyAttemptsJSON = []byte(`[]`)

// actionRowToAction converts a sqlite row to a workflow.Action.
func (p *planReader) actionRowToAction(ctx context.Context, stmt *sqlite.Stmt) (*workflow.Action, error) {
	var err error
	a := &workflow.Action{}
	a.ID, err = uuid.Parse(stmt.GetText("id"))
	if err != nil {
		return nil, fmt.Errorf("couldn't parse action id: %w", err)
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

	plug := p.reg.Plugin(a.Plugin)
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

// idSearchFromUUID returns a byte slice that can be used in a query to search for the given UUIDs.
// The returned byte slice is a comma separated list of UUIDs
func idSearchFromUUID(ids []uuid.UUID) string {
	if len(ids) == 0 {
		return ""
	}

	build := strings.Builder{}
	for i, id := range ids {
		build.WriteString(id.String())
		if i < len(ids)-1 {
			build.WriteString(",")
		}
	}
	return build.String()
}
