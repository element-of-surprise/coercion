package azblob

import (
	"fmt"
	"maps"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/worker"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

// uploader uploads a plan and its sub-objects to blob storage.
type uploader struct {
	mu     *planlocks.Group
	client blobops.Ops
	prefix string
	pool   *worker.Pool
}

type uploadPlanType uint8

const (
	uptUnknown uploadPlanType = iota
	uptCreate
	uptUpdate
	uptComplete
)

// uploadPlan uploads a plan and all its sub-objects to blob storage.
// Write order: planEntry (with metadata) → sub-objects → workflow.Plan object
func (u *uploader) uploadPlan(ctx context.Context, p *workflow.Plan, uploadPlanType uploadPlanType) error {
	if p == nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeParameter, fmt.Errorf("commitPlan: plan cannot be nil"))
	}
	if p.ID == uuid.Nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("commitPlan: plan ID cannot be nil"))
	}
	if uploadPlanType == uptUnknown {
		return errors.E(ctx, errors.CatInternal, errors.TypeParameter, fmt.Errorf("commitPlan: uploadPlanType cannot be unknown"))
	}

	containerName := containerForPlan(u.prefix, p.ID)

	if uploadPlanType == uptCreate {
		if err := u.client.EnsureContainer(ctx, containerName); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageCreate, fmt.Errorf("failed to create container: %w", err))
		}
	}

	md, err := planToMetadata(ctx, p)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to convert plan to metadata: %w", err))
	}

	if p.GroupID != uuid.Nil {
		md[mdKeyGroupID] = toPtr(p.GroupID.String())
	}

	// Do NOT attempt to make these uploads concurrent. This will screw up the ordering that is required to
	// do consistency checks since we don't have transactions.
	if err := u.uploadPlanEntry(ctx, p, md); err != nil {
		if uploadPlanType == uptCreate {
			_ = u.client.DeleteBlob(ctx, containerName, planObjectBlobName(p.ID))
		}
		return err
	}

	// Only upload sub-objects if plan is not started. This is called at the start of plan execution
	// and the end, so we only need to upload sub-objects once. At the end of execution we only need to update the
	// plan object.
	if uploadPlanType == uptCreate {
		if err := u.uploadSubObjects(ctx, containerName, p); err != nil {
			_ = u.client.DeleteBlob(ctx, containerName, planEntryBlobName(p.ID))
			return err
		}
	}

	if err := u.uploadPlanObject(ctx, p, md); err != nil {
		return err
	}

	return nil
}

// uploadPlanEntry uploads the planEntry blob for a plan (used for lightweight updates).
func (u *uploader) uploadPlanEntry(ctx context.Context, p *workflow.Plan, md map[string]*string) error {
	containerName := containerForPlan(u.prefix, p.ID)

	if err := u.client.EnsureContainer(ctx, containerName); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageCreate, fmt.Errorf("failed to create container: %w", err))
	}

	planEntry, err := planToPlanEntry(p)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	planEntryData, err := json.Marshal(planEntry)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal planEntry: %w", err))
	}

	entryBlobName := planEntryBlobName(p.ID)
	md = maps.Clone(md)
	md[mdPlanType] = toPtr(ptEntry)
	if err := u.client.UploadBlob(ctx, containerName, entryBlobName, md, planEntryData); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to upload planEntry blob: %w", err))
	}
	return nil
}

// uploadPlanObject uploads the full plan object blob for a plan.
func (u *uploader) uploadPlanObject(ctx context.Context, p *workflow.Plan, md map[string]*string) error {
	containerName := containerForPlan(u.prefix, p.ID)

	planObjectData, err := json.Marshal(p)
	if err != nil {
		_ = u.client.DeleteBlob(ctx, containerName, planEntryBlobName(p.ID))
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal plan object: %w", err))
	}

	objectBlobName := planObjectBlobName(p.ID)
	md = maps.Clone(md)
	md[mdPlanType] = toPtr(string(ptObject))
	if err := u.client.UploadBlob(ctx, containerName, objectBlobName, md, planObjectData); err != nil {
		_ = u.client.DeleteBlob(ctx, containerName, planEntryBlobName(p.ID))
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to upload plan object blob: %w", err))
	}
	return nil
}

