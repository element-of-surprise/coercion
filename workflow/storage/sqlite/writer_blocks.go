package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"
	"zombiezen.com/go/sqlite"
)

var _ storage.BlockWriter = BlockWriter{}

// BlockWriter implements the storage.BlockWriter interface.
type BlockWriter struct {
	mu   *sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// Write implements storage.BlockWriter.Write().
func (b BlockWriter) Write(ctx context.Context, action *workflow.Block) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	stmt, err := b.conn.Prepare(updateBlock)
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
