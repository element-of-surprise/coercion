package azblob

import (
	"fmt"
	"reflect"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/concurrency/worker"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// fetchConcurrency bounds the number of concurrent blob fetches spawned at each individual object
// fetch. Each fan-out creates its own Limited pool based on worker.Default() (the unbounded default
// pool), never on another Limited pool, so nested waits can never starve and deadlock.
const fetchConcurrency = 10

// unwrapGroup collapses the *sync.Errors that a worker Group's Wait() returns into an error whose
// chain still exposes the underlying errors, so blobops.IsNotFound and errors.Is/As keep working
// through it. A fan-out runs every sibling to completion with no first-error short-circuit, so on
// mixed failures the group holds a not-found alongside other failures in nondeterministic completion
// order. Because blobops.IsNotFound only inspects the first ResponseError errors.As reaches, that
// ordering would let a genuine not-found mask a transient/internal error (or vice versa) run to run.
// To keep classification stable, when any non-not-found failure is present we surface only those, so
// a retryable error is never reported as a not-found; a not-found is reported only when every failure
// was a not-found. Non-group errors (and nil) pass through unchanged.
func unwrapGroup(err error) error {
	if err == nil {
		return nil
	}
	var errs *sync.Errors
	if !errors.As(err, &errs) {
		return err
	}
	collected := errs.Errors()
	if len(collected) == 0 {
		return nil
	}
	other := make([]error, 0, len(collected))
	var notFound []error
	for _, e := range collected {
		if blobops.IsNotFound(e) {
			notFound = append(notFound, e)
			continue
		}
		other = append(other, e)
	}
	if len(other) > 0 {
		return errors.Join(other...)
	}
	return errors.Join(notFound...)
}

// goFetchChecks schedules a concurrent fetch of the checks blob identified by id (a no-op when id is
// uuid.Nil) on g, assigning the result via set. It removes the need to pass the address of a struct
// field into the goroutine. Shared by fetchRunningPlan and fetchBlock, which populate the same five
// check-type fields on Plan and Block respectively.
func (r reader) goFetchChecks(ctx context.Context, g *sync.Group, containerName string, planID, id uuid.UUID, set func(*workflow.Checks)) {
	if id == uuid.Nil {
		return
	}
	g.Go(ctx, func(ctx context.Context) error {
		c, err := r.fetchChecks(ctx, containerName, planID, id)
		if err != nil {
			return err
		}
		set(c)
		return nil
	})
}

type setPlanIDer interface {
	SetPlanID(uuid.UUID)
}

// fetchPlan fetches a plan from blob storage and reconstructs the full hierarchy.
func (r reader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan, err := r.fetchPlanFromContainer(ctx, id)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan with ID %s not found: %w", id, storage.ErrNotFound))
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, err)
	}

	return plan, nil
}

// fetchPlanFromContainer fetches a plan from a specific container. If the plan is not running, we read the
// workflow.Plan object blob directly. If the plan is running, we reconstruct it from planEntry and sub-objects.
// Recovery: If planEntry exists but planObject doesn't, we delete the planEntry (incomplete write) and return not found.
func (r reader) fetchPlanFromContainer(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	pm, err := r.fetchPlanEntryMeta(ctx, id)
	if err != nil {
		return nil, err
	}

	containerName := containerForPlan(r.prefix, id)
	if pm.State.Status == workflow.Running {
		return r.fetchRunningPlan(ctx, containerName, id, pm.ListResult)
	}
	return r.fetchNonRunningPlan(ctx, containerName, id)
}

