package sm

import (
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/values/generics/result"

	"github.com/element-of-surprise/coercion/workflow/context"
)

// Started reports whether a recovered run got going. Recovery reports when it has written the fixed Plan and moved
// on to execution, or why it could not. Other code reports when the run ends before Recovery could, so a waiter never
// hangs. Only the first report counts.
type Started struct {
	once sync.Once
	v    *result.Value[struct{}]
}

// NewStarted returns a Started with nothing reported.
func NewStarted() *Started {
	return &Started{v: result.New[struct{}]()}
}

// Report records the outcome: nil means the run is set up and executing. Reports after the first are ignored.
func (s *Started) Report(err error) {
	s.once.Do(func() { s.v.Set(struct{}{}, err) })
}

// Wait blocks until an outcome is reported or ctx ends, and returns the reported error (or ctx's).
func (s *Started) Wait(ctx context.Context) error {
	_, err := s.v.Wait(ctx)
	return err
}
