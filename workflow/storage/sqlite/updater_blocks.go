package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite/sqlitex"
)

var _ storage.BlockUpdater = blockUpdater{}

// blockUpdater implements the storage.blockUpdater interface.
type blockUpdater struct {
	mu   *sync.Mutex
	pool *sqlitex.Pool

	private.Storage
}

// UpdateBlock implements storage.Blockupdater.UpdateBlock().
func (b blockUpdater) UpdateBlock(ctx context.Context, action *workflow.Block) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	conn, err := b.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("couldn't get a connection from the pool: %w", err)
	}
	defer b.pool.Put(conn)

	stmt, err := conn.Prepare(updateBlock)
	if err != nil {
		return fmt.Errorf("BlockWriter.Write: %w", err)
	}

	stmt.SetText("$id", action.ID.String())
	stmt.SetInt64("$state_status", int64(action.State.Status))
	stmt.SetInt64("$state_start", action.State.Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("BlockWriter.Write: %w", err)
	}

	return nil
}
