package azblob

import (
	"fmt"
	"io"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// fetchPlan fetches a plan from blob storage and reconstructs the full hierarchy.
func (r reader) fetchPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan, err := r.fetchPlanFromContainer(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan with ID %s not found", id))
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, err)
	}

	return plan, nil
}

// fetchPlanFromContainer fetches a plan from a specific container. If the plan is not running, we read the
// workflow.Plan object blob directly. If the plan is running, we reconstruct it from planEntry and sub-objects.
// Recovery: If planEntry exists but planObject doesn't, we delete the planEntry (incomplete write) and return not found.
func (r reader) fetchPlanFromContainer(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	containerName := containerForPlan(r.prefix, id)
	entryBlobName := planEntryBlobName(id)
	blobClient := r.client.ServiceClient().NewContainerClient(containerName).NewBlobClient(entryBlobName)
	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to get planEntry blob properties: %w", err))
	}

	lr, err := r.parsePlanMetadata(props.Metadata)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to parse planEntry metadata: %w", err))
	}

	if lr.State.Status == workflow.Running {
		return r.fetchRunningPlan(ctx, containerName, id, lr)
	}

	// Not running - read the workflow.Plan object blob directly
	objectBlobName := planObjectBlobName(id)

	resp, err := r.client.DownloadStream(ctx, containerName, objectBlobName, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download plan object blob: %w", err))
	}
	defer resp.Body.Close()

	// Read the blob data
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read plan object blob: %w", err))
	}

	// Unmarshal the full workflow.Plan object
	var plan workflow.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal plan object: %w", err))
	}

	// Set registry for all actions
	if err := r.setRegistry(&plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to set registry: %w", err))
	}

	return &plan, nil
}

// fetchRunningPlan fetches a running plan by reconstructing it from planEntry and all sub-objects.
func (r reader) fetchRunningPlan(ctx context.Context, containerName string, id uuid.UUID, lr storage.ListResult) (*workflow.Plan, error) {
	entry, err := r.fetchPlanEntry(ctx, containerName, id)
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
		State:      lr.State,
	}

	if entry.BypassChecks != uuid.Nil {
		plan.BypassChecks, err = r.fetchChecks(ctx, containerName, id, entry.BypassChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.PreChecks != uuid.Nil {
		plan.PreChecks, err = r.fetchChecks(ctx, containerName, id, entry.PreChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.PostChecks != uuid.Nil {
		plan.PostChecks, err = r.fetchChecks(ctx, containerName, id, entry.PostChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.ContChecks != uuid.Nil {
		plan.ContChecks, err = r.fetchChecks(ctx, containerName, id, entry.ContChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.DeferredChecks != uuid.Nil {
		plan.DeferredChecks, err = r.fetchChecks(ctx, containerName, id, entry.DeferredChecks)
		if err != nil {
			return nil, err
		}
	}

	plan.Blocks = make([]*workflow.Block, len(entry.Blocks))
	for i, blockID := range entry.Blocks {
		plan.Blocks[i], err = r.fetchBlock(ctx, containerName, id, blockID)
		if err != nil {
			return nil, err
		}
	}

	if err := r.setRegistry(plan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to set registry: %w", err))
	}

	return plan, nil
}

// fetchPlanEntry downloads and unmarshals a planEntry.
func (r reader) fetchPlanEntry(ctx context.Context, containerName string, planID uuid.UUID) (planEntry, error) {
	blobName := planEntryBlobName(planID)
	resp, err := r.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return planEntry{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download planEntry blob: %w", err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return planEntry{}, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read planEntry blob: %w", err))
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
	resp, err := r.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download checks blob: %w", err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read checks blob: %w", err))
	}

	var entry checksEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal checks: %w", err))
	}

	checks, err := entryToChecks(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to checks: %w", err))
	}

	// Fetch all actions
	checks.Actions = make([]*workflow.Action, len(entry.Actions))
	for i, actionID := range entry.Actions {
		checks.Actions[i], err = r.fetchAction(ctx, containerName, planID, actionID)
		if err != nil {
			return nil, err
		}
	}

	return checks, nil
}

// fetchBlock downloads a Block object and all its sub-objects (Checks and Sequences).
func (r reader) fetchBlock(ctx context.Context, containerName string, planID, blockID uuid.UUID) (*workflow.Block, error) {
	blobName := blockBlobName(planID, blockID)
	resp, err := r.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download block blob: %w", err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read block blob: %w", err))
	}

	var entry blocksEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal block: %w", err))
	}

	block, err := entryToBlock(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to block: %w", err))
	}

	// Fetch all check objects
	if entry.BypassChecks != uuid.Nil {
		block.BypassChecks, err = r.fetchChecks(ctx, containerName, planID, entry.BypassChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.PreChecks != uuid.Nil {
		block.PreChecks, err = r.fetchChecks(ctx, containerName, planID, entry.PreChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.PostChecks != uuid.Nil {
		block.PostChecks, err = r.fetchChecks(ctx, containerName, planID, entry.PostChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.ContChecks != uuid.Nil {
		block.ContChecks, err = r.fetchChecks(ctx, containerName, planID, entry.ContChecks)
		if err != nil {
			return nil, err
		}
	}
	if entry.DeferredChecks != uuid.Nil {
		block.DeferredChecks, err = r.fetchChecks(ctx, containerName, planID, entry.DeferredChecks)
		if err != nil {
			return nil, err
		}
	}

	// Fetch all sequences
	block.Sequences = make([]*workflow.Sequence, len(entry.Sequences))
	for i, seqID := range entry.Sequences {
		block.Sequences[i], err = r.fetchSequence(ctx, containerName, planID, seqID)
		if err != nil {
			return nil, err
		}
	}

	return block, nil
}

// fetchSequence downloads a Sequence object and all its Actions.
func (r reader) fetchSequence(ctx context.Context, containerName string, planID, sequenceID uuid.UUID) (*workflow.Sequence, error) {
	blobName := sequenceBlobName(planID, sequenceID)
	resp, err := r.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download sequence blob: %w", err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read sequence blob: %w", err))
	}

	var entry sequencesEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal sequence: %w", err))
	}

	seq, err := entryToSequence(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to sequence: %w", err))
	}

	// Fetch all actions
	seq.Actions = make([]*workflow.Action, len(entry.Actions))
	for i, actionID := range entry.Actions {
		seq.Actions[i], err = r.fetchAction(ctx, containerName, planID, actionID)
		if err != nil {
			return nil, err
		}
	}

	return seq, nil
}

// fetchAction downloads a single Action object.
func (r reader) fetchAction(ctx context.Context, containerName string, planID, actionID uuid.UUID) (*workflow.Action, error) {
	blobName := actionBlobName(planID, actionID)
	resp, err := r.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download action blob: %w", err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read action blob: %w", err))
	}

	var entry actionsEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal action: %w", err))
	}

	action, err := entryToAction(entry)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to convert entry to action: %w", err))
	}

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
