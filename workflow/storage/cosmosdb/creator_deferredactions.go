package cosmosdb

import (
	"fmt"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

func deferredActionsToItems(iCtx *itemsContext, da *workflow.DeferredActions) error {
	if da == nil {
		return nil
	}

	entry, err := deferredActionsToEntry(iCtx, da)
	if err != nil {
		return err
	}

	for i, b := range da.DeferredBatches {
		if err := deferBatchToItems(iCtx, da.ID, i, b); err != nil {
			return fmt.Errorf("deferredActionsToItems(batch %d): %w", i, err)
		}
	}

	item, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal DeferredActions item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[da.ID.String()] = item
	return nil
}

func deferredActionsToEntry(iCtx *itemsContext, da *workflow.DeferredActions) (deferredActionsEntry, error) {
	batches, err := objsToIDs(da.DeferredBatches)
	if err != nil {
		return deferredActionsEntry{}, fmt.Errorf("objsToIDs(deferredBatches): %w", err)
	}
	return deferredActionsEntry{
		PartitionKey:    keyStr(iCtx.planID),
		Swarm:           iCtx.swarm,
		Type:            workflow.OTDeferredActions,
		ID:              da.ID,
		PlanID:          iCtx.planID,
		DeferredBatches: batches,
		StateStatus:     da.State.Get().Status,
		StateStart:      da.State.Get().Start,
		StateEnd:        da.State.Get().End,
	}, nil
}

func deferBatchToItems(iCtx *itemsContext, daID uuid.UUID, pos int, b *workflow.DeferBatch) error {
	if b == nil {
		return fmt.Errorf("deferBatchToItems: batch cannot be nil")
	}

	entry, err := deferBatchToEntry(iCtx, daID, pos, b)
	if err != nil {
		return err
	}

	for i, a := range b.Actions {
		if err := actionToItems(iCtx, i, a); err != nil {
			return fmt.Errorf("deferBatchToItems(actionToItems): %w", err)
		}
	}

	item, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal DeferBatch item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[b.ID.String()] = item
	return nil
}

func deferBatchToEntry(iCtx *itemsContext, daID uuid.UUID, pos int, b *workflow.DeferBatch) (deferBatchesEntry, error) {
	actions, err := objsToIDs(b.Actions)
	if err != nil {
		return deferBatchesEntry{}, fmt.Errorf("objsToIDs(actions): %w", err)
	}
	return deferBatchesEntry{
		PartitionKey:      keyStr(iCtx.planID),
		Swarm:             iCtx.swarm,
		Type:              workflow.OTBatch,
		ID:                b.ID,
		Key:               b.Key,
		PlanID:            iCtx.planID,
		DeferredActionsID: daID,
		When:              b.When,
		Pos:               pos,
		FailElement:       b.FailElement,
		Name:              b.Name,
		Descr:             b.Descr,
		Actions:           actions,
		StateStatus:       b.State.Get().Status,
		StateStart:        b.State.Get().Start,
		StateEnd:          b.State.Get().End,
	}, nil
}
