package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fieldToCheck reads a field from the statement and returns a workflow.Checks  object. stmt must be
// from a Plan or Block query.
func (p *planReader) fieldToCheck(ctx context.Context, field string, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.Checks, error) {
	strID := stmt.GetText(field)
	if strID == "" {
		return nil, nil
	}
	id, err := uuid.FromBytes(strToBytes(strID))
	if err != nil {
		return nil, fmt.Errorf("couldn't convert ID to UUID: %w", err)
	}
	return p.fetchChecksByID(ctx, conn, id)
}

// fetchChecksByID fetches a Checks object by its ID.
func (p *planReader) fetchChecksByID(ctx context.Context, conn *sqlite.Conn, id uuid.UUID) (*workflow.Checks, error) {
	var check *workflow.Checks
	do := func(conn *sqlite.Conn) (err error) {
		err = sqlitex.Execute(
			conn,
			fetchChecksByID,
			&sqlitex.ExecOptions{
				Named: map[string]any{
					"$ids": id.String(),
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
func (p *planReader) checksRowToChecks(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.Checks, error) {
	c := &workflow.Checks{}
	c.ID = uuid.UUID(fieldToBytes("$id", stmt)[:16])
	c.Delay = time.Duration(stmt.GetInt64("$delay"))
	c.State = &workflow.State{
		Status: workflow.Status(stmt.GetInt64("$state_status")),
		Start:  time.Unix(0, stmt.GetInt64("$state_start")),
		End:    time.Unix(0, stmt.GetInt64("$state_end")),
	}
	var err error
	c.Actions, err = p.fieldToActions(ctx, conn, stmt)
	if err != nil {
		return nil, fmt.Errorf("couldn't get actions ids: %w", err)
	}

	return c, nil
}
