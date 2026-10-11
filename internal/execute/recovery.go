package execute

import (
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/values/chans"
)

type recoverData struct {
	searchResults []storage.Stream[storage.ListResult]
	plans         []*workflow.Plan
	agedOut       []*workflow.Plan
	// unrecovered are the Running plans recovery could not read or age out because storage failed. They are left to
	// the background retry (see Plans.retryResume) rather than failing recovery, and so New, for every plan.
	unrecovered []unrecovered
}

// unrecovered is a Running plan that recovery could not handle, and when to give up retrying it and restart.
type unrecovered struct {
	id        uuid.UUID
	restartAt time.Time
}

// recover is a state machine that recovers plans after a crash.
type recover struct {
	maxAge time.Duration
	store  storage.Vault
}

// start starts the recovery process. It searches for running plans in the data store.
// The recovery process DOES NOT use concurrency due to the fact that the sqlite store is flawed
// and cannot handle concurrent reads and writes.
func (r *recover) start(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	// A store whose Recovery already listed the Running plans hands them over, saving a second full listing.
	if rr, ok := r.store.(storage.RecoveredRunning); ok {
		if results, ok := rr.RecoveredRunning(); ok {
			for _, lr := range results {
				req.Data.searchResults = append(req.Data.searchResults, storage.Stream[storage.ListResult]{Result: lr})
			}
			req.Next = r.fetchPlans
			return req
		}
	}

	results, err := r.store.Search(req.Ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		// E returns an error that is already typed unchanged.
		req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeStorageList, err)
		return req
	}
	for result := range chans.Iter(req.Ctx, results) {
		if result.Err != nil {
			req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeStorageList, result.Err)
			return req
		}
		req.Data.searchResults = append(req.Data.searchResults, result)
	}
	// A producer may close the stream early when the Context ends, and Iter stops when it does; either way the list is
	// incomplete, and recovering from it would leave the missing Running plans with nothing running them.
	if err := req.Ctx.Err(); err != nil {
		req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("searching for running plans: %w", context.Cause(req.Ctx)))
		return req
	}

	req.Next = r.fetchPlans
	return req
}

// fetchPlans reads the plans that are running from the data store. A plan that cannot be read does not fail recovery,
// since that would fail New, and so every restart, over one plan:
//   - A plan that no longer exists is skipped.
//   - A plan whose storage is inconsistent is logged and skipped. Retrying or restarting cannot fix it; it needs repair,
//     and a Wait for it returns the error.
//   - Any other failure is storage failing for now. The plan is left to the background retry. Its age is unknown, so
//     its restart time is counted from now.
//
// A read that fails because the Context ended is not storage failing: recovery fails with a TypeTimeout error, so
// New's caller learns its deadline passed rather than plans being queued for retry, which could exit the process.
func (r *recover) fetchPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	recovered := make([]*workflow.Plan, 0, len(req.Data.searchResults))
	for _, result := range req.Data.searchResults {
		id := result.Result.ID
		plan, err := r.store.Read(req.Ctx, id)
		switch {
		case err == nil:
			recovered = append(recovered, plan)
		case errors.IsNotFound(err):
			context.Log(req.Ctx).Warn("coercion: recovered plan no longer exists, skipping it", "id", id)
		case errors.IsStorageInconsistent(err):
			context.Log(req.Ctx).Error("coercion: recovered plan's storage is inconsistent and cannot be recovered until it is repaired", "id", id, "error", err)
		case req.Ctx.Err() != nil:
			req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("reading recovered plan(%s): %w", id, context.Cause(req.Ctx)))
			return req
		default:
			context.Log(req.Ctx).Error("coercion: could not read recovered plan, retrying it in the background", "id", id, "error", err)
			now := time.Now()
			req.Data.unrecovered = append(req.Data.unrecovered, unrecovered{id: id, restartAt: restartBy(now, r.maxAge, now)})
		}
	}
	req.Data.plans = recovered
	req.Next = r.filterPlans
	return req
}

// filterPlans filters out the plans that have exceeded the recovery time into a list and removes them from the list of plans.
func (r *recover) filterPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	now := time.Now()

	for i, plan := range req.Data.plans {
		if isAgedOut(req.Ctx, plan, r.maxAge, now) {
			req.Data.agedOut = append(req.Data.agedOut, plan)
			req.Data.plans[i] = nil
		}
	}
	plans := []*workflow.Plan{}
	for i := 0; i < len(req.Data.plans); i++ {
		if req.Data.plans[i] != nil {
			plans = append(plans, req.Data.plans[i])
		}
	}
	req.Data.plans = plans
	req.Next = r.agedOut
	return req
}

// agedOut marks the plans that have exceeded the recovery time as failed. A plan whose write fails does not fail
// recovery: it is left to the background retry, whose Resume ages it out again. A write that fails because the
// Context ended is not storage failing, and fails recovery with a TypeTimeout error (see fetchPlans).
func (r *recover) agedOut(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	for _, plan := range req.Data.agedOut {
		// Taken before ageOut changes the Plan.
		restartAt := restartBy(walk.LastUpdate(req.Ctx, plan), r.maxAge, time.Now())
		err := ageOut(req.Ctx, r.store, plan)
		if err != nil && req.Ctx.Err() != nil {
			req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("aging out recovered plan(%s): %w", plan.ID, context.Cause(req.Ctx)))
			return req
		}
		if err != nil {
			err = fmt.Errorf("plan(%s) is too old to recover and could not be marked failed: %w", plan.ID, err)
			context.Log(req.Ctx).Error("coercion: could not age out recovered plan, retrying it in the background", "id", plan.ID, "error", err)
			req.Data.unrecovered = append(req.Data.unrecovered, unrecovered{id: plan.ID, restartAt: restartAt})
		}
	}
	req.Next = r.done
	return req
}

// ageOut marks a Running plan that has not been updated within the recovery limit as Failed with FRExceedRecovery, and
// fails its running objects. The objects are written before the Plan, each after its descendants (see storage.ChangesUpdater).
//
// The failed objects end at the last time the plan was known to be alive, not now. Recovery decides whether a plan is
// too old from every object's Start and End (walk.LastUpdate), so if a write fails part way through, an object stamped
// with the current time would make the retry think the plan was updated and recover it instead of aging it out. With
// the old time the retry sees the same stale plan and ages it out again, which converges. The Plan itself is written
// last and is never examined again once it is Failed, so it ends now.
func ageOut(ctx context.Context, store storage.Updater, plan *workflow.Plan) error {
	last := walk.LastUpdate(ctx, plan)
	before := changes.Record(plan)

	state := plan.State.Get()
	state.Status = workflow.Failed
	state.End = time.Now()
	plan.State.Set(state)
	plan.Reason = workflow.FRExceedRecovery

	walk.SettleRunning(plan, workflow.Failed, last)
	if err := store.UpdateChanges(ctx, plan, before); err != nil {
		return err
	}
	return store.UpdatePlan(ctx, plan)
}

// isAgedOut reports whether plan has gone longer than maxAge without an update, so it cannot be recovered.
func isAgedOut(ctx context.Context, plan *workflow.Plan, maxAge time.Duration, now time.Time) bool {
	return walk.LastUpdate(ctx, plan).Add(maxAge).Before(now)
}

// done is the final state of the recovery process.
func (r *recover) done(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	return req
}
