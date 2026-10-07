package sqlite

import (
	"testing"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins/registry"
)

func TestClose(t *testing.T) {
	// Not parallel: asserts that Close has not returned within a wait.

	v, err := New(t.Context(), "", registry.New(), WithInMemory())
	if err != nil {
		t.Fatalf("TestClose: New: %s", err)
	}

	// Hold the lock the way a write does for its whole transaction. Closing the pool now would interrupt it.
	v.mu.Lock()
	closed := make(chan error, 1)
	if !context.Pool(t.Context()).Submit(t.Context(), func() { closed <- v.Close(t.Context()) }) {
		v.mu.Unlock()
		t.Fatalf("TestClose: couldn't submit Close")
	}

	select {
	case err := <-closed:
		t.Errorf("TestClose: Close returned (err == %v) while a write held the lock, want it to wait for the write", err)
		v.mu.Unlock()
		return
	case <-time.After(100 * time.Millisecond):
	}

	v.mu.Unlock()
	if err := <-closed; err != nil {
		t.Errorf("TestClose: got err == %s, want err == nil", err)
	}
}
