package cosmosdb

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"github.com/gostdlib/ops/retry/exponential"
)

type deleter struct {
	mu *sync.Mutex
	Client

	reader reader
}

// consider using transactional batch here instead of updating everything separately.

// Delete deletes a plan with "id" from the storage.
func (d deleter) Delete(ctx context.Context, id uuid.UUID) error {
	plan, err := d.reader.Read(ctx, id)
	if err != nil {
		return fmt.Errorf("couldn't fetch plan: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	itemOpt := &azcosmos.TransactionalBatchItemOptions{
		// EnableContentResponseOnWrite: true,
	}

	// need to be super careful about retriable and permanent errors.
	deletePlan := func(ctx context.Context, r exponential.Record) error {
		if err := d.deletePlan(ctx, plan, itemOpt); err != nil {
			if r.Attempt >= 5 {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(ctx, deletePlan); err != nil {
		return fmt.Errorf("couldn't delete plan: %w", err)
	}

	return nil
}

func (d deleter) deletePlan(ctx context.Context, plan *workflow.Plan, itemOpt *azcosmos.TransactionalBatchItemOptions) error {
	batch := d.NewTransactionalBatch()

	if err := d.deleteChecks(ctx, batch, plan.BypassChecks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete plan bypasschecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.PreChecks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete plan prechecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.PostChecks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete plan postchecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.ContChecks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete plan contchecks: %w", err)
	}
	if err := d.deleteChecks(ctx, batch, plan.DeferredChecks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete plan deferredchecks: %w", err)
	}
	if err := d.deleteBlocks(ctx, batch, plan.Blocks, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete blocks: %w", err)
	}

	// Do I care about etag here? I don't have it for the other items unless I read.
	// Once we're at the point where a plan needs to be deleted, I assume it's completed (whether failed or successful)
	// and no important operations will take place on this other than to delete.
	var ifMatchEtag *azcore.ETag = nil
	if plan.State.ETag != "" {
		ifMatchEtag = (*azcore.ETag)(&plan.State.ETag)
	}
	itemOpt.IfMatchETag = ifMatchEtag
	batch.DeleteItem(plan.ID.String(), itemOpt)
	d.SetBatch(batch) // for testing

	if _, err := d.ExecuteTransactionalBatch(ctx, batch, nil); err != nil {
		return fmt.Errorf("failed to delete plan through Cosmos DB API: %w", err)
	}

	return nil
}

func (d deleter) deleteBlocks(ctx context.Context, batch TransactionalBatch, blocks []*workflow.Block, itemOpt *azcosmos.TransactionalBatchItemOptions) error {
	if len(blocks) == 0 {
		return nil
	}

	for _, block := range blocks {
		if err := d.deleteChecks(ctx, batch, block.BypassChecks, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block bypasschecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.PreChecks, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block prechecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.PostChecks, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block postchecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.ContChecks, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block contchecks: %w", err)
		}
		if err := d.deleteChecks(ctx, batch, block.DeferredChecks, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block deferredchecks: %w", err)
		}
		if err := d.deleteSeqs(ctx, batch, block.Sequences, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete block sequences: %w", err)
		}
	}

	for _, block := range blocks {
		batch.DeleteItem(block.ID.String(), itemOpt)
	}
	return nil
}

func (d deleter) deleteChecks(ctx context.Context, batch TransactionalBatch, checks *workflow.Checks, itemOpt *azcosmos.TransactionalBatchItemOptions) error {
	if checks == nil {
		return nil
	}

	if err := d.deleteActions(ctx, batch, checks.Actions, itemOpt); err != nil {
		return fmt.Errorf("couldn't delete checks actions: %w", err)
	}

	batch.DeleteItem(checks.ID.String(), itemOpt)

	return nil
}

func (d deleter) deleteSeqs(ctx context.Context, batch TransactionalBatch, seqs []*workflow.Sequence, itemOpt *azcosmos.TransactionalBatchItemOptions) error {
	if len(seqs) == 0 {
		return nil
	}

	for _, seq := range seqs {
		if err := d.deleteActions(ctx, batch, seq.Actions, itemOpt); err != nil {
			return fmt.Errorf("couldn't delete sequence actions: %w", err)
		}
	}

	for _, seq := range seqs {
		batch.DeleteItem(seq.ID.String(), itemOpt)
	}
	return nil
}

func (d deleter) deleteActions(ctx context.Context, batch TransactionalBatch, actions []*workflow.Action, itemOpt *azcosmos.TransactionalBatchItemOptions) error {
	if len(actions) == 0 {
		return nil
	}

	for _, action := range actions {
		batch.DeleteItem(action.ID.String(), itemOpt)
	}
	return nil
}
