package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/workstream/internal/private"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

// creator implements the storage.creator interface.
type creator struct {
	mu     *sync.Mutex
	conn   *sqlite.Conn
	reader reader

	private.Storage
}

// Create writes Plan data to storage, and all underlying data.
func (u creator) Create(ctx context.Context, plan *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if plan.ID == uuid.Nil {
		return fmt.Errorf("plan ID cannot be nil")
	}

	exist, err := u.reader.Exists(ctx, plan.ID)
	if err != nil {
		return err
	}

	if exist {
		return fmt.Errorf("plan with ID(%s) already exists", plan.ID)
	}

	return commitPlan(ctx, u.conn, plan)
}
