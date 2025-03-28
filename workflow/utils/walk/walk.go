// Package walk provides a way to walk a workflow.Plan for all objects under it.
package walk

import (
	"iter"

	"github.com/element-of-surprise/coercion/workflow"
)

// Item represents an object in the workflow and the chain of objects that led
// to it. Based on calling Value.Type(), you can call the appropriate method to
// get the object without using reflection.
type Item struct {
	// Value is the value of the Item.
	Value workflow.Object
	// Chain is the chain of objects that led to this object. This will be empty
	// for the Plan object. While modifying an Object in the chain is fine,
	// the slice itself should not be modified. Otherwise the results will be
	// unpredictable.
	Chain []workflow.Object
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

// Checks returns the Value as a *workflow.Checks. If the object is not a
// Checks, this will panic.
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

// Plan walks a *workflow.Plan for all objects in call order.
func Plan(p *workflow.Plan) iter.Seq[Item] {
	return func(yield func(Item) bool) {
		if !yield(Item{Value: p}) {
			return
		}
		chain := []workflow.Object{p}
		if p.BypassChecks != nil {
			if ok := walkChecks(yield, chain, p.BypassChecks); !ok {
				return
			}
		}
		if p.PreChecks != nil {
			if ok := walkChecks(yield, chain, p.PreChecks); !ok {
				return
			}
		}
		if p.ContChecks != nil {
			if ok := walkChecks(yield, chain, p.ContChecks); !ok {
				return
			}
		}
		if p.Blocks != nil {
			for _, block := range p.Blocks {
				if ok := walkBlock(yield, chain, block); !ok {
					return
				}
			}
		}
		if p.PostChecks != nil {
			if ok := walkChecks(yield, chain, p.PostChecks); !ok {
				return
			}
		}
		if p.DeferredChecks != nil {
			if ok := walkChecks(yield, chain, p.DeferredChecks); !ok {
				return
			}
		}
	}
}

func walkChecks(yield func(Item) bool, chain []workflow.Object, checks *workflow.Checks) bool {
	i := Item{Chain: chain, Value: checks}

	if !yield(i) {
		return false
	}

	chain = append(chain, checks)
	if checks.Actions != nil {
		for _, action := range checks.Actions {
			if !yield(Item{Chain: chain, Value: action}) {
				return false
			}
		}
	}
	return true
}

func walkBlock(yield func(Item) bool, chain []workflow.Object, block *workflow.Block) (ok bool) {
	i := Item{Chain: chain, Value: block}

	if !yield(i) {
		return false
	}

	chain = append(chain, block)
	if block.BypassChecks != nil {
		if !walkChecks(yield, chain, block.BypassChecks) {
			return false
		}
	}
	if block.PreChecks != nil {
		if !walkChecks(yield, chain, block.PreChecks) {
			return false
		}
	}
	if block.ContChecks != nil {
		if !walkChecks(yield, chain, block.ContChecks) {
			return false
		}
	}

	if block.Sequences != nil {
		for _, sequence := range block.Sequences {
			if !walkSequence(yield, chain, sequence) {
				return false
			}
		}
	}
	if block.PostChecks != nil {
		if !walkChecks(yield, chain, block.PostChecks) {
			return false
		}
	}
	if block.DeferredChecks != nil {
		if !walkChecks(yield, chain, block.DeferredChecks) {
			return false
		}
	}
	return true
}

func walkSequence(yield func(Item) bool, chain []workflow.Object, sequence *workflow.Sequence) (ok bool) {
	i := Item{Chain: chain, Value: sequence}
	if !yield(i) {
		return false
	}

	chain = append(chain, sequence)
	if sequence.Actions != nil {
		for _, action := range sequence.Actions {
			if !yield(Item{Chain: chain, Value: action}) {
				return false
			}
		}
	}
	return true
}
