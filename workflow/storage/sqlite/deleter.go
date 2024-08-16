package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type deleter struct {
	mu   *sync.Mutex
	pool *sqlitex.Pool

	reader reader
}

// Delete deletes a plan with "id" from the storage.
func (d deleter) Delete(ctx context.Context, id uuid.UUID) error {
	plan, err := d.reader.Read(ctx, id)
	if err != nil {
		return fmt.Errorf("couldn't fetch plan: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	conn, err := d.pool.Take(ctx)
	if err != nil {
		return fmt.Errorf("couldn't get a connection from the pool: %w", err)
	}
	defer d.pool.Put(conn)

	defer sqlitex.Transaction(conn)(&err)

	if err = d.deletePlan(ctx, conn, plan); err != nil {
		return fmt.Errorf("couldn't delete plan: %w", err)
	}
	return nil
}

func (d deleter) deletePlan(ctx context.Context, conn *sqlite.Conn, plan *workflow.Plan) error {
	if err := d.deleteChecks(ctx, conn, plan.PreChecks); err != nil {
		return fmt.Errorf("couldn't delete plan prechecks: %w", err)
	}
	if err := d.deleteChecks(ctx, conn, plan.PostChecks); err != nil {
		return fmt.Errorf("couldn't delete plan postchecks: %w", err)
	}
	if err := d.deleteChecks(ctx, conn, plan.ContChecks); err != nil {
		return fmt.Errorf("couldn't delete plan contchecks: %w", err)
	}
	if err := d.deleteBlocks(ctx, conn, plan.Blocks); err != nil {
		return fmt.Errorf("couldn't delete blocks: %w", err)
	}

	stmt, err := conn.Prepare(deletePlanByID)
	if err != nil {
		return fmt.Errorf("couldn't prepare delete statement: %w", err)
	}

	stmt.SetText("$id", plan.ID.String())
	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("problem deleting plan: %w", err)
	}
	return nil
}

func (d deleter) deleteBlocks(ctx context.Context, conn *sqlite.Conn, blocks []*workflow.Block) error {
	if len(blocks) == 0 {
		return nil
	}

	for _, block := range blocks {
		if err := d.deleteChecks(ctx, conn, block.PreChecks); err != nil {
			return fmt.Errorf("couldn't delete block prechecks: %w", err)
		}
		if err := d.deleteChecks(ctx, conn, block.PostChecks); err != nil {
			return fmt.Errorf("couldn't delete block postchecks: %w", err)
		}
		if err := d.deleteChecks(ctx, conn, block.ContChecks); err != nil {
			return fmt.Errorf("couldn't delete block contchecks: %w", err)
		}
		if err := d.deletesSeqs(ctx, conn, block.Sequences); err != nil {
			return fmt.Errorf("couldn't delete block sequences: %w", err)
		}
	}

	for _, block := range blocks {
		stmt, err := conn.Prepare(delteBlocksByID)
		if err != nil {
			return fmt.Errorf("couldn't prepare delete statement: %w", err)
		}
		stmt.SetText("$id", block.ID.String())
		_, err = stmt.Step()
		if err != nil {
			return fmt.Errorf("problem deleting block: %w", err)
		}
	}
	return nil
}

func (d deleter) deleteChecks(ctx context.Context, conn *sqlite.Conn, checks *workflow.Checks) error {
	if checks == nil {
		return nil
	}

	if err := d.deleteActions(ctx, conn, checks.Actions); err != nil {
		return fmt.Errorf("couldn't delete checks actions: %w", err)
	}

	stmt, err := conn.Prepare(deleteChecksByID)
	if err != nil {
		return fmt.Errorf("couldn't prepare checks delete statement: %w", err)
	}
	stmt.SetText("$id", checks.ID.String())
	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("problem deleting check: %w", err)
	}
	return nil
}

func (d deleter) deletesSeqs(ctx context.Context, conn *sqlite.Conn, seqs []*workflow.Sequence) error {
	if len(seqs) == 0 {
		return nil
	}

	for _, seq := range seqs {
		if err := d.deleteActions(ctx, conn, seq.Actions); err != nil {
			return fmt.Errorf("couldn't delete sequence actions: %w", err)
		}
	}

	for _, seq := range seqs {
		stmt, err := conn.Prepare(deleteSequencesByID)
		if err != nil {
			return fmt.Errorf("couldn't prepare delete statement: %w", err)
		}
		stmt.SetText("$id", seq.ID.String())
		_, err = stmt.Step()
		if err != nil {
			return fmt.Errorf("problem deleting sequence: %w", err)
		}
	}
	return nil
}

func (d deleter) deleteActions(ctx context.Context, conn *sqlite.Conn, actions []*workflow.Action) error {
	if len(actions) == 0 {
		return nil
	}

	for _, action := range actions {
		stmt, err := conn.Prepare(deleteActionsByID)
		if err != nil {
			return fmt.Errorf("couldn't prepare delete statement: %w", err)
		}
		stmt.SetText("$id", action.ID.String())
		_, err = stmt.Step()
		if err != nil {
			return fmt.Errorf("problem deleting action: %w", err)
		}
	}
	return nil
}
