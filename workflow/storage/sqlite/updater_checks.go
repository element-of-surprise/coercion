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

var _ storage.ChecksUpdater = checksUpdater{}

// checksUpdater implements the storage.checksUpdater interface.
type checksUpdater struct {
	mu      *sync.Mutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateChecks implements storage.ChecksUpdater.UpdateCheck().
func (c checksUpdater) UpdateChecks(ctx context.Context, check *workflow.Checks) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := c.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("couldn't get a connection from the pool: %w", err)
	}
	defer c.pool.Put(conn)

	stmt := Stmt{}
	stmt.Query(updateChecks)
	stmt.SetText("$id", check.ID.String())
	stmt.SetInt64("$state_status", int64(check.State.Status))
	stmt.SetInt64("$state_start", check.State.Start.UnixNano())
	stmt.SetInt64("$state_end", check.State.End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("ChecksWriter.Checks: %w", err)
	}

	_, err = sStmt.Step()
	if err != nil {
		return fmt.Errorf("ChecksWriter.Checks: %w", err)
	}
	c.capture.Capture(stmt)

	return nil

}
