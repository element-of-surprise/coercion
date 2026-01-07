package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fetchPlan fetches a plan by its id.
func (p reader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan := &workflow.Plan{}

	conn, err := p.pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't get a connection from the pool: %w", err)
	}
	defer p.pool.Put(conn)

	found := false
	err = sqlitex.Execute(
		conn,
		fetchPlanByID,
		&sqlitex.ExecOptions{
			Named: map[string]any{
				"$id": id.String(),
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				var err error
				plan.ID, err = uuid.Parse(stmt.GetText("id"))
				if err != nil {
					return fmt.Errorf("couldn't convert ID to UUID: %w", err)
				}
				gid := stmt.GetText("group_id")
				if gid == "" {
					plan.GroupID = uuid.Nil
				} else {
					plan.GroupID, err = uuid.Parse(stmt.GetText("group_id"))
					if err != nil {
						return fmt.Errorf("couldn't convert GroupID to UUID: %w", err)
					}
				}
				plan.Name = stmt.GetText("name")
				plan.Descr = stmt.GetText("descr")
				plan.SubmitTime, err = timeFromField("submit_time", stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan submit time: %w", err)
				}
				plan.Reason = workflow.FailureReason(stmt.GetInt64("reason"))
				state, err := fieldToState(stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan state: %w", err)
				}
				plan.State.Set(*state)
				if b := fieldToBytes("meta", stmt); b != nil {
					plan.Meta = b
				}
				plan.BypassChecks, err = p.fieldToCheck(ctx, "bypasschecks", conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan bypasschecks: %w", err)
				}
				plan.PreChecks, err = p.fieldToCheck(ctx, "prechecks", conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan prechecks: %w", err)
				}
				plan.ContChecks, err = p.fieldToCheck(ctx, "contchecks", conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan contchecks: %w", err)
				}
				plan.PostChecks, err = p.fieldToCheck(ctx, "postchecks", conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan postchecks: %w", err)
				}
				plan.DeferredChecks, err = p.fieldToCheck(ctx, "deferredchecks", conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get plan deferredchecks: %w", err)
				}
				plan.Blocks, err = p.fieldToBlocks(ctx, conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't get blocks: %w", err)
				}
				return nil
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch plan: %w", err)
	}
	if !found {
		return nil, storage.ErrNotFound
	}
	return plan, nil
}
