package coercion

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

// fakeStatusStore returns the Plan with the next status in statuses on each Read, and the last one once they run out.
// If err is set, every Read returns it instead.
type fakeStatusStore struct {
	storage.Vault

	statuses []workflow.Status
	err      error
	reads    atomic.Int32
}

func (f *fakeStatusStore) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	n := int(f.reads.Add(1)) - 1
	if f.err != nil {
		return nil, f.err
	}
	p := &workflow.Plan{ID: id}
	p.State.Set(workflow.State{Status: f.statuses[min(n, len(f.statuses)-1)]})
	return p, nil
}

// TestStatus is also a regression test: Status chose at random between a done Context and a due tick, so once the
// Context was cancelled it could still read the Plan and yield a result. No read may start once the Context is done.
func TestStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		statuses []workflow.Status
		readErr  error
		interval time.Duration
		// cancelled runs Status with a Context that is already done. The run is repeated, since the bug picked the
		// due tick only some of the time.
		cancelled   bool
		wantResults int
		wantReads   int32
		wantErr     bool
	}{
		{
			name:        "Success: results are yielded until the Plan is no longer Running",
			statuses:    []workflow.Status{workflow.Running, workflow.Completed},
			interval:    time.Nanosecond,
			wantResults: 2,
			wantReads:   2,
		},
		{
			name:        "Success: a cancelled Context stops iteration before any read",
			statuses:    []workflow.Status{workflow.Running},
			interval:    time.Nanosecond,
			cancelled:   true,
			wantResults: 0,
			wantReads:   0,
		},
		{
			// Regression: time.NewTicker panicked on an interval that was not positive.
			name:        "Success: an interval that is not positive uses the default instead of panicking",
			statuses:    []workflow.Status{workflow.Running},
			interval:    0,
			cancelled:   true,
			wantResults: 0,
			wantReads:   0,
		},
		{
			name:        "Error: a failed read is yielded and ends iteration",
			statuses:    []workflow.Status{workflow.Running},
			readErr:     fmt.Errorf("read failed"),
			interval:    time.Nanosecond,
			wantResults: 1,
			wantReads:   1,
			wantErr:     true,
		},
	}

	for _, test := range tests {
		runs := 1
		if test.cancelled {
			runs = 200
		}
		for i := 0; i < runs; i++ {
			store := &fakeStatusStore{statuses: test.statuses, err: test.readErr}
			w := &Workstream{store: store}

			ctx, cancel := context.WithCancel(t.Context())
			if test.cancelled {
				cancel()
			}
			results := 0
			var lastErr error
			for r := range w.Status(ctx, uuid.New(), test.interval) {
				results++
				lastErr = r.Err
			}
			cancel()

			switch {
			case lastErr == nil && test.wantErr:
				t.Errorf("TestStatus(%s): got err == nil, want err != nil", test.name)
			case lastErr != nil && !test.wantErr:
				t.Errorf("TestStatus(%s): got err == %s, want err == nil", test.name, lastErr)
			}
			if results != test.wantResults {
				t.Errorf("TestStatus(%s): run %d: got %d results, want %d", test.name, i, results, test.wantResults)
			}
			if got := store.reads.Load(); got != test.wantReads {
				t.Errorf("TestStatus(%s): run %d: got %d reads, want %d", test.name, i, got, test.wantReads)
			}
			if t.Failed() {
				break
			}
		}
	}
}
