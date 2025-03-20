package execute

import (
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/gostdlib/base/statemachine"
)

type recoverData struct {
	searchResults []storage.Stream[storage.ListResult]
	plans         []*workflow.Plan
	agedOut       []*workflow.Plan
}

// recover is a state machine that recovers plans after a crash.
type recover struct {
	maxAge time.Duration
	store  storage.Vault
}

// Start starts the recovery process. It searches for running plans in the data store.
// The recovery process DOES NOT use concurrency due to the fact that the sqlite store is flawed
// and cannot handle concurrent reads and writes.
func (r *recover) Start(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	results, err := r.store.Search(req.Ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		req.Err = fmt.Errorf("failed to search for running plans: %w", err)
		return req
	}
	for result := range results {
		if result.Err != nil {
			req.Err = fmt.Errorf("failed mid search for running plans: %w", result.Err)
			return req
		}
		req.Data.searchResults = append(req.Data.searchResults, result)
	}

	req.Next = r.FetchPlans
	return req
}

// FetchPlans fetches the plans that are running from the data store in parallel.
func (r *recover) FetchPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	recovered := make([]*workflow.Plan, len(req.Data.searchResults))
	for i, result := range req.Data.searchResults {
		plan, err := r.store.Read(req.Ctx, result.Result.ID)
		if err != nil {
			req.Err = fmt.Errorf("failed to read Plan(%s): %w", result.Result.ID, err)
			return req
		}
		recovered[i] = plan
	}
	req.Data.plans = recovered
	req.Next = r.FilterPlans
	return req
}

// This represents a concurrent recovery that won't work with sqlite store at the moment.
// I'm going to keep it so I can come back to it later and work on the sqlite conundrum.
/*
type recoverData struct {
	searchResults chan storage.Stream[storage.ListResult]
	plans         []*workflow.Plan
	agedOut       []*workflow.Plan
}

func (r *recover) Start(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	req.Ctx = context.WithoutCancel(req.Ctx)
	results, err := r.store.Search(req.Ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		req.Err = fmt.Errorf("failed to search for running plans: %w", err)
		return req
	}
	req.Data.searchResults = results
	req.Next = r.FetchPlans
	return req
}

// FetchPlans fetches the plans that are running from the data store in parallel.
func (r *recover) FetchPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	g := context.Pool(req.Ctx).Limited(10).Group()
	recovered := []*workflow.Plan{}
	i := -1
	for result := range req.Data.searchResults {
		i++
		x := i
		if result.Err != nil {
			req.Err = fmt.Errorf("failed mid search for running plans: %w", result.Err)
			return req
		}
		g.Go(req.Ctx, func(ctx context.Context) error {
			plan, err := r.store.Read(ctx, result.Result.ID)
			if err != nil {
				return fmt.Errorf("failed to read Plan(%s): %w", result.Result.ID, err)
			}
			recovered[x] = plan
			return nil
		})
	}
	if err := g.Wait(req.Ctx); err != nil {
		req.Err = err
		return req
	}
	req.Data.plans = recovered
	req.Next = r.FilterPlans
	return req
}
*/

// FilterPlans filters out the plans that have exceeded the recovery time into a list and removes them from the list of plans.
func (r *recover) FilterPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	now := time.Now()

	for i, plan := range req.Data.plans {
		if plan.LastUpdate(req.Ctx).Add(r.maxAge).Before(now) {
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
	req.Next = r.AgedOut
	return req
}

// AgedOut marks the plans that have exceeded the recovery time as failed.
func (r *recover) AgedOut(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	for _, plan := range req.Data.agedOut {
		plan.State.Status = workflow.Failed
		plan.Reason = workflow.FRExceedRecovery
		plan.State.End = time.Now()
		runningToFailed(req.Ctx, plan)

		if err := r.store.UpdatePlan(req.Ctx, plan); err != nil {
			req.Err = fmt.Errorf("failed to update Plan(%s): %w", plan.ID, err)
			return req
		}
	}
	req.Next = r.Done
	return req
}

// Done is the final state of the recovery process.
func (r *recover) Done(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	return req
}

// runningToFailed marks all objects in running states in the plan as failed.
func runningToFailed(ctx context.Context, p *workflow.Plan) {
	p.State.End = time.Now()
	for item := range walk.Plan(ctx, p) {
		state := item.Value.(getStater).GetState()
		if state.Status == workflow.Running {
			state.Status = workflow.Failed
			state.End = time.Now()
		}
	}
}
