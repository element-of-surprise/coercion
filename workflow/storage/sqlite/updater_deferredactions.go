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

var (
	_ storage.DeferredActionsUpdater = deferredActionsUpdater{}
	_ storage.DeferBatchUpdater      = deferBatchUpdater{}
)

// deferredActionsUpdater implements storage.DeferredActionsUpdater.
type deferredActionsUpdater struct {
	mu      *sync.Mutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateDeferredActions implements storage.DeferredActionsUpdater.UpdateDeferredActions.
func (u deferredActionsUpdater) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	conn, err := u.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer u.pool.Put(conn)

	stmt := Stmt{}
	stmt.Query(updateDeferredActions)
	stmt.SetText("$id", da.ID.String())
	stmt.SetInt64("$state_status", int64(da.State.Get().Status))
	stmt.SetInt64("$state_start", da.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", da.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return err
	}
	if _, err := sStmt.Step(); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("DeferredActionsUpdater.UpdateDeferredActions: %w", err))
	}
	u.capture.Capture(stmt)
	return nil
}

// deferBatchUpdater implements storage.DeferBatchUpdater.
type deferBatchUpdater struct {
	mu      *sync.Mutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateDeferBatch implements storage.DeferBatchUpdater.UpdateDeferBatch.
func (u deferBatchUpdater) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	conn, err := u.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer u.pool.Put(conn)

	stmt := Stmt{}
	stmt.Query(updateDeferBatch)
	stmt.SetText("$id", b.ID.String())
	stmt.SetInt64("$state_status", int64(b.State.Get().Status))
	stmt.SetInt64("$state_start", b.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", b.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return err
	}
	if _, err := sStmt.Step(); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("DeferBatchUpdater.UpdateDeferBatch: %w", err))
	}
	u.capture.Capture(stmt)
	return nil
}
