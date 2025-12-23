package azblob

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

var _ storage.Deleter = deleter{}

// deleter implements the storage.Deleter interface.
type deleter struct {
	mu     *planlocks.Group
	prefix string
	client blobops.Ops
	reader reader

	private.Storage
}

// Delete implements storage.Deleter.Delete(). It deletes all blobs associated with the plan:
// the plan blob and all sub-object blobs (blocks, sequences, checks, actions).
func (d deleter) Delete(ctx context.Context, id uuid.UUID) error {
	d.mu.Lock(id)
	defer d.mu.Unlock(id)

	// Read the plan to get full hierarchy
	v, err, _ := d.reader.readFlight.Do(
		id.String(),
		func() (any, error) {
			return d.reader.fetchPlan(ctx, id)
		},
	)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to read plan for deletion: %w", err))
	}
	plan := v.(*workflow.Plan)

	// Get the container for this plan based on its ID
	containerName := containerForPlan(d.prefix, id)

	if err := d.deletePlanInContainer(ctx, containerName, plan); err != nil {
		if !blobops.IsNotFound(err) {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageDelete, fmt.Errorf("failed to delete plan in container %s: %w", containerName, err))
		}
	}

	return nil
}

// deletePlanInContainer deletes all blobs for a plan in a specific container.
// This includes both the planEntry blob and the workflow.Plan object blob, plus all sub-objects.
func (d deleter) deletePlanInContainer(ctx context.Context, containerName string, plan *workflow.Plan) error {
	// Check if container exists
	exists, err := d.client.ContainerExists(ctx, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return nil // Container doesn't exist, nothing to delete
	}

	// Delete planEntry blob (lightweight, with metadata)
	entryBlob := planEntryBlobName(plan.ID)
	if err := d.deleteBlob(ctx, containerName, entryBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete planEntry blob: %w", err)
		}
	}

	// Delete all checks blobs
	for _, checks := range []*workflow.Checks{plan.BypassChecks, plan.PreChecks, plan.PostChecks, plan.ContChecks, plan.DeferredChecks} {
		if checks != nil {
			if err := d.deleteChecksBlobs(ctx, containerName, plan.ID, checks); err != nil {
				if !blobops.IsNotFound(err) {
					return err
				}
			}
		}
	}

	// Delete all block-related blobs
	for _, block := range plan.Blocks {
		if err := d.deleteBlockBlobs(ctx, containerName, plan.ID, block); err != nil {
			if !blobops.IsNotFound(err) {
				return err
			}
		}
	}

	// Delete workflow.Plan object blob (full embedded hierarchy)
	objectBlob := planObjectBlobName(plan.ID)
	if err := d.deleteBlob(ctx, containerName, objectBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete plan object blob: %w", err)
		}
	}

	return nil
}

// deleteBlockBlobs deletes all blobs for a block and its sub-objects.
func (d deleter) deleteBlockBlobs(ctx context.Context, containerName string, planID uuid.UUID, block *workflow.Block) error {
	// Delete block blob
	blockBlob := blockBlobName(planID, block.ID)
	if err := d.deleteBlob(ctx, containerName, blockBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete block blob: %w", err)
		}
	}

	// Delete block's checks
	for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
		if checks != nil {
			if err := d.deleteChecksBlobs(ctx, containerName, planID, checks); err != nil {
				return err
			}
		}
	}

	// Delete block's sequences
	for _, seq := range block.Sequences {
		if err := d.deleteSequenceBlobs(ctx, containerName, planID, seq); err != nil {
			return err
		}
	}

	return nil
}

// deleteSequenceBlobs deletes all blobs for a sequence and its actions.
func (d deleter) deleteSequenceBlobs(ctx context.Context, containerName string, planID uuid.UUID, seq *workflow.Sequence) error {
	// Delete sequence blob
	seqBlob := sequenceBlobName(planID, seq.ID)
	if err := d.deleteBlob(ctx, containerName, seqBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete sequence blob: %w", err)
		}
	}

	// Delete sequence's actions
	for _, action := range seq.Actions {
		if err := d.deleteActionBlob(ctx, containerName, planID, action.ID); err != nil {
			return err
		}
	}

	return nil
}

// deleteChecksBlobs deletes all blobs for a checks object and its actions.
func (d deleter) deleteChecksBlobs(ctx context.Context, containerName string, planID uuid.UUID, checks *workflow.Checks) error {
	// Delete checks blob
	checksBlob := checksBlobName(planID, checks.ID)
	if err := d.deleteBlob(ctx, containerName, checksBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete checks blob: %w", err)
		}
	}

	// Delete checks' actions
	for _, action := range checks.Actions {
		if err := d.deleteActionBlob(ctx, containerName, planID, action.ID); err != nil {
			return err
		}
	}

	return nil
}

// deleteActionBlob deletes a single action blob.
func (d deleter) deleteActionBlob(ctx context.Context, containerName string, planID, actionID uuid.UUID) error {
	actionBlob := actionBlobName(planID, actionID)
	if err := d.deleteBlob(ctx, containerName, actionBlob); err != nil {
		if !blobops.IsNotFound(err) {
			return fmt.Errorf("failed to delete action blob: %w", err)
		}
	}
	return nil
}

// deleteBlob deletes a single blob.
func (d deleter) deleteBlob(ctx context.Context, containerName, blobName string) error {
	return d.client.DeleteBlob(ctx, containerName, blobName)
}
