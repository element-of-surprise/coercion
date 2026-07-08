// Package clone provides advanced cloning for Plans and object contained in Plans.
package clone

import (
	"reflect"
	"strings"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/brunoga/deep"
)

type cloneOptions struct {
	keepSecrets     bool
	removeCompleted bool
	keepState       bool

	// callNum is used to track where we are in the clone call stack.
	callNum int
}

// Option is an option for Plan().
type Option func(c cloneOptions) cloneOptions

// WithKeepSecrets sets that secrets should be kept for Action requests
// and responses that are marked with `coerce:"secure"`.
// By default they are wiped when cloning. Please note that you can still
// leak data in names, descriptions and meta data you include.
func WithKeepSecrets() Option {
	return func(c cloneOptions) cloneOptions {
		c.keepSecrets = true
		return c
	}
}

// WithRemoveCompletedSequences removes Sequences that are Completed from Blocks.
// If a Block contains only Completed Sequences, the Block is removed as long as
// all PreChecks, PostChecks have completed and ContChecks are not in a failed state.
// If no Blocks exist and all the Plan checks are in a state similar to above, a returned Plan will be nil.
func WithRemoveCompletedSequences() Option {
	return func(c cloneOptions) cloneOptions {
		c.removeCompleted = true
		return c
	}
}

// WithKeepState keeps all the state for all objects. This includes IDs,
// output, etc. This is only useful if going to out to display or writing
// to disk. You cannot submit an object cloned this way.
func WithKeepState() Option {
	return func(c cloneOptions) cloneOptions {
		c.keepState = true
		return c
	}
}

// withOptions allows internal methods to pass down options to sub-clone methods without
// having to recreate the cloneOptions struct.
func withOptions(opts cloneOptions) Option {
	return func(c cloneOptions) cloneOptions {
		return opts
	}
}

// Plan clones a plan with various options. This includes all sub-objects. If WithRemoveCompletedSequences
// is used, the Plan may be nil if all Blocks are removed and checks are not in a problem state. Plans are
// always deep cloned.
func Plan(ctx context.Context, p *workflow.Plan, options ...Option) *workflow.Plan {
	if p == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	meta := make([]byte, len(p.Meta))
	copy(meta, p.Meta)

	np := &workflow.Plan{
		Name:    p.Name,
		Descr:   p.Descr,
		GroupID: p.GroupID,
		Meta:    meta,
	}

	if opts.keepState {
		np.ID = p.ID
		np.Reason = p.Reason
		cloneStateAtomic(&np.State, &p.State)
		np.SubmitTime = p.SubmitTime
	}

	if p.BypassChecks != nil {
		if opts.removeCompleted && p.BypassChecks.State.Get().Status == workflow.Completed {
			return nil
		}
		np.BypassChecks = Checks(ctx, p.BypassChecks, withOptions(opts))
	}
	if p.PreChecks != nil {
		np.PreChecks = Checks(ctx, p.PreChecks, withOptions(opts))
	}
	if p.ContChecks != nil {
		np.ContChecks = Checks(ctx, p.ContChecks, withOptions(opts))
	}
	if p.PostChecks != nil {
		np.PostChecks = Checks(ctx, p.PostChecks, withOptions(opts))
	}
	if p.DeferredChecks != nil {
		np.DeferredChecks = Checks(ctx, p.DeferredChecks, withOptions(opts))
	}
	if p.DeferredActions != nil {
		np.DeferredActions = DeferredActions(ctx, p.DeferredActions, withOptions(opts))
	}

	np.Blocks = make([]*workflow.Block, 0, len(p.Blocks))
	for _, b := range p.Blocks {
		nb := Block(ctx, b, withOptions(opts))
		// This happens if the Block has completed.
		if nb == nil {
			continue
		}
		np.Blocks = append(np.Blocks, nb)
	}

	if opts.removeCompleted {
		// We have some blocks left, so we don't return nil
		if len(np.Blocks) != 0 {
			return np
		}
		// There are no blocks, but if any of these are not in a good state, we return the Plan.
		if p.PreChecks != nil && p.PreChecks.State.Get().Status != workflow.Completed {
			return np
		}
		if p.PostChecks != nil && p.PostChecks.State.Get().Status != workflow.Completed {
			return np
		}
		if p.ContChecks != nil && p.ContChecks.State.Get().Status == workflow.Failed {
			return np
		}
		if p.DeferredChecks != nil && p.DeferredChecks.State.Get().Status == workflow.Failed {
			return np
		}
		if p.DeferredActions != nil && p.DeferredActions.State.Get().Status == workflow.Failed {
			return np
		}
		return nil
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(np)
	}

	return np
}

