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

var _ storage.ChecksWriter = ChecksWriter{}

// ChecksWriter implements the storage.ChecksWriter interface.
type ChecksWriter struct {
	mu   *sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// Write implements storage.ChecksWriter.Checks().
func (c ChecksWriter) Write(ctx context.Context, check *workflow.Checks) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	stmt, err := c.conn.Prepare(updateChecks)
	if err != nil {
		return fmt.Errorf("ChecksWriter.Checks: %w", err)
	}

	stmt.SetText("$id", check.ID.String())
	stmt.SetInt64("$state_status", int64(check.State.Status))
	stmt.SetInt64("$state_start", check.State.Start.UnixNano())
	stmt.SetInt64("$state_end", check.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("ChecksWriter.Checks: %w", err)
	}

	return nil

}