// uploadSubObjects uploads all sub-object blobs for a plan.
func (u *uploader) uploadSubObjects(ctx context.Context, containerName string, p *workflow.Plan) error {
	g := u.pool.Group()

	for _, checks := range []*workflow.Checks{p.BypassChecks, p.PreChecks, p.PostChecks, p.ContChecks, p.DeferredChecks} {
		if ctx.Err() != nil {
			break
		}
		if checks != nil {
			g.Go(
				ctx,
				func(ctx context.Context) error {
					return u.uploadChecksBlob(ctx, containerName, p.ID, checks)
				},
			)
		}
	}

	for i, block := range p.Blocks {
		if ctx.Err() != nil {
			break
		}

		g.Go(
			ctx,
			func(ctx context.Context) error {
				return u.uploadBlockBlob(ctx, containerName, p.ID, block, i)
			},
		)
	}

	return g.Wait(ctx)
}

// uploadBlockBlob uploads a block blob and all its sub-objects.
func (u *uploader) uploadBlockBlob(ctx context.Context, containerName string, planID uuid.UUID, block *workflow.Block, pos int) error {
	blockEntry, err := blockToEntry(block, pos)
	if err != nil {
		return fmt.Errorf("failed to convert block to entry: %w", err)
	}

	blockData, err := json.Marshal(blockEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	g := u.pool.Group()

	blockBlobName := blockBlobName(planID, block.ID)
	g.Go(
		ctx,
		func(ctx context.Context) error {
			if err := u.client.UploadBlob(ctx, containerName, blockBlobName, nil, blockData); err != nil {
				return fmt.Errorf("failed to upload block blob: %w", err)
			}
			return nil
		},
	)

	for _, checks := range []*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
		if ctx.Err() != nil {
			break
		}
		if checks != nil {
			g.Go(
				ctx,
				func(ctx context.Context) error {
					return u.uploadChecksBlob(ctx, containerName, planID, checks)
				},
			)
		}
	}

	for i, seq := range block.Sequences {
		g.Go(
			ctx,
			func(ctx context.Context) error {
				return u.uploadSequenceBlob(ctx, containerName, planID, seq, i)
			},
		)
	}

	return g.Wait(ctx)
}

// uploadSequenceBlob uploads a sequence blob and all its actions.
func (u *uploader) uploadSequenceBlob(ctx context.Context, containerName string, planID uuid.UUID, seq *workflow.Sequence, pos int) error {
	seqEntry, err := sequenceToEntry(seq, pos)
	if err != nil {
		return fmt.Errorf("failed to convert sequence to entry: %w", err)
	}

	seqData, err := json.Marshal(seqEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal sequence: %w", err)
	}

	g := u.pool.Group()

	seqBlobName := sequenceBlobName(planID, seq.ID)
	g.Go(
		ctx,
		func(ctx context.Context) error {
			if err := u.client.UploadBlob(ctx, containerName, seqBlobName, nil, seqData); err != nil {
				return fmt.Errorf("failed to upload sequence blob: %w", err)
			}
			return nil
		},
	)

	for i, action := range seq.Actions {
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

// uploadChecksBlob uploads a checks blob and all its actions.
func (u *uploader) uploadChecksBlob(ctx context.Context, containerName string, planID uuid.UUID, checks *workflow.Checks) error {
	checksEntry, err := checksToEntry(checks)
	if err != nil {
		return fmt.Errorf("failed to convert checks to entry: %w", err)
	}

	checksData, err := json.Marshal(checksEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal checks: %w", err)
	}

	g := u.pool.Group()

	checksBlobName := checksBlobName(planID, checks.ID)
	g.Go(
		ctx,
		func(ctx context.Context) error {
			if err := u.client.UploadBlob(ctx, containerName, checksBlobName, nil, checksData); err != nil {
				return fmt.Errorf("failed to upload checks blob: %w", err)
			}
			return nil
		},
	)

	for i, action := range checks.Actions {
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

// uploadActionBlob uploads a single action blob.
func (u *uploader) uploadActionBlob(ctx context.Context, containerName string, planID uuid.UUID, action *workflow.Action, pos int) error {
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
	if err := u.client.UploadBlob(ctx, containerName, actionBlobName, nil, actionData); err != nil {
		return fmt.Errorf("failed to upload action blob: %w", err)
	}

	return nil
}
