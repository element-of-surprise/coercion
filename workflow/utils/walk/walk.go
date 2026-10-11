// Package walk provides a way to walk a workflow.Plan for all objects under it.
package walk

import (
	"iter"
	"slices"
	"time"

	"github.com/gostdlib/base/context"

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

// DeferredActions returns the Value as a *workflow.DeferredActions. If the
// object is not a DeferredActions, this will panic.
func (i Item) DeferredActions() *workflow.DeferredActions {
	return i.Value.(*workflow.DeferredActions)
}

// DeferBatch returns the Value as a *workflow.DeferBatch. If the object is
// not a DeferBatch, this will panic.
func (i Item) DeferBatch() *workflow.DeferBatch {
	return i.Value.(*workflow.DeferBatch)
}

type planOptions struct {
	skipPlan bool
	reverse  bool
}

// PlanOption modifies how Plan walks a *workflow.Plan. Options are applied in order, so later options override earlier
// ones. All options are optional.
type PlanOption func(p planOptions) planOptions

// WithSkipPlan returns a PlanOption that causes Plan to skip yielding the *workflow.Plan itself,
// and only yield the objects under it.
func WithSkipPlan() PlanOption {
	return func(p planOptions) planOptions {
		p.skipPlan = true
		return p
	}
}

// WithReverseOrder returns a PlanOption that causes Plan to yield the objects in reverse order. The output is the same
// as calling slices.Reverse() on slices.Collect() of the forward walk, but is produced lazily, so stopping early does
// not walk the rest of the Plan. Each Item's Chain is the same as in the forward walk.
func WithReverseOrder() PlanOption {
	return func(p planOptions) planOptions {
		p.reverse = true
		return p
	}
}

// Plan walks a *workflow.Plan for all objects in call order. By default the Plan itself is yielded first, followed by
// every object under it. See WithSkipPlan() and WithReverseOrder() to change this.
func Plan(p *workflow.Plan, options ...PlanOption) iter.Seq[Item] {
	opts := planOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	return func(yield func(Item) bool) {
		if opts.reverse {
			walkPlanReverse(yield, p, opts.skipPlan)
			return
		}
		walkPlan(yield, p, opts.skipPlan)
	}
}

func walkPlan(yield func(Item) bool, p *workflow.Plan, skipPlan bool) bool {
	if !skipPlan {
		if !yield(Item{Value: p}) {
			return false
		}
	}
	chain := []workflow.Object{p}
	if p.BypassChecks != nil {
		if ok := walkChecks(yield, chain, p.BypassChecks); !ok {
			return false
		}
	}
	if p.PreChecks != nil {
		if ok := walkChecks(yield, chain, p.PreChecks); !ok {
			return false
		}
	}
	if p.ContChecks != nil {
		if ok := walkChecks(yield, chain, p.ContChecks); !ok {
			return false
		}
	}
	if p.Blocks != nil {
		for _, block := range p.Blocks {
			if ok := walkBlock(yield, chain, block); !ok {
				return false
			}
		}
	}
	if p.PostChecks != nil {
		if ok := walkChecks(yield, chain, p.PostChecks); !ok {
			return false
		}
	}
	if p.DeferredActions != nil {
		if ok := walkDeferredActions(yield, chain, p.DeferredActions); !ok {
			return false
		}
	}
	if p.DeferredChecks != nil {
		if ok := walkChecks(yield, chain, p.DeferredChecks); !ok {
			return false
		}
	}
	return true
}

// Stateful is implemented by every object walk.Plan yields: the Plan and each object below it.
type Stateful interface {
	GetState() workflow.State
	SetState(workflow.State)
}

// SettleRunning sets every object below p that is Running to status, giving it end as its end time if it has none,
// and returns the objects it changed with each one after all of its descendants: the order to write them in, so that a
// parent stored as finished never sits on top of a child that is not.
func SettleRunning(p *workflow.Plan, status workflow.Status, end time.Time) []Item {
	return SettleRunningItems(Plan(p, WithSkipPlan()), status, end)
}

// SettleRunningItems settles only the Running objects yielded by items. items must yield parents before descendants;
// the returned changes are in the opposite order for persistence. Existing end times are preserved. Call only when
// no execution is still changing the selected objects.
func SettleRunningItems(items iter.Seq[Item], status workflow.Status, end time.Time) []Item {
	var changed []Item
	for item := range items {
		obj := item.Value.(Stateful)
		state := obj.GetState()
		if state.Status != workflow.Running {
			continue
		}
		state.Status = status
		if state.End.IsZero() {
			state.End = end
		}
		obj.SetState(state)
		changed = append(changed, item)
	}
	slices.Reverse(changed)
	return changed
}

// RunningObjects yields the objects below p (not p itself) whose status is Running, in walk.Plan order. The caller may
// change an object's state while iterating.
func RunningObjects(p *workflow.Plan) iter.Seq[Item] {
	return func(yield func(Item) bool) {
		for item := range Plan(p) {
			if item.Value.Type() == workflow.OTPlan {
				continue
			}
			if item.Value.(Stateful).GetState().Status != workflow.Running {
				continue
			}
			if !yield(item) {
				return
			}
		}
	}
}

// LastUpdate returns the time of the most recent update to any object in the plan.
func LastUpdate(ctx context.Context, p *workflow.Plan) time.Time {
	if p == nil {
		return time.Time{}
	}

	var last time.Time
	rt := p.RuntimeUpdate.Get()
	if !rt.IsZero() {
		last = rt
	}

	for item := range Plan(p) {
		state := item.Value.(Stateful).GetState()
		if state.Start.After(last) {
			last = state.Start
		}
		if state.End.After(last) {
			last = state.End
		}
	}

	return last
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

func walkDeferredActions(yield func(Item) bool, chain []workflow.Object, da *workflow.DeferredActions) bool {
	if !yield(Item{Chain: chain, Value: da}) {
		return false
	}

	chain = append(chain, da)
	for _, batch := range da.DeferredBatches {
		if !walkDeferBatch(yield, chain, batch) {
			return false
		}
	}
	return true
}

func walkDeferBatch(yield func(Item) bool, chain []workflow.Object, batch *workflow.DeferBatch) bool {
	if !yield(Item{Chain: chain, Value: batch}) {
		return false
	}

	chain = append(chain, batch)
	for _, action := range batch.Actions {
		if !yield(Item{Chain: chain, Value: action}) {
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

// The walk*Reverse functions yield the exact reverse of their forward counterparts: the children of an object are
// walked from last to first and the object itself is yielded after all of them.

func walkPlanReverse(yield func(Item) bool, p *workflow.Plan, skipPlan bool) bool {
	chain := []workflow.Object{p}
	if p.DeferredChecks != nil {
		if !walkChecksReverse(yield, chain, p.DeferredChecks) {
			return false
		}
	}
	if p.DeferredActions != nil {
		if !walkDeferredActionsReverse(yield, chain, p.DeferredActions) {
			return false
		}
	}
	if p.PostChecks != nil {
		if !walkChecksReverse(yield, chain, p.PostChecks) {
			return false
		}
	}
	for i := len(p.Blocks) - 1; i >= 0; i-- {
		if !walkBlockReverse(yield, chain, p.Blocks[i]) {
			return false
		}
	}
	if p.ContChecks != nil {
		if !walkChecksReverse(yield, chain, p.ContChecks) {
			return false
		}
	}
	if p.PreChecks != nil {
		if !walkChecksReverse(yield, chain, p.PreChecks) {
			return false
		}
	}
	if p.BypassChecks != nil {
		if !walkChecksReverse(yield, chain, p.BypassChecks) {
			return false
		}
	}
	if skipPlan {
		return true
	}
	return yield(Item{Value: p})
}

func walkChecksReverse(yield func(Item) bool, chain []workflow.Object, checks *workflow.Checks) bool {
	if !yieldActionsReverse(yield, append(chain, checks), checks.Actions) {
		return false
	}
	return yield(Item{Chain: chain, Value: checks})
}

func walkBlockReverse(yield func(Item) bool, chain []workflow.Object, block *workflow.Block) bool {
	next := append(chain, block)
	if block.DeferredChecks != nil {
		if !walkChecksReverse(yield, next, block.DeferredChecks) {
			return false
		}
	}
	if block.PostChecks != nil {
		if !walkChecksReverse(yield, next, block.PostChecks) {
			return false
		}
	}
	for i := len(block.Sequences) - 1; i >= 0; i-- {
		if !walkSequenceReverse(yield, next, block.Sequences[i]) {
			return false
		}
	}
	if block.ContChecks != nil {
		if !walkChecksReverse(yield, next, block.ContChecks) {
			return false
		}
	}
	if block.PreChecks != nil {
		if !walkChecksReverse(yield, next, block.PreChecks) {
			return false
		}
	}
	if block.BypassChecks != nil {
		if !walkChecksReverse(yield, next, block.BypassChecks) {
			return false
		}
	}
	return yield(Item{Chain: chain, Value: block})
}

func walkDeferredActionsReverse(yield func(Item) bool, chain []workflow.Object, da *workflow.DeferredActions) bool {
	next := append(chain, da)
	for i := len(da.DeferredBatches) - 1; i >= 0; i-- {
		if !walkDeferBatchReverse(yield, next, da.DeferredBatches[i]) {
			return false
		}
	}
	return yield(Item{Chain: chain, Value: da})
}

func walkDeferBatchReverse(yield func(Item) bool, chain []workflow.Object, batch *workflow.DeferBatch) bool {
	if !yieldActionsReverse(yield, append(chain, batch), batch.Actions) {
		return false
	}
	return yield(Item{Chain: chain, Value: batch})
}

func walkSequenceReverse(yield func(Item) bool, chain []workflow.Object, sequence *workflow.Sequence) bool {
	if !yieldActionsReverse(yield, append(chain, sequence), sequence.Actions) {
		return false
	}
	return yield(Item{Chain: chain, Value: sequence})
}

// yieldActionsReverse yields actions from last to first, each with chain as its Chain.
func yieldActionsReverse(yield func(Item) bool, chain []workflow.Object, actions []*workflow.Action) bool {
	for i := len(actions) - 1; i >= 0; i-- {
		if !yield(Item{Chain: chain, Value: actions[i]}) {
			return false
		}
	}
	return true
}
