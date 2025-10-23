package azblob

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.Recovery = recovery{}

// recovery implements the storage.Recovery interface.
type recovery struct {
	reader  reader
	updater updater

	private.Storage
}

// Recovery implements storage.Recovery.Recovery(). It performs recovery operations
// on plans that may have incomplete blob storage due to failures or crashes.
//
// Recovery strategy:
// 1. Read the plan blob (which contains the full hierarchy)
// 2. Check if the plan is currently running (status == Running)
// 3. If not running, verify all sub-object blobs exist
// 4. Recreate any missing blobs from the plan hierarchy
func (r recovery) Recovery(ctx context.Context) error {
	// List all plans in recent containers
	containers := containerNames(r.reader.prefix)

	for _, containerName := range containers {
		if err := r.recoverPlansInContainer(ctx, containerName); err != nil {
			// Log error but continue with other containers
			// We don't want a failure in one container to prevent recovery of others
			continue
		}
	}

	return nil
}

// recoverPlansInContainer recovers all plans in a specific container.
func (r recovery) recoverPlansInContainer(ctx context.Context, containerName string) error {
	exists, err := containerExists(ctx, r.reader.client, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return nil // Container doesn't exist, nothing to recover
	}

	planBlobs, err := r.reader.listPlansInContainer(ctx, containerName)
	if err != nil {
		return err
	}

	for _, planResult := range planBlobs {
		if err := r.recoverPlan(ctx, containerName, planResult.ID); err != nil {
			return err
		}
	}

	return nil
}

// recoverPlan recovers a single plan by ensuring all sub-object blobs exist.
func (r recovery) recoverPlan(ctx context.Context, containerName string, planID uuid.UUID) error {
	// Read the plan to get the full hierarchy
	plan, err := r.reader.fetchPlan(ctx, planID)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read plan for recovery: %w", err))
	}

	// Only recover if the plan is not currently running
	if plan.State != nil && plan.State.Status == workflow.Running {
		return nil // Plan is running, don't interfere
	}

	// Verify and recreate missing blobs
	if err := r.ensureSubObjectBlobs(ctx, containerName, plan); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to ensure sub-object blobs: %w", err))
	}

	return nil
}

// ensureSubObjectBlobs ensures all sub-object blobs exist for a plan.
func (r recovery) ensureSubObjectBlobs(ctx context.Context, containerName string, plan *workflow.Plan) error {
	// Create a temporary creator to upload missing blobs
	c := creator{
		prefix:   r.reader.prefix,
		client:   r.reader.client,
		endpoint: r.reader.endpoint,
		reader:   r.reader,
	}

	// Ensure checks blobs exist
	for _, checks := range []*workflow.Checks{plan.BypassChecks, plan.PreChecks, plan.PostChecks, plan.ContChecks, plan.DeferredChecks} {
		if checks != nil {
			if err := r.ensureChecksBlob(ctx, c, containerName, plan.ID, checks); err != nil {
				return err
			}
		}
	}

	// Ensure block blobs exist
	for i, block := range plan.Blocks {
		if err := r.ensureBlockBlob(ctx, c, containerName, plan.ID, block, i); err != nil {
			return err
		}
	}

	return nil
}

// ensureBlockBlob ensures a block blob and all its sub-objects exist.
func (r recovery) ensureBlockBlob(ctx context.Context, c creator, containerName string, planID uuid.UUID, block *workflow.Block, pos int) error {
	blockBlobName := blockBlobName(planID, block.ID)
	exists, err := r.blobExists(ctx, containerName, blockBlobName)
	if err != nil {
		return err
	}

	if !exists {
		if err := c.uploadBlockBlob(ctx, containerName, planID, block, pos); err != nil {
			return fmt.Errorf("failed to recreate block blob: %w", err)
		}
	}

	for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
		if checks != nil {
			if err := r.ensureChecksBlob(ctx, c, containerName, planID, checks); err != nil {
				return err
			}
		}
	}

	for i, seq := range block.Sequences {
		if err := r.ensureSequenceBlob(ctx, c, containerName, planID, seq, i); err != nil {
			return err
		}
	}

	return nil
}

// ensureSequenceBlob ensures a sequence blob and all its actions exist.
func (r recovery) ensureSequenceBlob(ctx context.Context, c creator, containerName string, planID uuid.UUID, seq *workflow.Sequence, pos int) error {
	seqBlobName := sequenceBlobName(planID, seq.ID)
	exists, err := r.blobExists(ctx, containerName, seqBlobName)
	if err != nil {
		return err
	}

	if !exists {
		if err := c.uploadSequenceBlob(ctx, containerName, planID, seq, pos); err != nil {
			return fmt.Errorf("failed to recreate sequence blob: %w", err)
		}
	}

	for i, action := range seq.Actions {
		if err := r.ensureActionBlob(ctx, c, containerName, planID, action, i); err != nil {
			return err
		}
	}

	return nil
}

// ensureChecksBlob ensures a checks blob and all its actions exist.
func (r recovery) ensureChecksBlob(ctx context.Context, c creator, containerName string, planID uuid.UUID, checks *workflow.Checks) error {
	checksBlobName := checksBlobName(planID, checks.ID)
	exists, err := r.blobExists(ctx, containerName, checksBlobName)
	if err != nil {
		return err
	}

	if !exists {
		if err := c.uploadChecksBlob(ctx, containerName, planID, checks); err != nil {
			return fmt.Errorf("failed to recreate checks blob: %w", err)
		}
	}

	for i, action := range checks.Actions {
		if err := r.ensureActionBlob(ctx, c, containerName, planID, action, i); err != nil {
			return err
		}
	}

	return nil
}

// ensureActionBlob ensures an action blob exists.
func (r recovery) ensureActionBlob(ctx context.Context, c creator, containerName string, planID uuid.UUID, action *workflow.Action, pos int) error {
	actionBlobName := actionBlobName(planID, action.ID)
	exists, err := r.blobExists(ctx, containerName, actionBlobName)
	if err != nil {
		return err
	}

	// If blob doesn't exist, create it
	if !exists {
		if err := c.uploadActionBlob(ctx, containerName, planID, action, pos); err != nil {
			return fmt.Errorf("failed to recreate action blob: %w", err)
		}
	}

	return nil
}

// blobExists checks if a blob exists in a container.
func (r recovery) blobExists(ctx context.Context, containerName, blobName string) (bool, error) {
	blobClient := r.reader.client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	_, err := blobClient.GetProperties(ctx, nil)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}
