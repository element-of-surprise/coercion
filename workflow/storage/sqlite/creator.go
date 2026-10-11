package sqlite

import (
	"fmt"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"zombiezen.com/go/sqlite/sqlitex"
)

// creator implements the storage.creator interface.
type creator struct {
	mu   *sync.RWMutex
	pool *sqlitex.Pool

	private.Storage

	capture *CaptureStmts
}

// Create writes Plan data to storage, and all underlying data. A write that fails on a lock conflict is retried.
func (u creator) Create(ctx context.Context, plan *workflow.Plan) error {
	if plan.ID == uuid.Nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan ID cannot be nil"))
	}
	// A started Create always finishes, like every other write, so the caller knows whether it took effect.
	ctx = context.WithoutCancel(ctx)
	return retryLocked(ctx, lockBackoff, func() error { return u.create(ctx, plan) })
}

// create is one attempt at Create.
func (u creator) create(ctx context.Context, plan *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// No separate existence check: commitPlan's insert of the Plan row fails on its primary key if the ID is stored,
	// which also covers another process storing it first.
	conn, err := u.pool.Take(ctx)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer u.pool.Put(conn)

	// Capture this attempt on its own, so a rolled back attempt leaves nothing captured.
	var attempt *CaptureStmts
	if u.capture != nil {
		attempt = &CaptureStmts{}
	}
	if err := commitPlan(ctx, conn, plan, attempt); err != nil {
		return err
	}
	u.capture.add(attempt)
	return nil
}
