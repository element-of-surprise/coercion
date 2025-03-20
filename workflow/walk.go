package workflow

// This file exists because of a cyclic dependency between the walk package and the workflow package.
// For the moment, this is simply a copy of the walk.go file from the walk package with some minor changes for
// private access to the walk functions.

import "context"

// item represents an object in the workflow and the chain of objects that led
// to it. Based on calling Value.Type(), you can call the appropriate method to
// get the object without using reflection.
type item struct {
	// Value is the value of the Item.
	Value Object
	// Chain is the chain of objects that led to this object. This will be empty
	// for the Plan object. While modifying an Object in the chain is fine,
	// the slice itself should not be modified. Otherwise the results will be
	// unpredictable.
	Chain []Object
}

// IsZero returns true if the Item is the zero value.
func (i item) IsZero() bool {
	return i.Value == nil && i.Chain == nil
}

// Plan returns the Value as a *Plan. If the object is not a Plan, this
// will panic.
func (i item) Plan() *Plan {
	return i.Value.(*Plan)
}

// Checks returns the Value as a *Checks. If the object is not a
// Checks, this will panic.
func (i item) Checks() *Checks {
	return i.Value.(*Checks)
}

// Block returns the Value as a *Block. If the object is not a Block,
// this will panic.
func (i item) Block() *Block {
	return i.Value.(*Block)
}

// Sequence returns the Value as a *Sequence. If the object is not a
// Sequence, this will panic.
func (i item) Sequence() *Sequence {
	return i.Value.(*Sequence)
}

// Action returns the Value as a *Action. If the object is not an
// Action, this will panic.
func (i item) Action() *Action {
	return i.Value.(*Action)
}

// walkPlan walks a *Plan for all objects in call order and emits the in the returned channel.
// If the Context is canceled, the channel will be closed.
func walkPlan(ctx context.Context, p *Plan) chan item {
	if p == nil {
		ch := make(chan item)
		close(ch)
		return ch
	}

	ch := make(chan item, 1)
	go func() {
		defer close(ch)

		i := item{Value: p}
		if ok := emit(ctx, ch, i); !ok {
			return
		}

		chain := []Object{p}
		if p.BypassChecks != nil {
			if ok := walkChecks(ctx, ch, chain, p.BypassChecks); !ok {
				return
			}
		}
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
		if p.DeferredChecks != nil {
			if ok := walkChecks(ctx, ch, chain, p.DeferredChecks); !ok {
				return
			}
		}
	}()
	return ch
}

func walkChecks(ctx context.Context, ch chan item, chain []Object, checks *Checks) (ok bool) {
	i := item{Chain: chain, Value: checks}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, checks)
	if checks.Actions != nil {
		for _, action := range checks.Actions {
			if ok := emit(ctx, ch, item{Chain: chain, Value: action}); !ok {
				return false
			}
		}
	}
	return true
}

func walkBlock(ctx context.Context, ch chan item, chain []Object, block *Block) (ok bool) {
	i := item{Chain: chain, Value: block}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, block)
	if block.BypassChecks != nil {
		if ok := walkChecks(ctx, ch, chain, block.BypassChecks); !ok {
			return false
		}
	}
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
	if block.DeferredChecks != nil {
		if ok := walkChecks(ctx, ch, chain, block.DeferredChecks); !ok {
			return false
		}
	}
	return true
}

func walkSequence(ctx context.Context, ch chan item, chain []Object, sequence *Sequence) (ok bool) {
	i := item{Chain: chain, Value: sequence}
	if ok := emit(ctx, ch, i); !ok {
		return false
	}

	chain = append(chain, sequence)
	if sequence.Actions != nil {
		for _, action := range sequence.Actions {
			if ok := emit(ctx, ch, item{Chain: chain, Value: action}); !ok {
				return false
			}
		}
	}
	return true
}

// emit emits an Item to the channel unless the channel is blocke and the Context is canceled.
// If the Context is canceled, emit returns false.
func emit(ctx context.Context, ch chan item, i item) (ok bool) {
	select {
	case <-ctx.Done():
		return false
	case ch <- i:
		return true
	}
}
