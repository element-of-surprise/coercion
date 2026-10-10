package sqlite

import (
	"testing"
	"time"

	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/values/chans"

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

	// The wait running out is the pass: Close is still waiting for the write.
	wctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	err, r := chans.Get(wctx, closed)
	cancel()
	if r != chans.ResultCtxDone {
		t.Errorf("TestClose: Close returned (err == %v) while a write held the lock, want it to wait for the write", err)
		v.mu.Unlock()
		return
	}

	v.mu.Unlock()
	if err := <-closed; err != nil {
		t.Errorf("TestClose: got err == %s, want err == nil", err)
	}
}
