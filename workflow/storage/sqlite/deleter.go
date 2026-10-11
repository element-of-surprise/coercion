package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite/sqlitex"
)

type deleter struct {
	mu   *sync.RWMutex
	pool *sqlitex.Pool
}

// Delete deletes a plan with "id" from the storage. A delete that fails on a lock conflict is retried.
func (d deleter) Delete(ctx context.Context, id uuid.UUID) error {
	// A started Delete always finishes, like every other write, so the caller knows whether it took effect.
	ctx = context.WithoutCancel(ctx)
	return retryLocked(ctx, lockBackoff, func() error { return d.delete(ctx, id) })
}

// delete is one attempt at Delete. It deletes the plan's row and then every row below it by plan_id, in one
// transaction, so it never reads or decodes the plan. Rows below a plan whose own row is already gone are deleted too;
// only a plan with no rows at all is not found. err is named so the transaction's commit error is returned.
func (d deleter) delete(ctx context.Context, id uuid.UUID) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	conn, err := d.pool.Take(ctx)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer d.pool.Put(conn)

	defer sqlitex.Transaction(conn)(&err)

	if err := sqlitex.Execute(conn, deletePlanByID, &sqlitex.ExecOptions{Named: map[string]any{"$id": id.String()}}); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageDelete, fmt.Errorf("couldn't delete plan(%s): %w", id, err))
	}
	deleted := conn.Changes()
	for _, q := range deleteByPlanID {
		if err := sqlitex.Execute(conn, q, &sqlitex.ExecOptions{Named: map[string]any{"$plan_id": id.String()}}); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageDelete, fmt.Errorf("couldn't delete plan(%s): %w", id, err))
		}
		deleted += conn.Changes()
	}
	if deleted == 0 {
		return errors.ErrNotFound(ctx, fmt.Errorf("plan(%s)", id))
	}
	return nil
}