// Checks clones a set of Checks. This includes all sub-objects.
func Checks(ctx context.Context, c *workflow.Checks, options ...Option) *workflow.Checks {
	if c == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	clone := &workflow.Checks{
		Delay:   c.Delay,
		Actions: make([]*workflow.Action, len(c.Actions)),
	}

	if opts.keepState {
		clone.ID = c.ID
		cloneStateAtomic(&clone.State, &c.State)
	}

	for i := 0; i < len(c.Actions); i++ {
		clone.Actions[i] = Action(ctx, c.Actions[i], withOptions(opts))
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(clone)
	}

	return clone
}

// Block clones a Block. This includes all sub-objects.
func Block(ctx context.Context, b *workflow.Block, options ...Option) *workflow.Block {
	if b == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	n := &workflow.Block{
		Name:              b.Name,
		Descr:             b.Descr,
		EntranceDelay:     b.EntranceDelay,
		ExitDelay:         b.ExitDelay,
		Concurrency:       b.Concurrency,
		ToleratedFailures: b.ToleratedFailures,
	}

	if opts.keepState {
		n.ID = b.ID
		cloneStateAtomic(&n.State, &b.State)
	}

	var (
		preChecksCompleted      bool
		contChecksNotFailed     bool
		postChecksCompleted     bool
		deferredChecksCompleted bool
	)

	if b.BypassChecks != nil {
		state := b.BypassChecks.State.Get()
		if state.Status == workflow.Completed {
			if opts.removeCompleted {
				return nil
			}
		}
		n.BypassChecks = Checks(ctx, b.BypassChecks, withOptions(opts))
	}
	if b.PreChecks != nil {
		state := b.PreChecks.State.Get()
		if state.Status == workflow.Completed {
			preChecksCompleted = true
		}
		n.PreChecks = Checks(ctx, b.PreChecks, withOptions(opts))
	}
	if b.ContChecks != nil {
		state := b.ContChecks.State.Get()
		if state.Status != workflow.Failed {
			contChecksNotFailed = true
		}
		n.ContChecks = Checks(ctx, b.ContChecks, withOptions(opts))
	}
	if b.PostChecks != nil {
		state := b.PostChecks.State.Get()
		if state.Status == workflow.Completed {
			postChecksCompleted = true
		}
		n.PostChecks = Checks(ctx, b.PostChecks, withOptions(opts))
	}
	if b.DeferredChecks != nil {
		state := b.DeferredChecks.State.Get()
		if state.Status == workflow.Completed {
			deferredChecksCompleted = true
		}
		n.DeferredChecks = Checks(ctx, b.DeferredChecks, withOptions(opts))
	}

	n.Sequences = make([]*workflow.Sequence, 0, len(b.Sequences))
	for _, seq := range b.Sequences {
		ns := Sequence(ctx, seq, withOptions(opts))
		if ns == nil {
			continue
		}
		n.Sequences = append(n.Sequences, ns)
	}

	if opts.removeCompleted && len(n.Sequences) == 0 {
		if preChecksCompleted || contChecksNotFailed || postChecksCompleted || deferredChecksCompleted {
			return nil
		}
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(n)
	}

	return n
}

// Sequence clones a Sequence. This includes all sub-objects.
func Sequence(ctx context.Context, s *workflow.Sequence, options ...Option) *workflow.Sequence {
	if s == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	ns := &workflow.Sequence{
		Name:    s.Name,
		Descr:   s.Descr,
		Actions: make([]*workflow.Action, len(s.Actions)),
	}

	if opts.keepState {
		ns.ID = s.ID
		cloneStateAtomic(&ns.State, &s.State)
	}

	for i, a := range s.Actions {
		na := Action(ctx, a, withOptions(opts))
		if na == nil {
			continue
		}
		ns.Actions[i] = na
	}

	if len(ns.Actions) == 0 {
		return nil
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(ns)
	}

	return ns
}

