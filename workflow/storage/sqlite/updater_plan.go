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

// planStmt returns the statement that updates a Plan's reason, state and runtime update.
func planStmt(plan *workflow.Plan) Stmt {
	stmt := stateStmt(updatePlan, plan.ID, plan.State.Get())
	stmt.SetInt64("$reason", int64(plan.Reason))
	stmt.SetInt64("$runtime_update", runtimeUpdateNanos(plan))
	return stmt
}

// runtimeUpdateNanos returns plan's RuntimeUpdate as stored in the runtime_update column: 0 if it is not set, as
// timeFromField reads 0 back as the zero time.
func runtimeUpdateNanos(plan *workflow.Plan) int64 {
	t := plan.RuntimeUpdate.Get()
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}
