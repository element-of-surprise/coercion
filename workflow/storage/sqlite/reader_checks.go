package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fieldToCheck reads a field from the statement and returns a workflow.Checks  object. stmt must be
// from a Plan or Block query.
func (p reader) fieldToCheck(ctx context.Context, field string, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.Checks, error) {
	strID := stmt.GetText(field)
	if strID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(strID)
	if err != nil {
		return nil, fmt.Errorf("couldn't convert ID to UUID: %w", err)
	}
	return p.fetchChecksByID(ctx, conn, id)
}

// fetchChecksByID fetches a Checks object by its ID.
func (p reader) fetchChecksByID(ctx context.Context, conn *sqlite.Conn, id uuid.UUID) (*workflow.Checks, error) {
	var check *workflow.Checks
	do := func(conn *sqlite.Conn) (err error) {
		err = sqlitex.Execute(
			conn,
			fetchChecksByID,
			&sqlitex.ExecOptions{
				Named: map[string]any{
					"$id": id.String(),
				},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					c, err := p.checksRowToChecks(ctx, conn, stmt)
					if err != nil {
						return fmt.Errorf("couldn't convert row to checks: %w", err)
					}
					check = c
					return nil
				},
			},
		)
		if err != nil {
			return fmt.Errorf("couldn't fetch checks by id: %w", err)
		}
		return nil
	}
	if err := do(conn); err != nil {
		return nil, fmt.Errorf("couldn't fetch checks by ids: %w", err)
	}
	if check == nil {
		return nil, fmt.Errorf("couldn't find checks by id(%s)", id)
	}
	return check, nil
}

// checksRowToChecks converts a sqlite row to a workflow.Checks.
func (p reader) checksRowToChecks(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.Checks, error) {
	var err error
	c := &workflow.Checks{}
	c.ID, err = uuid.Parse(stmt.GetText("id"))
	if err != nil {
		return nil, fmt.Errorf("checksRowToChecks: couldn't convert ID to UUID: %w", err)
	}
	k := stmt.GetText("key")
	if k != "" {
		c.Key, err = uuid.Parse(k)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse check key: %w", err)
		}
	}
	c.Delay = time.Duration(stmt.GetInt64("delay"))
	c.State, err = fieldToState(stmt)
	if err != nil {
		return nil, fmt.Errorf("checksRowToChecks: %w", err)
	}
	c.Actions, err = p.fieldToActions(ctx, conn, stmt)
	if err != nil {
		return nil, fmt.Errorf("couldn't get actions ids: %w", err)
	}

	return c, nil
}
