package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite/sqlitex"
)

var _ storage.ActionUpdater = actionUpdater{}

// actionUpdater implements the storage.actionUpdater interface.
type actionUpdater struct {
	mu      *sync.Mutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateAction implements storage.ActionUpdater.UpdateAction().
func (a actionUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	conn, err := a.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer a.pool.Put(conn)

	stmt := Stmt{}
	stmt.Query(updateAction)
	stmt.SetText("$id", action.ID.String())
	stmt.SetInt64("$state_status", int64(action.State.Status))
	stmt.SetInt64("$state_start", action.State.Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.End.UnixNano())

	b, err := encodeAttempts(action.Attempts)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("ActionWriter.Write: %w", err))
	}
	stmt.SetBytes("$attempts", b)

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return err
	}

	_, err = sStmt.Step()
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("ActionWriter.Write: %w", err))
	}
	a.capture.Capture(stmt)

	return nil
}
