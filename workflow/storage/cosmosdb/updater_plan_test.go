package cosmosdb

import (
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
)

func TestUpdatePlan(t *testing.T) {
	t.Parallel()

	// created and heartbeat are the Plan's RuntimeUpdate when it is stored and when it is updated. Startup recovery ages
	// a Plan out by its last update, so a heartbeat that is not stored lets a restart fail a Plan that is still live.
	created := time.Unix(150, 0).UTC()
	heartbeat := time.Unix(300, 0).UTC()

	tests := []struct {
		name string
		// runtimeUpdate is the Plan's RuntimeUpdate when it is created.
		runtimeUpdate time.Time
		// heartbeat is the RuntimeUpdate set on the Plan before UpdatePlan.
		heartbeat time.Time
	}{
		{
			// Regression: UpdatePlan did not patch runtimeUpdate and the plan document had no field for it, so the
			// heartbeat was never stored.
			name:          "Success: a Plan's RuntimeUpdate is stored when it is created and when it is updated",
			runtimeUpdate: created,
			heartbeat:     heartbeat,
		},
		{
			name:      "Success: a Plan created with no RuntimeUpdate stores the first one it is updated with",
			heartbeat: heartbeat,
		},
	}

	for _, test := range tests {
		store := newFakeStorage(testReg)
		mu := &sync.RWMutex{}
		r := reader{mu: mu, client: store, reg: testReg}
		u := planUpdater{mu: mu, client: store, defaultIOpts: &azcosmos.ItemOptions{}}

		p := NewTestPlan()
		p.RuntimeUpdate.Set(test.runtimeUpdate)
		if err := store.WritePlan(t.Context(), p); err != nil {
			t.Fatalf("TestUpdatePlan(%s): WritePlan: %s", test.name, err)
		}

		got, err := r.Read(t.Context(), p.ID)
		if err != nil {
			t.Fatalf("TestUpdatePlan(%s): Read after WritePlan: %s", test.name, err)
		}
		if !got.RuntimeUpdate.Get().Equal(test.runtimeUpdate) {
			t.Errorf("TestUpdatePlan(%s): after WritePlan got RuntimeUpdate %v, want %v", test.name, got.RuntimeUpdate.Get(), test.runtimeUpdate)
		}

		state := p.State.Get()
		state.Status = workflow.Running
		p.State.Set(state)
		p.RuntimeUpdate.Set(test.heartbeat)
		if err := u.UpdatePlan(t.Context(), p); err != nil {
			t.Errorf("TestUpdatePlan(%s): got err == %s, want err == nil", test.name, err)
			continue
		}

		got, err = r.Read(t.Context(), p.ID)
		if err != nil {
			t.Fatalf("TestUpdatePlan(%s): Read after UpdatePlan: %s", test.name, err)
		}
		if !got.RuntimeUpdate.Get().Equal(test.heartbeat) {
			t.Errorf("TestUpdatePlan(%s): got RuntimeUpdate %v, want %v", test.name, got.RuntimeUpdate.Get(), test.heartbeat)
		}
		if got.State.Get().Status != workflow.Running {
			t.Errorf("TestUpdatePlan(%s): got status %v, want %v", test.name, got.State.Get().Status, workflow.Running)
		}
	}
}
