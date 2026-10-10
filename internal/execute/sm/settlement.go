package sm

import (
	"fmt"
	"iter"
	"slices"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// recoveryObjects yields the objects below the Plan for settlement, except the deferred work in keep that will resume
// and everything under it. Terminal deferred containers are not kept, so they are still settled: their remaining
// children will not execute even when other deferred work remains.
func recoveryObjects(plan *workflow.Plan, keep ...workflow.Object) iter.Seq[walk.Item] {
	kept := func(item walk.Item) bool {
		for _, k := range keep {
			if item.Value == k || slices.Contains(item.Chain, k) {
				return true
			}
		}
		return false
	}
	return func(yield func(walk.Item) bool) {
		for item := range walk.Plan(plan, walk.WithSkipPlan()) {
			if kept(item) {
				continue
			}
			if !yield(item) {
				return
			}
		}
	}
}

// failSequences settles abandoned recovered work before a failed Block can be persisted. Launched sequences must
// have been joined first. Checks are excluded: their execution and shutdown are handled by the calling states.
func (s *States) failSequences(ctx context.Context, b *workflow.Block) error {
	items := func(yield func(walk.Item) bool) {
		for _, seq := range b.Sequences {
			if !yield(walk.Item{Value: seq}) {
				return
			}
			for _, action := range seq.Actions {
				if !yield(walk.Item{Value: action}) {
					return
				}
			}
		}
	}
	for _, item := range walk.SettleRunningItems(items, workflow.Failed, s.now()) {
		if err := storage.WriteObject(ctx, s.store, item); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("settling block(%s) sequences: %w", b.ID, err))
		}
	}
	return nil
}
