package azblob

import (
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
)

// uploadDeferredActionsBlob uploads a DeferredActions blob and all its DeferBatch sub-objects.
func (u *uploader) uploadDeferredActionsBlob(ctx context.Context, containerName string, planID uuid.UUID, da *workflow.DeferredActions) error {
	entry, err := deferredActionsToEntry(da)
	if err != nil {
		return fmt.Errorf("failed to convert DeferredActions to entry: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal DeferredActions: %w", err)
	}

	g := u.blockPool.Group()

	blobName := deferredActionsBlobName(planID, da.ID)
	g.Go(
		ctx,
		func(ctx context.Context) error {
			if err := u.client.UploadBlob(ctx, containerName, blobName, nil, data); err != nil {
				return fmt.Errorf("failed to upload DeferredActions blob: %w", err)
			}
			return nil
		},
	)

	for _, batch := range da.OnFailure {
		if ctx.Err() != nil {
			break
		}
		b := batch
		g.Go(
			ctx,
			func(ctx context.Context) error {
				return u.uploadDeferBatchBlob(ctx, containerName, planID, b)
			},
		)
	}
	for _, batch := range da.OnSuccess {
		if ctx.Err() != nil {
			break
		}
		b := batch
		g.Go(
			ctx,
			func(ctx context.Context) error {
				return u.uploadDeferBatchBlob(ctx, containerName, planID, b)
			},
		)
	}
	return g.Wait(ctx)
}

// uploadDeferBatchBlob uploads a DeferBatch blob and all its action sub-objects.
func (u *uploader) uploadDeferBatchBlob(ctx context.Context, containerName string, planID uuid.UUID, batch *workflow.DeferBatch) error {
	entry, err := deferBatchToEntry(batch)
	if err != nil {
		return fmt.Errorf("failed to convert DeferBatch to entry: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal DeferBatch: %w", err)
	}

	g := u.leafObjPool.Group()

	blobName := deferBatchBlobName(planID, batch.ID)
	g.Go(
		ctx,
		func(ctx context.Context) error {
			if err := u.client.UploadBlob(ctx, containerName, blobName, nil, data); err != nil {
				return fmt.Errorf("failed to upload DeferBatch blob: %w", err)
			}
			return nil
		},
	)

	for i, action := range batch.Actions {
		if ctx.Err() != nil {
			break
		}
		g.Go(
			ctx,
			func(ctx context.Context) error {
				return u.uploadActionBlob(ctx, containerName, planID, action, i)
			},
		)
	}

	return g.Wait(ctx)
}
