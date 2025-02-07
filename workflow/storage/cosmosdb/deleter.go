package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/Azure/retry/exponential"
	"github.com/google/uuid"
)

type deleter struct {
	mu *sync.RWMutex
	client

	reader reader
}

// Delete deletes a plan with "id" from the storage.
func (d deleter) Delete(ctx context.Context, id uuid.UUID) error {
	plan, err := d.reader.Read(ctx, id)
	if err != nil {
		return fmt.Errorf("couldn't fetch plan: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	deletePlan := func(ctx context.Context, r exponential.Record) error {
		if err := d.deletePlan(ctx, plan); err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), deletePlan); err != nil {
		return fmt.Errorf("couldn't delete plan: %w", err)
	}

	return nil
}

func (d deleter) deletePlan(ctx context.Context, plan *workflow.Plan) error {
	batch := d.newTransactionalBatch()

	if err := d.deleteChecks(ctx, batch, plan.BypassChecks); err != nil {
		return fmt.Errorf("couldn't delete plan bypasschecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.PreChecks); err != nil {
		return fmt.Errorf("couldn't delete plan prechecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.PostChecks); err != nil {
		return fmt.Errorf("couldn't delete plan postchecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.ContChecks); err != nil {
		return fmt.Errorf("couldn't delete plan contchecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.DeferredChecks); err != nil {
		return fmt.Errorf("couldn't delete plan deferredchecks: %w", err)
	}
	if err := d.deleteBlocks(ctx, batch, plan.Blocks); err != nil {
		return fmt.Errorf("couldn't delete blocks: %w", err)
	}

	var ifMatchEtag *azcore.ETag = nil
	if plan.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&plan.State.ETag)
	}
	itemOpt := &azcosmos.TransactionalBatchItemOptions{
		IfMatchETag: ifMatchEtag,
	}
	batch.DeleteItem(plan.ID.String(), itemOpt)
	d.setBatch(batch) // for testing

	if _, err := d.executeTransactionalBatch(ctx, batch, nil); err != nil {
		return fmt.Errorf("failed to delete plan through Cosmos DB API: %w", err)
	}

	return nil
}

func (d deleter) deleteBlocks(ctx context.Context, batch transactionalBatch, blocks []*workflow.Block) error {
	if len(blocks) == 0 {
		return nil
	}

	for _, block := range blocks {
		if err := d.deleteChecks(ctx, batch, block.BypassChecks); err != nil {
			return fmt.Errorf("couldn't delete block bypasschecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.PreChecks); err != nil {
			return fmt.Errorf("couldn't delete block prechecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.PostChecks); err != nil {
			return fmt.Errorf("couldn't delete block postchecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.ContChecks); err != nil {
			return fmt.Errorf("couldn't delete block contchecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.DeferredChecks); err != nil {
			return fmt.Errorf("couldn't delete block deferredchecks: %w", err)
		}
		if err := d.deleteSeqs(ctx, batch, block.Sequences); err != nil {
			return fmt.Errorf("couldn't delete block sequences: %w", err)
		}
	}

	for _, block := range blocks {
		var ifMatchEtag *azcore.ETag = nil
		if block.State.ETag != "" {
			ifMatchEtag = (*azcore.ETag)(&block.State.ETag)
		}
		itemOpt := &azcosmos.TransactionalBatchItemOptions{
			IfMatchETag: ifMatchEtag,
		}

		batch.DeleteItem(block.ID.String(), itemOpt)
	}
	return nil
}

func (d deleter) deleteChecks(ctx context.Context, batch transactionalBatch, checks *workflow.Checks) error {
	if checks == nil {
		return nil
	}

	if err := d.deleteActions(ctx, batch, checks.Actions); err != nil {
		return fmt.Errorf("couldn't delete checks actions: %w", err)
	}

	var ifMatchEtag *azcore.ETag = nil
	if checks.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&checks.State.ETag)
	}
	itemOpt := &azcosmos.TransactionalBatchItemOptions{
		IfMatchETag: ifMatchEtag,
	}

	batch.DeleteItem(checks.ID.String(), itemOpt)

	return nil
}

func (d deleter) deleteSeqs(ctx context.Context, batch transactionalBatch, seqs []*workflow.Sequence) error {
	if len(seqs) == 0 {
		return nil
	}

	for _, seq := range seqs {
		if err := d.deleteActions(ctx, batch, seq.Actions); err != nil {
			return fmt.Errorf("couldn't delete sequence actions: %w", err)
		}
	}

	for _, seq := range seqs {
		var ifMatchEtag *azcore.ETag = nil
		if seq.State.ETag != "" {
			ifMatchEtag = (*azcore.ETag)(&seq.State.ETag)
		}
		itemOpt := &azcosmos.TransactionalBatchItemOptions{
			IfMatchETag: ifMatchEtag,
		}

		batch.DeleteItem(seq.ID.String(), itemOpt)
	}
	return nil
}

func (d deleter) deleteActions(ctx context.Context, batch transactionalBatch, actions []*workflow.Action) error {
	if len(actions) == 0 {
		return nil
	}

	for _, action := range actions {
		var ifMatchEtag *azcore.ETag = nil
		if action.State.ETag != "" {
			ifMatchEtag = (*azcore.ETag)(&action.State.ETag)
		}
		itemOpt := &azcosmos.TransactionalBatchItemOptions{
			IfMatchETag: ifMatchEtag,
		}

		batch.DeleteItem(action.ID.String(), itemOpt)
	}
	return nil
}
