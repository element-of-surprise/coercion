package azblob

import (
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
)

const (
	mdKeyPlanID     = "PlanID"
	mdKeyGroupID    = "GroupID"
	mdKeyName       = "Name"
	mdKeyDescr      = "Descr"
	mdKeySubmitTime = "SubmitTime"
	mdKeyState      = "State"
)

// commitPlan commits a plan to blob storage. This commits the entire plan and all sub-objects.
// Write order: planEntry (with metadata) → sub-objects → workflow.Plan object
func (c creator) commitPlan(ctx context.Context, p *workflow.Plan) error {
	if p == nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeParameter, fmt.Errorf("commitPlan: plan cannot be nil"))
	}
	if p.ID == uuid.Nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("commitPlan: plan ID cannot be nil"))
	}

	// Use Plan.SubmitTime to determine container name
	// This ensures the plan and all sub-objects are in the same container
	containerName := containerForPlan(c.prefix, p.ID)

	if err := ensureContainer(ctx, c.client, containerName); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageCreate, fmt.Errorf("failed to create container: %w", err))
	}

	// Step 1: Create and upload planEntry (lightweight, IDs only) with metadata
	planEntry, err := planToPlanEntry(p)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	stateJSON, err := json.Marshal(p.State)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal plan state: %w", err))
	}

	md := map[string]*string{
		mdKeyPlanID:     toPtr(p.ID.String()),
		mdKeyName:       toPtr(p.Name),
		mdKeyDescr:      toPtr(p.Descr),
		mdKeySubmitTime: toPtr(p.SubmitTime.Format(time.RFC3339Nano)),
		mdKeyState:      toPtr(bytesToStr(stateJSON)),
	}

	if p.GroupID != uuid.Nil {
		md[mdKeyGroupID] = toPtr(p.GroupID.String())
	}

	planEntryData, err := json.Marshal(planEntry)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal planEntry: %w", err))
	}

	entryBlobName := planEntryBlobName(p.ID)
	if err := uploadBlob(ctx, c.client, containerName, entryBlobName, md, planEntryData); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to upload planEntry blob: %w", err))
	}

	// Step 2: Upload all sub-objects
	if err := c.uploadSubObjects(ctx, containerName, p); err != nil {
		_ = c.deleteBlob(ctx, containerName, entryBlobName)
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to upload sub-objects: %w", err))
	}

	// Step 3: Upload full workflow.Plan object (complete hierarchy)
	planObjectData, err := json.Marshal(p)
	if err != nil {
		_ = c.deleteBlob(ctx, containerName, entryBlobName)
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal plan object: %w", err))
	}

	objectBlobName := planObjectBlobName(p.ID)
	if err := uploadBlob(ctx, c.client, containerName, objectBlobName, nil, planObjectData); err != nil {
		_ = c.deleteBlob(ctx, containerName, entryBlobName)
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to upload plan object blob: %w", err))
	}

	return nil
}

// uploadSubObjects uploads all sub-object blobs for a plan.
func (c creator) uploadSubObjects(ctx context.Context, containerName string, p *workflow.Plan) error {
	for _, checks := range []*workflow.Checks{p.BypassChecks, p.PreChecks, p.PostChecks, p.ContChecks, p.DeferredChecks} {
		if checks != nil {
			if err := c.uploadChecksBlob(ctx, containerName, p.ID, checks); err != nil {
				return err
			}
		}
	}

	for i, block := range p.Blocks {
		if err := c.uploadBlockBlob(ctx, containerName, p.ID, block, i); err != nil {
			return err
		}
	}

	return nil
}

// uploadBlockBlob uploads a block blob and all its sub-objects.
func (c creator) uploadBlockBlob(ctx context.Context, containerName string, planID uuid.UUID, block *workflow.Block, pos int) error {
	blockEntry, err := blockToEntry(block, pos)
	if err != nil {
		return fmt.Errorf("failed to convert block to entry: %w", err)
	}

	blockData, err := json.Marshal(blockEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	blockBlobName := blockBlobName(planID, block.ID)
	if err := uploadBlob(ctx, c.client, containerName, blockBlobName, nil, blockData); err != nil {
		return fmt.Errorf("failed to upload block blob: %w", err)
	}

	for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
		if checks != nil {
			if err := c.uploadChecksBlob(ctx, containerName, planID, checks); err != nil {
				return err
			}
		}
	}

	for i, seq := range block.Sequences {
		if err := c.uploadSequenceBlob(ctx, containerName, planID, seq, i); err != nil {
			return err
		}
	}

	return nil
}

// uploadSequenceBlob uploads a sequence blob and all its actions.
func (c creator) uploadSequenceBlob(ctx context.Context, containerName string, planID uuid.UUID, seq *workflow.Sequence, pos int) error {
	seqEntry, err := sequenceToEntry(seq, pos)
	if err != nil {
		return fmt.Errorf("failed to convert sequence to entry: %w", err)
	}

	seqData, err := json.Marshal(seqEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal sequence: %w", err)
	}

	seqBlobName := sequenceBlobName(planID, seq.ID)
	if err := uploadBlob(ctx, c.client, containerName, seqBlobName, nil, seqData); err != nil {
		return fmt.Errorf("failed to upload sequence blob: %w", err)
	}

	// Upload sequence's actions
	for i, action := range seq.Actions {
		if err := c.uploadActionBlob(ctx, containerName, planID, action, i); err != nil {
			return err
		}
	}

	return nil
}

// uploadChecksBlob uploads a checks blob and all its actions.
func (c creator) uploadChecksBlob(ctx context.Context, containerName string, planID uuid.UUID, checks *workflow.Checks) error {
	// Convert checks to entry
	checksEntry, err := checksToEntry(checks)
	if err != nil {
		return fmt.Errorf("failed to convert checks to entry: %w", err)
	}

	// Marshal and upload checks blob
	checksData, err := json.Marshal(checksEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal checks: %w", err)
	}

	checksBlobName := checksBlobName(planID, checks.ID)
	if err := uploadBlob(ctx, c.client, containerName, checksBlobName, nil, checksData); err != nil {
		return fmt.Errorf("failed to upload checks blob: %w", err)
	}

	// Upload checks' actions
	for i, action := range checks.Actions {
		if err := c.uploadActionBlob(ctx, containerName, planID, action, i); err != nil {
			return err
		}
	}

	return nil
}

// uploadActionBlob uploads a single action blob.
func (c creator) uploadActionBlob(ctx context.Context, containerName string, planID uuid.UUID, action *workflow.Action, pos int) error {
	// Convert action to entry
	actionEntry, err := actionToEntry(action, pos)
	if err != nil {
		return fmt.Errorf("failed to convert action to entry: %w", err)
	}

	// Marshal and upload action blob
	actionData, err := json.Marshal(actionEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal action: %w", err)
	}

	actionBlobName := actionBlobName(planID, action.ID)
	if err := uploadBlob(ctx, c.client, containerName, actionBlobName, nil, actionData); err != nil {
		return fmt.Errorf("failed to upload action blob: %w", err)
	}

	return nil
}

// deleteBlob deletes a blob (used for cleanup on failure).
func (c creator) deleteBlob(ctx context.Context, containerName, blobName string) error {
	blobClient := c.client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	_, err := blobClient.Delete(ctx, &blob.DeleteOptions{})
	return err
}
