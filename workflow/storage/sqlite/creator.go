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
	mu     *sync.Mutex
	pool   *sqlitex.Pool
	reader reader

	private.Storage

	capture *CaptureStmts
}

// Create writes Plan data to storage, and all underlying data.
func (u creator) Create(ctx context.Context, plan *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if plan.ID == uuid.Nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan ID cannot be nil"))
	}

	exist, err := u.reader.Exists(ctx, plan.ID)
	if err != nil {
		return err
	}

	if exist {
		return fmt.Errorf("plan with ID(%s) already exists", plan.ID)
	}

	conn, err := u.pool.Take(ctx)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer u.pool.Put(conn)

	return commitPlan(ctx, conn, plan, u.capture)
}
