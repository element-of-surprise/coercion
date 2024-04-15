package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/workstream/internal/private"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"

	"zombiezen.com/go/sqlite"
)

var _ storage.PlanUpdater = planUpdater{}

// planUpdater implements the storage.PlanUpdater interface.
type planUpdater struct {
	mu   *sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// UpdatePlan implements storage.PlanUpdater.UpdatePlan().
func (u planUpdater) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	stmt, err := u.conn.Prepare(updatePlan)
	if err != nil {
		return fmt.Errorf("PlanUpdater.UpdatePlan: %w", err)
	}

	stmt.SetText("$id", plan.ID.String())
	stmt.SetInt64("$reason", int64(plan.Reason))
	stmt.SetInt64("$state_status", int64(plan.State.Status))
	stmt.SetInt64("$state_start", plan.State.Start.UnixNano())
	stmt.SetInt64("$state_end", plan.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("PlanUpdater.UpdatePlan: %w", err)
	}

	return nil
}
