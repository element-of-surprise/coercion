// Package clone provides advanced cloning for Plans and object contained in Plans.
package clone

import (
	"context"
	"reflect"
	"strings"

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
		np.State = state(p.State)
		np.SubmitTime = p.SubmitTime

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				secure(np)
			}()
		}
	}

	if p.PreChecks != nil {
		np.PreChecks = p.PreChecks.Clone()
	}
	if p.ContChecks != nil {
		np.ContChecks = p.ContChecks.Clone()
	}
	if p.PostChecks != nil {
		np.PostChecks = p.PostChecks.Clone()
	}

	np.Blocks = make([]*workflow.Block, 0, len(p.Blocks))
	for _, b := range p.Blocks {
		nb := Block(ctx, b, withOptions(opts))
		// This happens if the Block has completed.
		if np == nil {
			continue
		}
		np.Blocks = append(np.Blocks, nb)
	}

	if opts.removeCompleted {
		if len(np.Blocks) != 0 {
			return np
		}
		if p.PreChecks.State.Status != workflow.Completed {
			return np
		}
		if p.PostChecks.State.Status != workflow.Completed {
			return np
		}
		if p.ContChecks.State.Status == workflow.Failed {
			return np
		}
		return nil
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
		clone.State = state(c.State)

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				secure(clone)
			}()
		}
	}

	for i := 0; i < len(c.Actions); i++ {
		clone.Actions[i] = Action(ctx, c.Actions[i], withOptions(opts))
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
		n.State = state(b.State)

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				secure(n)
			}()
		}
	}

	if b.PreChecks != nil {
		n.PreChecks = Checks(ctx, b.PreChecks, withOptions(opts))
	}
	if b.ContChecks != nil {
		n.ContChecks = Checks(ctx, b.ContChecks, withOptions(opts))
	}
	if b.PostChecks != nil {
		n.PostChecks = Checks(ctx, b.PostChecks, withOptions(opts))
	}

	n.Sequences = make([]*workflow.Sequence, 0, len(b.Sequences))
	for _, seq := range b.Sequences {
		if opts.removeCompleted {
			if seq.State.Status == workflow.Completed {
				continue
			}
		}
		ns := Sequence(ctx, seq, withOptions(opts))
		n.Sequences = append(n.Sequences, ns)
	}

	if opts.removeCompleted && len(n.Sequences) == 0 {
		// We are checking against the original object, not the cloned one which may or may not have state.
		if b.PreChecks.State.Status != workflow.Completed {
			return n
		}
		if b.PostChecks.State.Status != workflow.Completed {
			return n
		}
		if b.ContChecks.State.Status == workflow.Failed {
			return n
		}
		return nil
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
		ns.State = state(s.State)

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				secure(ns)
			}()
		}
	}

	for i, a := range s.Actions {
		ns.Actions[i] = Action(ctx, a, withOptions(opts))
	}

	return ns
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

	na := &workflow.Action{
		Name:    a.Name,
		Descr:   a.Descr,
		Plugin:  a.Plugin,
		Timeout: a.Timeout,
		Retries: a.Retries,
		Req:     a.Req,
	}

	if opts.keepState {
		na.ID = a.ID
		na.State = state(a.State)
		na.Attempts = attempts(a.Attempts)

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				secure(na)
			}()
		}
	}

	return na
}

// state clones a *workflow.State.
func state(state *workflow.State) *workflow.State {
	return &workflow.State{
		Status: state.Status,
		Start:  state.Start,
		End:    state.End,
	}
}

// attempts clones a []*workflow.Attempt.
func attempts(attempts []*workflow.Attempt) []*workflow.Attempt {
	sl := make([]*workflow.Attempt, 0, len(attempts))
	for _, attempt := range attempts {
		na := &workflow.Attempt{
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

func secure(v any) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if !field.CanSet() {
			field = field.Addr()
		}

		tags := getTags(typ.Field(i))
		if tags.hasTag("secure") {
			if field.Type().Kind() == reflect.String {
				field.SetString("[secret hidden]")
			} else {
				field.Set(reflect.Zero(field.Type()))
			}
			continue
		}

		// Recursively coerce nested structs
		if field.Kind() == reflect.Struct || (field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct) {
			secure(field.Addr().Interface())
		}
	}
}
