package sm

import (
	"fmt"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"

	"github.com/gostdlib/base/statemachine"
)

// finalStates is used to set the finalStates states on the Plan by examining the Plan's object states.
type finalStates struct {
	// unrun is the failure reason for the first Plan checks that never ran (still NotStarted). That is expected when
	// a block failed, which sends the Plan past PlanPostChecks, so blocks only reports it as a bug when no block
	// failed. FRUnknown when every check ran.
	unrun workflow.FailureReason
}

// start is simply the starting place for the statemachine. It does nothing.
func (f finalStates) start(req statemachine.Request[Data]) statemachine.Request[Data] {
	req.Next = f.bypassChecks
	return req
}

// bypassChecks looks through all the checks in the in the Plan bypass and Completes the Plan if there are
// bypass checks defined and they all pass. If there are no bypasses defined, the Plan is examined
// further.
func (f finalStates) bypassChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan

	skipped := f.examineBypasses(plan.BypassChecks)
	if skipped {
		req.Next = f.end // Records the Plan as Completed
		return req
	}
	req.Next = f.planChecks
	return req
}

// planChecks looks through all the checks in the Plan and fails the Plan if any of the checks failed
// and records the failure reason. It also examines DeferredActions and fails the Plan
// with FRDeferredAction if a FailElement=true batch failed. It does not do BypassChecks
// as those are handled in bypassChecks.
//
// DeferredActions failure takes precedence over a checks failure: DA runs after all
// checks and FailElement=true is an explicit opt-in, so its failure must not be masked
// by an earlier checks failure.
func (f finalStates) planChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan

	checksReason, unrun, checksErr := f.examineChecks(req.Ctx, [4]*workflow.Checks{plan.PreChecks, plan.ContChecks, plan.PostChecks, plan.DeferredChecks})
	daReason, daErr := f.examineDeferredActions(req.Ctx, plan.DeferredActions)

	if daErr != nil {
		state := plan.State.Get()
		state.Status = workflow.Failed
		plan.State.Set(state)
		plan.Reason = daReason
		req.Err = daErr
		req.Next = f.end
		return req
	}
	if checksErr != nil {
		state := plan.State.Get()
		state.Status = workflow.Failed
		plan.State.Set(state)
		plan.Reason = checksReason
		req.Err = checksErr
		req.Next = f.end
		return req
	}
	f.unrun = unrun
	req.Next = f.blocks
	return req
}

// examineDeferredActions returns FRDeferredAction and an error if the DeferredActions
// container is in a Failed state (set by PlanDeferredActions when a batch with
// FailElement=true failed). Nil or non-Failed is a pass.
func (f finalStates) examineDeferredActions(ctx context.Context, da *workflow.DeferredActions) (workflow.FailureReason, error) {
	if da == nil {
		return workflow.FRUnknown, nil
	}
	if da.State.Get().Status == workflow.Failed {
		return workflow.FRDeferredAction, errors.ErrPlugin(ctx, fmt.Errorf("DeferredActions failure"))
	}
	return workflow.FRUnknown, nil
}

// blocks checks the state of the block and fails the Plan if any of the blocks failed. If a block is not in a
// state we should be in, or every block completed but some Plan checks never ran, it generates a TypeBug error.
func (f finalStates) blocks(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan
	for _, block := range req.Data.Plan.Blocks {
		switch block.State.Get().Status {
		case workflow.Completed:
		case workflow.Failed:
			state := plan.State.Get()
			state.Status = workflow.Failed
			plan.State.Set(state)
			plan.Reason = workflow.FRBlock
			req.Err = errors.ErrPlugin(req.Ctx, fmt.Errorf("block(%s) failure", block.Name))
			return req
		default:
			state := plan.State.Get()
			state.Status = workflow.Failed
			plan.State.Set(state)
			plan.Reason = workflow.FRBlock
			req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("block(%s) End state reached in %s state, which is invalid", block.Name, block.State.Get().Status))
			return req
		}
	}
	if f.unrun != workflow.FRUnknown {
		state := plan.State.Get()
		state.Status = workflow.Failed
		plan.State.Set(state)
		plan.Reason = f.unrun
		req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("plan End state reached with %v checks that never ran, though nothing failed", f.unrun))
		return req
	}
	req.Next = f.end
	return req
}

// end records a Plan as Completed.
func (f finalStates) end(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan
	state := plan.State.Get()
	if state.Status < workflow.Completed {
		state.Status = workflow.Completed
	}
	plan.State.Set(state)
	return req
}

// examineBypasses checks to see if any gates were defined. If not, it returns false.
// If the gates were defined and they are in a Completed state, it returns true.
func (f finalStates) examineBypasses(gates *workflow.Checks) bool {
	if gates == nil {
		return false
	}
	if gates.State.Get().Status == workflow.Completed {
		return true
	}
	return false
}

// examineChecks Pre/Cont/Post/Deferred checks passed and returns a failure reason and an error if one of them failed.
// Checks still NotStarted are not a failure here: a failed block or earlier phase skips the Plan's PostChecks, so
// whether they are a bug depends on the blocks. The reason for the first of them is returned as unrun for blocks to
// decide. If nothing failed (or checks are nil) this returns workflow.FRUnknown as reason and a nil error.
func (f finalStates) examineChecks(ctx context.Context, checks [4]*workflow.Checks) (reason, unrun workflow.FailureReason, err error) {
	for i, check := range checks {
		if check == nil {
			continue
		}

		var t string
		var r workflow.FailureReason
		switch i {
		case 0:
			t = "PreChecks"
			r = workflow.FRPreCheck
		case 1:
			t = "ContChecks"
			r = workflow.FRContCheck
		case 2:
			t = "PostChecks"
			r = workflow.FRPostCheck
		case 3:
			t = "DeferredChecks"
			r = workflow.FRDeferredCheck
		}

		switch check.State.Get().Status {
		case workflow.Completed:
			continue
		case workflow.NotStarted:
			if unrun == workflow.FRUnknown {
				unrun = r
			}
			continue
		case workflow.Failed:
			return r, unrun, errors.ErrPlugin(ctx, fmt.Errorf("%s failure", t))
		default:
			return r, unrun, errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("plan End state reached with a %s in %s state, which is invalid", t, check.State.Get().Status))
		}
	}
	return workflow.FRUnknown, unrun, nil
}
