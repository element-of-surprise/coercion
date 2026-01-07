package execute

import (
	"time"

	"github.com/element-of-surprise/coercion/workflow"
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

// start starts the recovery process. It searches for running plans in the data store.
// The recovery process DOES NOT use concurrency due to the fact that the sqlite store is flawed
// and cannot handle concurrent reads and writes.
func (r *recover) start(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	results, err := r.store.Search(req.Ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		req.Err = err
		return req
	}
	for result := range results {
		if result.Err != nil {
			req.Err = result.Err
			return req
		}
		req.Data.searchResults = append(req.Data.searchResults, result)
	}

	req.Next = r.fetchPlans
	return req
}

// fetchPlans fetches the plans that are running from the data store in parallel.
func (r *recover) fetchPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	recovered := make([]*workflow.Plan, len(req.Data.searchResults))
	for i, result := range req.Data.searchResults {
		plan, err := r.store.Read(req.Ctx, result.Result.ID)
		if err != nil {
			req.Err = err
			return req
		}
		recovered[i] = plan
	}
	req.Data.plans = recovered
	req.Next = r.filterPlans
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

// filterPlans filters out the plans that have exceeded the recovery time into a list and removes them from the list of plans.
func (r *recover) filterPlans(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	now := time.Now()

	for i, plan := range req.Data.plans {
		if walk.LastUpdate(req.Ctx, plan).Add(r.maxAge).Before(now) {
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

// agedOut marks the plans that have exceeded the recovery time as failed.
func (r *recover) agedOut(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	for _, plan := range req.Data.agedOut {
		state := plan.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		plan.State.Set(state)
		plan.Reason = workflow.FRExceedRecovery
		runningToFailed(plan)

		if err := r.store.UpdatePlan(req.Ctx, plan); err != nil {
			req.Err = err
			return req
		}
	}
	req.Next = r.done
	return req
}

// done is the final state of the recovery process.
func (r *recover) done(req statemachine.Request[recoverData]) statemachine.Request[recoverData] {
	return req
}

// runningToFailed marks all objects in running states in the plan as failed.
func runningToFailed(p *workflow.Plan) {
	state := p.State.Get()
	state.End = time.Now()
	p.State.Set(state)
	for item := range walk.Plan(p) {
		state := item.Value.(getSetStates)
		objState := state.GetState()
		if objState.Status == workflow.Running {
			objState.Status = workflow.Failed
			objState.End = time.Now()
			state.SetState(objState)
		}
	}
}
