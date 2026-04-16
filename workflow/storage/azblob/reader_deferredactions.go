package azblob

import (
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
)

// fetchDeferredActions downloads a DeferredActions object and all its DeferBatches.
func (r reader) fetchDeferredActions(ctx context.Context, containerName string, planID, daID uuid.UUID) (*workflow.DeferredActions, error) {
	blobName := deferredActionsBlobName(planID, daID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download DeferredActions blob: %w", err))
	}

	var entry deferredActionsEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal DeferredActions: %w", err))
	}

	da := entryToDeferredActions(entry)
	da.SetPlanID(planID)

	da.DeferredBatches = make([]*workflow.DeferBatch, len(entry.DeferredBatches))
	for i, id := range entry.DeferredBatches {
		da.DeferredBatches[i], err = r.fetchDeferBatch(ctx, containerName, planID, id)
		if err != nil {
			return nil, err
		}
	}
	return da, nil
}

// fetchDeferBatch downloads a DeferBatch object and all its actions.
func (r reader) fetchDeferBatch(ctx context.Context, containerName string, planID, batchID uuid.UUID) (*workflow.DeferBatch, error) {
	blobName := deferBatchBlobName(planID, batchID)
	data, err := r.client.GetBlob(ctx, containerName, blobName)
	if err != nil {
		if blobops.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to download DeferBatch blob: %w", err))
	}

	var entry deferBatchesEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal DeferBatch: %w", err))
	}

	b := entryToDeferBatch(entry)
	b.SetPlanID(planID)

	b.Actions = make([]*workflow.Action, len(entry.Actions))
	for i, aid := range entry.Actions {
		b.Actions[i], err = r.fetchAction(ctx, containerName, planID, aid)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}