func (r reader) fetchPlanEntryMeta(ctx context.Context, id uuid.UUID) (planMeta, error) {
	containerName := containerForPlan(r.prefix, id)
	entryBlobName := planEntryBlobName(id)
	md, err := r.client.GetMetadata(ctx, containerName, entryBlobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return planMeta{}, err
		}
		return planMeta{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to get planEntry blob metadata: %w", err))
	}
	pm, err := mapToPlanMeta(md)
	if err != nil {
		return planMeta{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to parse planEntry metadata: %w", err))
	}
	return pm, nil
}

func (r reader) fetchPlanObjectMeta(ctx context.Context, id uuid.UUID) (planMeta, error) {
	containerName := containerForPlan(r.prefix, id)
	objBlobName := planObjectBlobName(id)
	md, err := r.client.GetMetadata(ctx, containerName, objBlobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return planMeta{}, err
		}
		return planMeta{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to get planEntry blob metadata: %w", err))
	}
	pm, err := mapToPlanMeta(md)
	if err != nil {
		return planMeta{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to parse planEntry metadata: %w", err))
	}
	return pm, nil
}

// fetchNonRunningPlan fetches a non-running plan by downloading the full workflow.Plan object blob.
// If the object blob doesn't exist but the entry does, the orphaned entry is cleaned up.
func (r reader) fetchNonRunningPlan(ctx context.Context, containerName string, id uuid.UUID) (*workflow.Plan, error) {
	// Not running - read the workflow.Plan object blob directly
	objectBlobName := planObjectBlobName(id)

	data, err := r.client.GetBlob(ctx, containerName, objectBlobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			// Object blob doesn't exist but entry does (we got here via entry metadata).
			// This is an orphaned entry from a failed creation - clean it up.
			_ = r.client.DeleteBlob(ctx, containerName, planEntryBlobName(id))
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download plan object blob: %w", err))
	}

	// Unmarshal the full workflow.Plan object
	plan := &workflow.Plan{}
	if err := json.Unmarshal(data, plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal plan object: %w", err))
	}

	// Set registry for all actions
	if err := r.setRegistry(plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to set registry: %w", err))
	}

	if err := r.fixActions(ctx, plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to fix action requests: %w", err))
	}

	for item := range walk.Plan(plan) {
		if item.Value.Type() != workflow.OTPlan {
			item.Value.(setPlanIDer).SetPlanID(plan.ID)
		}
	}

	return plan, nil
}

// fetchRunningPlan fetches a running plan by reconstructing it from planEntry and all sub-objects.
func (r reader) fetchRunningPlan(ctx context.Context, containerName string, id uuid.UUID, lr storage.ListResult) (*workflow.Plan, error) {
	entry, err := r.fetchPlanEntry(ctx, id)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to fetch planEntry: %w", err))
	}

	plan := &workflow.Plan{
		ID:         id,
		Name:       lr.Name,
		Descr:      lr.Descr,
		GroupID:    lr.GroupID,
		Meta:       entry.Meta,
		SubmitTime: lr.SubmitTime,
		Reason:     entry.Reason,
	}
	plan.State.Set(lr.State)

	// Fetch all plan-level checks, deferred actions, and blocks concurrently. Each writes to a
	// distinct field/index, so no synchronization is needed beyond the group's Wait.
	g := worker.Default().Limited(ctx, "azBlobReaderPlan", fetchConcurrency).Group()

	r.goFetchChecks(ctx, &g, containerName, id, entry.BypassChecks, func(c *workflow.Checks) { plan.BypassChecks = c })
	r.goFetchChecks(ctx, &g, containerName, id, entry.PreChecks, func(c *workflow.Checks) { plan.PreChecks = c })
	r.goFetchChecks(ctx, &g, containerName, id, entry.PostChecks, func(c *workflow.Checks) { plan.PostChecks = c })
	r.goFetchChecks(ctx, &g, containerName, id, entry.ContChecks, func(c *workflow.Checks) { plan.ContChecks = c })
	r.goFetchChecks(ctx, &g, containerName, id, entry.DeferredChecks, func(c *workflow.Checks) { plan.DeferredChecks = c })

	if entry.DeferredActions != uuid.Nil {
		g.Go(ctx, func(ctx context.Context) error {
			da, err := r.fetchDeferredActions(ctx, containerName, id, entry.DeferredActions)
			if err != nil {
				return err
			}
			plan.DeferredActions = da
			return nil
		})
	}

	plan.Blocks = make([]*workflow.Block, len(entry.Blocks))
	for i, blockID := range entry.Blocks {
		g.Go(ctx, func(ctx context.Context) error {
			block, err := r.fetchBlock(ctx, containerName, id, blockID)
			if err != nil {
				return err
			}
			plan.Blocks[i] = block
			return nil
		})
	}

	if err := unwrapGroup(g.Wait(ctx)); err != nil {
		return nil, err
	}

	if err := r.setRegistry(plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to set registry: %w", err))
	}

	return plan, nil
}

// fetchPlanEntry downloads and unmarshals a planEntry.
func (r reader) fetchPlanEntry(ctx context.Context, planID uuid.UUID) (planEntry, error) {
	containerName := containerForPlan(r.prefix, planID)

	blobName := planEntryBlobName(planID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return planEntry{}, err
		}
		return planEntry{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download planEntry blob: %w", err))
	}

	var entry planEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return planEntry{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal planEntry: %w", err))
	}

	return entry, nil
}

// fetchChecks downloads a Checks object and all its Actions.
func (r reader) fetchChecks(ctx context.Context, containerName string, planID, checksID uuid.UUID) (*workflow.Checks, error) {
	blobName := checksBlobName(planID, checksID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download checks blob: %w", err))
	}

	var entry checksEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal checks: %w", err))
	}

	checks, err := entryToChecks(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to checks: %w", err))
	}
	checks.SetPlanID(planID)

	// Fetch all actions concurrently; each writes a distinct slice index.
	checks.Actions = make([]*workflow.Action, len(entry.Actions))
	g := worker.Default().Limited(ctx, "azBlobReaderChecks", fetchConcurrency).Group()
	for i, actionID := range entry.Actions {
		g.Go(ctx, func(ctx context.Context) error {
			action, err := r.fetchAction(ctx, containerName, planID, actionID)
			if err != nil {
				return err
			}
			checks.Actions[i] = action
			return nil
		})
	}
	if err := unwrapGroup(g.Wait(ctx)); err != nil {
		return nil, err
	}

	return checks, nil
}

