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

var _ storage.BlockUpdater = blockUpdater{}

// blockUpdater implements the storage.blockUpdater interface.
type blockUpdater struct {
	mu      *sync.Mutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateBlock implements storage.Blockupdater.UpdateBlock().
func (b blockUpdater) UpdateBlock(ctx context.Context, action *workflow.Block) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	conn, err := b.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer b.pool.Put(conn)

	stmt := Stmt{}
	stmt.Query(updateBlock)
	stmt.SetText("$id", action.ID.String())
	stmt.SetInt64("$state_status", int64(action.State.Get().Status))
	stmt.SetInt64("$state_start", action.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return err
	}

	_, err = sStmt.Step()
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("BlockWriter.Write: %w", err))
	}
	b.capture.Capture(stmt)

	return nil
}