// DeferredActions clones a DeferredActions. This includes all sub-objects.
func DeferredActions(ctx context.Context, da *workflow.DeferredActions, options ...Option) *workflow.DeferredActions {
	if da == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	n := &workflow.DeferredActions{}

	if opts.keepState {
		n.ID = da.ID
		cloneStateAtomic(&n.State, &da.State)
	}

	if len(da.DeferredBatches) > 0 {
		n.DeferredBatches = make([]*workflow.DeferBatch, 0, len(da.DeferredBatches))
		for _, b := range da.DeferredBatches {
			nb := DeferBatch(ctx, b, withOptions(opts))
			if nb == nil {
				continue
			}
			n.DeferredBatches = append(n.DeferredBatches, nb)
		}
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(n)
	}

	return n
}

// DeferBatch clones a DeferBatch. This includes all sub-objects.
func DeferBatch(ctx context.Context, b *workflow.DeferBatch, options ...Option) *workflow.DeferBatch {
	if b == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	n := &workflow.DeferBatch{
		When:        b.When,
		FailElement: b.FailElement,
		Sequence: workflow.Sequence{
			Name:  b.Name,
			Descr: b.Descr,
			Key:   b.Key,
		},
	}

	if opts.keepState {
		n.ID = b.ID
		cloneStateAtomic(&n.State, &b.State)
	}

	n.Actions = make([]*workflow.Action, 0, len(b.Actions))
	for _, a := range b.Actions {
		na := Action(ctx, a, withOptions(opts))
		if na == nil {
			continue
		}
		n.Actions = append(n.Actions, na)
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(n)
	}

	return n
}

// Action clones an Action. This includes all sub-objects.
func Action(ctx context.Context, a *workflow.Action, options ...Option) *workflow.Action {
	if a == nil {
		return nil
	}

	opts := cloneOptions{}
	for _, o := range options {
		opts = o(opts)
	}
	opts.callNum++

	if opts.removeCompleted && a.State.Get().Status == workflow.Completed {
		return nil
	}

	na := &workflow.Action{
		Name:    a.Name,
		Descr:   a.Descr,
		Plugin:  a.Plugin,
		Timeout: a.Timeout,
		Retries: a.Retries,
		Req:     deep.MustCopy(a.Req),
	}

	if opts.keepState {
		na.ID = a.ID
		cloneStateAtomic(&na.State, &a.State)
		// Only materialize Attempts when there is something to copy; Set with an empty slice would
		// turn an unset AtomicSlice into a materialized one, so the clone would no longer match its source.
		if attempts := cloneAttempts(a.Attempts.Get()); len(attempts) > 0 {
			na.Attempts.Set(attempts)
		}
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(na)
	}

	return na
}

// cloneStateAtomic clones the state from src AtomicValue[State] into dst AtomicValue[State].
func cloneStateAtomic(dst, src *workflow.AtomicValue[workflow.State]) {
	state := src.Get()
	if state == (workflow.State{}) {
		return
	}
	dst.Set(workflow.State{
		Status: state.Status,
		Start:  state.Start,
		End:    state.End,
	})
}

// cloneAttempts clones a []workflow.Attempt.
func cloneAttempts(attempts []workflow.Attempt) []workflow.Attempt {
	if len(attempts) == 0 {
		return nil
	}

	sl := make([]workflow.Attempt, 0, len(attempts))
	for _, attempt := range attempts {
		na := workflow.Attempt{
			Resp:  deep.MustCopy(attempt.Resp),
			Err:   cloneErr(attempt.Err),
			Start: attempt.Start,
			End:   attempt.End,
		}
		sl = append(sl, na)
	}
	return sl
}

// cloneErr clones a *plugins.Err.
func cloneErr(e *plugins.Error) *plugins.Error {
	if e == nil {
		return nil
	}
	ne := &plugins.Error{
		Code:      e.Code,
		Message:   e.Message,
		Permanent: e.Permanent,
	}
	if e.Wrapped != nil {
		ne.Wrapped = cloneErr(e.Wrapped)
	}
	return ne
}

// tags is a set of tags for a field.
type tags map[string]bool

func (t tags) hasTag(tag string) bool {
	if t == nil {
		return false
	}
	return t[tag]
}

// getTags returns the tags for a field.
func getTags(f reflect.StructField) tags {
	strTags := f.Tag.Get("coerce")
	if strings.TrimSpace(strTags) == "" {
		return nil
	}
	t := make(tags)
	for _, tag := range strings.Split(strTags, ",") {
		tag = strings.TrimSpace(strings.ToLower(tag))
		t[tag] = true
	}
	return t
}