// fetchBlock downloads a Block object and all its sub-objects (Checks and Sequences).
func (r reader) fetchBlock(ctx context.Context, containerName string, planID, blockID uuid.UUID) (*workflow.Block, error) {
	blobName := blockBlobName(planID, blockID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download block blob: %w", err))
	}

	var entry blocksEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal block: %w", err))
	}

	block, err := entryToBlock(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to block: %w", err))
	}
	block.SetPlanID(planID)

	// Fetch all check objects and sequences concurrently; each writes a distinct field/index.
	g := worker.Default().Limited(ctx, "azBlobReaderBlock", fetchConcurrency).Group()

	r.goFetchChecks(ctx, &g, containerName, planID, entry.BypassChecks, func(c *workflow.Checks) { block.BypassChecks = c })
	r.goFetchChecks(ctx, &g, containerName, planID, entry.PreChecks, func(c *workflow.Checks) { block.PreChecks = c })
	r.goFetchChecks(ctx, &g, containerName, planID, entry.PostChecks, func(c *workflow.Checks) { block.PostChecks = c })
	r.goFetchChecks(ctx, &g, containerName, planID, entry.ContChecks, func(c *workflow.Checks) { block.ContChecks = c })
	r.goFetchChecks(ctx, &g, containerName, planID, entry.DeferredChecks, func(c *workflow.Checks) { block.DeferredChecks = c })

	block.Sequences = make([]*workflow.Sequence, len(entry.Sequences))
	for i, seqID := range entry.Sequences {
		g.Go(ctx, func(ctx context.Context) error {
			seq, err := r.fetchSequence(ctx, containerName, planID, seqID)
			if err != nil {
				return err
			}
			block.Sequences[i] = seq
			return nil
		})
	}

	if err := unwrapGroup(g.Wait(ctx)); err != nil {
		return nil, err
	}

	return block, nil
}

// fetchSequence downloads a Sequence object and all its Actions.
func (r reader) fetchSequence(ctx context.Context, containerName string, planID, sequenceID uuid.UUID) (*workflow.Sequence, error) {
	blobName := sequenceBlobName(planID, sequenceID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download sequence blob: %w", err))
	}

	var entry sequencesEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal sequence: %w", err))
	}

	seq, err := entryToSequence(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to sequence: %w", err))
	}
	seq.SetPlanID(planID)

	// Fetch all actions concurrently; each writes a distinct slice index.
	seq.Actions = make([]*workflow.Action, len(entry.Actions))
	g := worker.Default().Limited(ctx, "azBlobReaderSequence", fetchConcurrency).Group()
	for i, actionID := range entry.Actions {
		g.Go(ctx, func(ctx context.Context) error {
			action, err := r.fetchAction(ctx, containerName, planID, actionID)
			if err != nil {
				return err
			}
			seq.Actions[i] = action
			return nil
		})
	}
	if err := unwrapGroup(g.Wait(ctx)); err != nil {
		return nil, err
	}

	return seq, nil
}

