package workflow

import (
	"bytes"
	"reflect"

	"github.com/element-of-surprise/coercion/plugins"
)

// Equal returns true if the State objects are equal.
// Only compares public fields.
func (s *State) Equal(other *State) bool {
	if s == other {
		return true
	}
	if s == nil || other == nil {
		return false
	}

	return s.Status == other.Status &&
		s.Start.Equal(other.Start) &&
		s.End.Equal(other.End) &&
		s.ETag == other.ETag
}

// Equal returns true if the Plan objects are equal.
// Only compares public fields.
func (p *Plan) Equal(other *Plan) bool {
	if p == other {
		return true
	}
	if p == nil || other == nil {
		return false
	}

	if p.ID != other.ID {
		return false
	}
	if p.Name != other.Name {
		return false
	}
	if p.Descr != other.Descr {
		return false
	}
	if p.GroupID != other.GroupID {
		return false
	}
	if !bytes.Equal(p.Meta, other.Meta) {
		return false
	}
	if !checksEqual(p.BypassChecks, other.BypassChecks) {
		return false
	}
	if !checksEqual(p.PreChecks, other.PreChecks) {
		return false
	}
	if !checksEqual(p.ContChecks, other.ContChecks) {
		return false
	}
	if !checksEqual(p.PostChecks, other.PostChecks) {
		return false
	}
	if !checksEqual(p.DeferredChecks, other.DeferredChecks) {
		return false
	}
	if !sliceOfObjectsEqual(p.Blocks, other.Blocks) {
		return false
	}
	if !stateEqual(p.State.Get(), other.State.Get()) {
		return false
	}
	if !p.SubmitTime.Equal(other.SubmitTime) {
		return false
	}
	if p.Reason != other.Reason {
		return false
	}

	return true
}

// Equal returns true if the Checks objects are equal.
// Only compares public fields.
func (c *Checks) Equal(other *Checks) bool {
	if c == other {
		return true
	}
	if c == nil || other == nil {
		return false
	}

	if c.ID != other.ID {
		return false
	}
	if c.Key != other.Key {
		return false
	}
	if c.Delay != other.Delay {
		return false
	}
	if !sliceOfObjectsEqual(c.Actions, other.Actions) {
		return false
	}
	if !stateEqual(c.State.Get(), other.State.Get()) {
		return false
	}

	return true
}

// Equal returns true if the Block objects are equal.
// Only compares public fields.
func (b *Block) Equal(other *Block) bool {
	if b == other {
		return true
	}
	if b == nil || other == nil {
		return false
	}

	if b.ID != other.ID {
		return false
	}
	if b.Key != other.Key {
		return false
	}
	if b.Name != other.Name {
		return false
	}
	if b.Descr != other.Descr {
		return false
	}
	if b.EntranceDelay != other.EntranceDelay {
		return false
	}
	if b.ExitDelay != other.ExitDelay {
		return false
	}
	if !checksEqual(b.BypassChecks, other.BypassChecks) {
		return false
	}
	if !checksEqual(b.PreChecks, other.PreChecks) {
		return false
	}
	if !checksEqual(b.ContChecks, other.ContChecks) {
		return false
	}
	if !checksEqual(b.PostChecks, other.PostChecks) {
		return false
	}
	if !checksEqual(b.DeferredChecks, other.DeferredChecks) {
		return false
	}
	if !sliceOfObjectsEqual(b.Sequences, other.Sequences) {
		return false
	}
	if b.Concurrency != other.Concurrency {
		return false
	}
	if b.ToleratedFailures != other.ToleratedFailures {
		return false
	}
	if !stateEqual(b.State.Get(), other.State.Get()) {
		return false
	}

	return true
}

// Equal returns true if the Sequence objects are equal.
// Only compares public fields.
func (s *Sequence) Equal(other *Sequence) bool {
	if s == other {
		return true
	}
	if s == nil || other == nil {
		return false
	}

	if s.ID != other.ID {
		return false
	}
	if s.Key != other.Key {
		return false
	}
	if s.Name != other.Name {
		return false
	}
	if s.Descr != other.Descr {
		return false
	}
	if !sliceOfObjectsEqual(s.Actions, other.Actions) {
		return false
	}
	if !stateEqual(s.State.Get(), other.State.Get()) {
		return false
	}

	return true
}

// Equal returns true if the Action objects are equal.
// Only compares public fields.
func (a *Action) Equal(other *Action) bool {
	if a == other {
		return true
	}
	if a == nil || other == nil {
		return false
	}

	if a.ID != other.ID {
		return false
	}
	if a.Key != other.Key {
		return false
	}
	if a.Name != other.Name {
		return false
	}
	if a.Descr != other.Descr {
		return false
	}
	if a.Plugin != other.Plugin {
		return false
	}
	if a.Timeout != other.Timeout {
		return false
	}
	if a.Retries != other.Retries {
		return false
	}
	if !reflect.DeepEqual(a.Req, other.Req) {
		return false
	}
	if !sliceOfObjectsEqual(a.Attempts.Get(), other.Attempts.Get()) {
		return false
	}
	if !stateEqual(a.State.Get(), other.State.Get()) {
		return false
	}

	return true
}

// Equal returns true if the Attempt objects are equal.
// Only compares public fields.
func (a Attempt) Equal(other Attempt) bool {
	if a == other {
		return true
	}

	if !reflect.DeepEqual(a.Resp, other.Resp) {
		return false
	}
	if !pluginErrorEqual(a.Err, other.Err) {
		return false
	}
	if !a.Start.Equal(other.Start) {
		return false
	}
	if !a.End.Equal(other.End) {
		return false
	}

	return true
}

func checksEqual(a, b *Checks) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}

func stateEqual(a, b State) bool {
	return a.Equal(&b)
}

type objectEqual[T any] interface {
	Equal(b T) bool
	self() T
}

func sliceOfObjectsEqual[O any, T objectEqual[O]](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 && len(b) == 0 {
		return true
	}

	// This does a pointer comparison between a slice's data pointer. If these two are the same array,
	// then the are the same data. This trick is used in reflect.DeepEqual() but for some reason not in
	// slices.Equal().
	if &a[0] == &b[0] {
		return true
	}

	for i := range a {
		if !a[i].Equal(b[i].self()) {
			return false
		}
	}
	return true
}

func pluginErrorEqual(a, b *plugins.Error) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Compare public fields of plugins.Error
	if a.Code != b.Code {
		return false
	}
	if a.Message != b.Message {
		return false
	}
	if a.Permanent != b.Permanent {
		return false
	}
	// Recursively compare Wrapped error
	return pluginErrorEqual(a.Wrapped, b.Wrapped)
}
