package sqlite

import (
	"context"
	"fmt"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fetchPlan fetches a plan by its id.
func (p *planReader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan := &workflow.Plan{}

	do := func(conn *sqlite.Conn) (err error) {
		defer sqlitex.Transaction(conn)(&err)

		stmt, err := conn.Prepare(fetchPlanByID)
		if err != nil {
			return fmt.Errorf("couldn't prepare statement: %w", err)
		}
		stmt.SetBytes("$id", id[:])

		return sqlitex.Execute(
			conn,
			fetchPlanByID,
			&sqlitex.ExecOptions{
				Named: map[string]any{
					"$id": id[:],
				},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					stmt.GetBytes("$id", plan.ID[:])
					plan.Name = stmt.GetText("$name")
					plan.Descr = stmt.GetText("$descr")
					if b := fieldToBytes("$meta", stmt); b != nil {
						plan.Meta = b
					}

					plan.PreChecks, err = p.fieldToCheck(ctx, "$prechecks", conn, stmt)
					if err != nil {
						return fmt.Errorf("couldn't get plan prechecks: %w", err)
					}
					plan.ContChecks, err = p.fieldToCheck(ctx, "$contchecks", conn, stmt)
					if err != nil {
						return fmt.Errorf("couldn't get plan contchecks: %w", err)
					}
					plan.PostChecks, err = p.fieldToCheck(ctx, "$postchecks", conn, stmt)
					if err != nil {
						return fmt.Errorf("couldn't get plan postchecks: %w", err)
					}
					plan.Blocks, err = p.fieldToBlocks(ctx, conn, stmt)
					if err != nil {
						return fmt.Errorf("couldn't get blocks: %w", err)
					}
					return nil
				},
			},
		)
	}
	if err := do(p.conn); err != nil {
		return nil, fmt.Errorf("couldn't read plan: %w", err)
	}
	return plan, nil
}
