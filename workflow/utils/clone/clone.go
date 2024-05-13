// Package clone provides advanced cloning for Plans and object contained in Plans.
package clone

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

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
		np.State = cloneState(p.State)
		np.SubmitTime = p.SubmitTime

		if !opts.keepSecrets && opts.callNum == 1 {
			defer func() {
				Secure(np)
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
		clone.State = cloneState(c.State)
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
		n.State = cloneState(b.State)
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
		ns.State = cloneState(s.State)
	}

	for i, a := range s.Actions {
		ns.Actions[i] = Action(ctx, a, withOptions(opts))
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(ns)
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
		Req:     deep.MustCopy(a.Req),
	}

	if opts.keepState {
		na.ID = a.ID
		na.State = cloneState(a.State)
		na.Attempts = cloneAttempts(a.Attempts)
	}

	if !opts.keepSecrets && opts.callNum == 1 {
		Secure(na)
	}

	return na
}

// cloneState clones a *workflow.State.
func cloneState(state *workflow.State) *workflow.State {
	if state == nil {
		return nil
	}

	return &workflow.State{
		Status: state.Status,
		Start:  state.Start,
		End:    state.End,
	}
}

// cloneAttempts clones a []*workflow.Attempt.
func cloneAttempts(attempts []*workflow.Attempt) []*workflow.Attempt {
	if len(attempts) == 0 {
		return nil
	}

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

// SecureStr is the string that is used to replace sensitive information.
// Use this in any tests to compare against the output of Secure. This string
// can be changed and using this constant will ensure that all tests are updated.
const SecureStr = "[secret hidden]"

// Secure removes sensitive information from a struct that is marked with the `coerce:"secure"` tag.
// It does not handle arrays and if you have some really bizare struct you might find it skipped
// something. Like a *map[string]*any that stores an any storing an *any that stores a *slice of struct. It probably
// will work, but I certainly haven't tested every iteration of weird stuff like that. We also don't handle anything
// not JSON serializable. So private fields are not handled.
func Secure(v any) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Ptr {
		return fmt.Errorf("value must be a pointer to a struct")
	}

	if val.IsNil() {
		return nil
	}

	if val.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("value must be a pointer to a struct")
	}
	secureStruct(val)
	return nil
}

// secureStruct recurses through a pointer or reference type in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func securePtrOrRef(val reflect.Value) reflect.Value {
	if val.IsNil() || val.IsZero() {
		return val
	}

	switch val.Kind() {
	case reflect.Ptr:
		return securePtr(val)
	case reflect.Slice:
		return secureSlice(val)
	case reflect.Map:
		return secureMap(val)
	case reflect.Interface:
		return secureInterface(val)
	}

	return val
}

// securePtr recurses throught a pointer to a value in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func securePtr(val reflect.Value) reflect.Value {
	switch val.Elem().Kind() {
	case reflect.Struct:
		return secureStruct(val)
	case reflect.Slice:
		return secureSlice(val)
	case reflect.Map:
		return secureMap(val)
	case reflect.Interface:
		return secureInterface(val)
	}
	return val
}

// secureStruct removes sensitive information from a *struct that is marked with the `coerce:"secure"` tag.
func secureStruct(ptr reflect.Value) reflect.Value {
	if ptr.Kind() != reflect.Ptr || ptr.Elem().Kind() != reflect.Struct {
		panic("value must be a pointer to a struct")
	}
	if ptr.IsNil() {
		return ptr
	}

	val := ptr.Elem()

	// Don't mess with time.Time.
	if _, ok := val.Interface().(time.Time); ok {
		return ptr
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		if !val.Type().Field(i).IsExported() {
			continue
		}
		field := val.Field(i)

		log.Println("field: ", val.Type().Field(i).Name)

		tags := getTags(typ.Field(i))
		if tags.hasTag("secure") {
			if field.Type().Kind() == reflect.String {
				field.SetString("[secret hidden]")
			} else {
				if !field.CanSet() {
					if field.CanAddr() {
						field = field.Addr()
					} else {
						// Diagnostic panic
						panic(fmt.Sprintf("cannot set field(%s) of type %q", typ.Field(i).Name, typ.Field(i).Type.String()))
					}
				}
				field.Set(reflect.Zero(field.Type()))
			}
			continue
		}

		// Okay, there is not secure tag, so we didn't make it a zero value.
		// However, if it is a *struct, struct or interface (containing a *struct or struct),
		// we need to recurse.

		switch field.Kind() {
		case reflect.Struct:
			if _, ok := field.Interface().(time.Time); ok {
				continue
			}
			if field.CanAddr() {
				secureStruct(field.Addr())
			} else {
				log.Printf("Type: %T", field.Interface())
				fieldPtr := noAddrStruct(field)
				fieldPtr = secureStruct(fieldPtr)
				val.Field(i).Set(fieldPtr.Elem())
			}
		case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
			field = securePtrOrRef(field)
			val.Field(i).Set(field)
		}
	}
	return ptr
}

// secureSlice recurses through a slice in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func secureSlice(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Slice {
		panic("val must be a slice")
	}

	if val.IsNil() {
		return val
	}

	for i := 0; i < val.Len(); i++ {
		switch val.Index(i).Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			securePtrOrRef(val.Index(i))
		case reflect.Struct:
			if _, ok := val.Interface().(time.Time); ok {
				continue
			}
			log.Printf("Type: %T", val.Interface())
			ptr := noAddrStruct(val.Index(i))
			secureStruct(ptr)
			val.Index(i).Set(ptr.Elem())
		}
	}
	return val
}

// secureMap recurses through a map in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields. It does not look at the keys,
// as those aren't valid JSON values if not a string.
func secureMap(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Map {
		panic("val must be a map")
	}

	if val.IsNil() {
		return val
	}

	for _, key := range val.MapKeys() {
		elem := val.MapIndex(key)
		switch elem.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			elem = securePtrOrRef(elem)
			val.SetMapIndex(key, elem)
		case reflect.Struct:
			if _, ok := val.Interface().(time.Time); ok {
				continue
			}
			ptr := noAddrStruct(elem)
			secureStruct(ptr)
			val.SetMapIndex(key, ptr.Elem())
		}
	}
	return val
}

// secureInterface recurses through an interface in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func secureInterface(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Interface {
		panic("val must be an interface")
	}
	if val.IsNil() {
		return val
	}
	elem := val.Elem()
	switch elem.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice:
		elem = securePtrOrRef(elem)
		val.Set(elem)
	case reflect.Struct:
		if _, ok := val.Interface().(time.Time); ok {
			return val
		}
		log.Printf("Type: %T", val.Interface())
		ptr := noAddrStruct(elem)
		ptr = secureStruct(ptr)
		val.Set(ptr.Elem())
	}
	return val
}

// noAddrStruct takes a struct and returns a *struct with the same values. This is
// useful when a struct cannot have Set() or Addr() called on it.
func noAddrStruct(orig reflect.Value) reflect.Value {
	if orig.Kind() != reflect.Struct {
		panic("orig must be a struct, not " + orig.Kind().String())
	}

	// Create a new instance of the struct type of original
	// Note: reflect.New returns a pointer, so we use Elem to get the actual struct
	ptr := reflect.New(orig.Type())
	n := ptr.Elem()

	// Copy each field from the original to the new instance
	for i := 0; i < orig.NumField(); i++ {
		if !orig.Type().Field(i).IsExported() {
			continue
		}

		nf := n.Field(i)
		of := orig.Field(i)

		// Ensure that the field is settable
		if !nf.CanSet() {
			panic("bug: field is not settable")
		}
		nf.Set(of)
	}
	return ptr
}
