package sqlite

import (
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
)

func TestUpdatePlan(t *testing.T) {
	t.Parallel()

	done := workflow.State{Status: workflow.Completed, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC()}

	tests := []struct {
		name string
		// stored is whether the Plan is stored before it is updated.
		stored  bool
		wantErr bool
	}{
		{
			name:   "Success: a stored Plan's state is updated.",
			stored: true,
		},
		{
			name:    "Error: a Plan with no stored row is not updated.",
			wantErr: true,
		},
	}

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	for _, test := range tests {
		pool, err := freshInMemoryPool(t)
		if err != nil {
			t.Fatalf("TestUpdatePlan(%s): freshInMemoryPool: %s", test.name, err)
		}
		t.Cleanup(func() { pool.Close() })

		p := createTestPlan(t, time.Now().UTC())
		if test.stored {
			conn, err := pool.Take(t.Context())
			if err != nil {
				t.Fatalf("TestUpdatePlan(%s): Take: %s", test.name, err)
			}
			err = commitPlan(t.Context(), conn, p, nil)
			pool.Put(conn)
			if err != nil {
				t.Fatalf("TestUpdatePlan(%s): commitPlan: %s", test.name, err)
			}
		}
		p.State.Set(done)

		capture := &CaptureStmts{}
		u := objectUpdater{mu: &sync.RWMutex{}, pool: pool, capture: capture}
		err = u.UpdatePlan(t.Context(), p)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestUpdatePlan(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestUpdatePlan(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			if capture.Len() != 0 {
				t.Errorf("TestUpdatePlan(%s): got %d captured updates, want 0", test.name, capture.Len())
			}
			continue
		}

		rdr := reader{mu: &sync.RWMutex{}, pool: pool, reg: reg}
		got, err := rdr.Read(t.Context(), p.ID)
		if err != nil {
			t.Fatalf("TestUpdatePlan(%s): Read: %s", test.name, err)
		}
		if got.State.Get().Status != done.Status {
			t.Errorf("TestUpdatePlan(%s): got status %v, want %v", test.name, got.State.Get().Status, done.Status)
		}
	}
}
