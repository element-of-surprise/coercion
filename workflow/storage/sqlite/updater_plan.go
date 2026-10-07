package sqlite

import (
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

var _ storage.PlanUpdater = objectUpdater{}

// UpdatePlan implements storage.PlanUpdater.UpdatePlan(). It writes through writeItems like every other object, so a
// write that fails on a lock conflict is retried, and a Plan with no stored row is an errors.ErrNotFound.
func (u objectUpdater) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	return u.writeItems(ctx, []walk.Item{{Value: plan}})
}

// planStmt returns the statement that updates a Plan's reason and state.
func planStmt(plan *workflow.Plan) Stmt {
	stmt := stateStmt(updatePlan, plan.ID, plan.State.Get())
	stmt.SetInt64("$reason", int64(plan.Reason))
	return stmt
}
