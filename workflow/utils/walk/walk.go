// Package walk provides a way to walk a workflow.Plan for all objects under it.
package walk

import (
	"context"

	"github.com/element-of-surprise/workstream/workflow"
)

// Item represents an object in the workflow and the chain of objects that led
// to it. Based on calling Value.Type(), you can call the appropriate method to
// get the object without using reflection.
type Item struct {
	// Chain is the chain of objects that led to this object. This will be empty
	// for the Plan object. While modifying an Object in the chain is fine,
	// the slice itself should not be modified. Otherwise the results will be
	// unpredictable.
	Chain []workflow.Object
	// Value is the value of the Item.
	Value workflow.Object
}

// IsZero returns true if the Item is the zero value.
func (i Item) IsZero() bool {
	return i.Value == nil && i.Chain == nil
}

// Plan returns the Value as a *workflow.Plan. If the object is not a Plan, this
// will panic.
func (i Item) Plan() *workflow.Plan {
	return i.Value.(*workflow.Plan)
}

// PreCheck returns the Value as a *workflow.PreCheck. If the object is not a
// PreCheck, this will panic.
func (i Item) Checks() *workflow.Checks {
	return i.Value.(*workflow.Checks)
}

// Block returns the Value as a *workflow.Block. If the object is not a Block,
// this will panic.
func (i Item) Block() *workflow.Block {
	return i.Value.(*workflow.Block)
}

// Sequence returns the Value as a *workflow.Sequence. If the object is not a
// Sequence, this will panic.
func (i Item) Sequence() *workflow.Sequence {
	return i.Value.(*workflow.Sequence)
}

// Action returns the Value as a *workflow.Action. If the object is not an
// Action, this will panic.
func (i Item) Action() *workflow.Action {
	return i.Value.(*workflow.Action)
}

// Plan walks a *workflow.Plan for all objects in call order and emits the in the returned channel.
// If the Context is canceled, the channel will be closed.
func Plan(ctx context.Context, p *workflow.Plan) chan Item {
	if p == nil {
		ch := make(chan Item)
		close(ch)
		return ch
	}

	ch := make(chan Item, 1)
	go func() {
		defer close(ch)

		i := Item{Value: p}
		if ok := emit(ctx, ch, i); !ok {
			return
		}

		chain := []workflow.Object{p}
		if p.PreChecks != nil {
			if ok := walkChecks(ctx, ch, chain, p.PreChecks); !ok {
				return
			}
		}
		if p.ContChecks != nil {
			if ok := walkChecks(ctx, ch, chain, p.ContChecks); !ok {
				return
			}
		}
		if p.Blocks != nil {
			for _, block := range p.Blocks {
				if ok := walkBlock(ctx, ch, chain, block); !ok {
					return
				}
			}
		}
		if p.PostChecks != nil {
			if ok := walkChecks(ctx, ch, chain, p.PostChecks); !ok {
				return
			}
		}
	}()
	return ch
}

func walkChecks(ctx context.Context, ch chan Item, chain []workflow.Object, checks *workflow.Checks) (ok bool) {
	i := Item{Chain: chain, Value: checks}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, checks)
	if checks.Actions != nil {
		for _, action := range checks.Actions {
			if ok := emit(ctx, ch, Item{Chain: chain, Value: action}); !ok {
				return false
			}
		}
	}
	return true
}

func walkBlock(ctx context.Context, ch chan Item, chain []workflow.Object, block *workflow.Block) (ok bool) {
	i := Item{Chain: chain, Value: block}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, block)
	if block.PreChecks != nil {
		if ok := walkChecks(ctx, ch, chain, block.PreChecks); !ok {
			return false
		}
	}
	if block.ContChecks != nil {
		if ok := walkChecks(ctx, ch, chain, block.ContChecks); !ok {
			return false
		}
	}

	if block.Sequences != nil {
		for _, sequence := range block.Sequences {
			if ok := walkSequence(ctx, ch, chain, sequence); !ok {
				return false
			}
		}
	}
	if block.PostChecks != nil {
		if ok := walkChecks(ctx, ch, chain, block.PostChecks); !ok {
			return false
		}
	}
	return true
}

func walkSequence(ctx context.Context, ch chan Item, chain []workflow.Object, sequence *workflow.Sequence) (ok bool) {
	i := Item{Chain: chain, Value: sequence}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, sequence)
	if sequence.Actions != nil {
		for _, action := range sequence.Actions {
			if ok := emit(ctx, ch, Item{Chain: chain, Value: action}); !ok {
				return false
			}
		}
	}
	return true
}

// emit emits an Item to the channel unless the channel is blocke and the Context is canceled.
// If the Context is canceled, emit returns false.
func emit(ctx context.Context, ch chan Item, i Item) (ok bool) {
	select {
	case <-ctx.Done():
		return false
	case ch <- i:
		return true
	}
}
