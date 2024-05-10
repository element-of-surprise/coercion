package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite"
)

var _ storage.ActionUpdater = actionUpdater{}

// actionUpdater implements the storage.actionUpdater interface.
type actionUpdater struct {
	mu   *sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// UpdateAction implements storage.ActionUpdater.UpdateAction().
func (a actionUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	stmt, err := a.conn.Prepare(updateAction)
	if err != nil {
		return fmt.Errorf("ActionWriter.Write: %w", err)
	}

	stmt.SetText("$id", action.ID.String())
	stmt.SetInt64("$state_status", int64(action.State.Status))
	stmt.SetInt64("$state_start", action.State.Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.End.UnixNano())

	b, err := encodeAttempts(action.Attempts)
	if err != nil {
		return fmt.Errorf("ActionWriter.Write: %w", err)
	}
	stmt.SetBytes("$attempts", b)

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("ActionWriter.Write: %w", err)
	}

	return nil
}