// fetchAction downloads a single Action object.
func (r reader) fetchAction(ctx context.Context, containerName string, planID, actionID uuid.UUID) (*workflow.Action, error) {
	blobName := actionBlobName(planID, actionID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download action blob: %w", err))
	}

	action, err := entryToAction(ctx, r.reg, data)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to action: %w", err))
	}
	action.SetPlanID(planID)

	return action, nil
}

// setRegistry sets the registry on all actions in the plan.
func (r reader) setRegistry(plan *workflow.Plan) error {
	// Set registry on all checks actions
	for _, checks := range []*workflow.Checks{plan.BypassChecks, plan.PreChecks, plan.PostChecks, plan.ContChecks, plan.DeferredChecks} {
		if checks != nil {
			for _, action := range checks.Actions {
				action.SetRegister(r.reg)
			}
		}
	}

	// Set registry on all DeferredActions batch actions
	if plan.DeferredActions != nil {
		for _, batch := range plan.DeferredActions.DeferredBatches {
			for _, action := range batch.Actions {
				action.SetRegister(r.reg)
			}
		}
	}

	// Set registry on all block/sequence actions
	for _, block := range plan.Blocks {
		// Block checks
		for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
			if checks != nil {
				for _, action := range checks.Actions {
					action.SetRegister(r.reg)
				}
			}
		}

		// Sequence actions
		for _, seq := range block.Sequences {
			for _, action := range seq.Actions {
				action.SetRegister(r.reg)
			}
		}
	}

	return nil
}

// fixActions reconstructs Action.Req and Attempt.Resp fields after unmarshaling from JSON.
func (r reader) fixActions(ctx context.Context, plan *workflow.Plan) error {
	fixAction := func(action *workflow.Action) error {
		plug := r.reg.Plugin(action.Plugin)
		if plug == nil {
			return fmt.Errorf("plugin %s not found", action.Plugin)
		}

		// Fix Action.Req
		if action.Req != nil {
			reqBytes, err := json.Marshal(action.Req)
			if err != nil {
				return fmt.Errorf("failed to marshal req: %w", err)
			}

			req := plug.Request()
			if req != nil {
				if reflect.TypeOf(req).Kind() != reflect.Pointer {
					if err := json.Unmarshal(reqBytes, &req); err != nil {
						return fmt.Errorf("failed to unmarshal req: %w", err)
					}
				} else {
					if err := json.Unmarshal(reqBytes, req); err != nil {
						return fmt.Errorf("failed to unmarshal req: %w", err)
					}
				}
				action.Req = req
			}
		}

		// Fix Attempt.Resp for all attempts
		attempts := action.Attempts.Get()
		for i := range attempts {
			if attempts[i].Resp != nil {
				respBytes, err := json.Marshal(attempts[i].Resp)
				if err != nil {
					return fmt.Errorf("failed to marshal attempt resp: %w", err)
				}

				resp := plug.Response()
				if resp != nil {
					if reflect.TypeOf(resp).Kind() != reflect.Pointer {
						if err := json.Unmarshal(respBytes, &resp); err != nil {
							return fmt.Errorf("failed to unmarshal attempt resp: %w", err)
						}
					} else {
						if err := json.Unmarshal(respBytes, resp); err != nil {
							return fmt.Errorf("failed to unmarshal attempt resp: %w", err)
						}
					}
					attempts[i].Resp = resp
				}
			}
		}
		action.Attempts.Set(attempts)

		return nil
	}

	// Fix all actions in plan-level checks
	for _, checks := range []*workflow.Checks{plan.BypassChecks, plan.PreChecks, plan.PostChecks, plan.ContChecks, plan.DeferredChecks} {
		if checks != nil {
			for _, action := range checks.Actions {
				if err := fixAction(action); err != nil {
					return err
				}
			}
		}
	}

	// Fix all actions in DeferredActions batches
	if plan.DeferredActions != nil {
		for _, batch := range plan.DeferredActions.DeferredBatches {
			for _, action := range batch.Actions {
				if err := fixAction(action); err != nil {
					return err
				}
			}
		}
	}

	// Fix all actions in blocks
	for _, block := range plan.Blocks {
		// Block-level checks
		for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
			if checks != nil {
				for _, action := range checks.Actions {
					if err := fixAction(action); err != nil {
						return err
					}
				}
			}
		}

		// Sequence actions
		for _, seq := range block.Sequences {
			for _, action := range seq.Actions {
				if err := fixAction(action); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
