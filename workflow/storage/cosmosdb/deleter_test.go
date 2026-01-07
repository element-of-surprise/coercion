package cosmosdb

import (
	"errors"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestDelete(t *testing.T) {
	t.Parallel()

	goodPlan := NewTestPlan()

	planWithETag := NewTestPlan()
	for item := range walk.Plan(planWithETag) {
		setter := item.Value.(setters)
		setter.SetState(
			workflow.State{
				Status: workflow.Running,
				Start:  time.Now().UTC(),
				End:    time.Now().UTC(),
				ETag:   string(azcore.ETag(planWithETag.ID.String())),
			},
		)
	}

	planWithNilChecks := NewTestPlan()
	planWithNilChecks.BypassChecks = nil
	planWithNilChecks.DeferredChecks = nil

	tests := []struct {
		name      string
		plan      *workflow.Plan
		readErr   error
		deleteErr bool
		wantErr   bool
	}{
		{
			name:    "Error: container client read error",
			plan:    goodPlan,
			readErr: errors.New("error"),
			wantErr: true,
		},
		{
			name:      "Error: container client delete error",
			plan:      goodPlan,
			deleteErr: true,
			wantErr:   true,
		},
		{
			name:    "Error: doesn't exist",
			wantErr: true,
		},
		{
			name:    "Success",
			plan:    goodPlan,
			wantErr: false,
		},
		{
			name:    "Success and etag set",
			plan:    planWithETag,
			wantErr: false,
		},
		{
			name:    "Success: not all checks defined",
			plan:    planWithNilChecks,
			wantErr: false,
		},
	}

	for _, test := range tests {
		ctx := context.Background()

		store := newFakeStorage(testReg)
		if test.plan != nil {
			if err := store.WritePlan(ctx, test.plan); err != nil {
				panic(err)
			}
		}
		store.deleteItemErr = test.deleteErr
		store.readItemErr = test.readErr

		mu := &sync.RWMutex{}
		v := &Vault{
			deleter: deleter{
				mu:     mu,
				client: store,
				reader: reader{
					mu:           mu,
					container:    "container",
					client:       store,
					defaultIOpts: &azcosmos.ItemOptions{},
					reg:          testReg,
				},
			},
		}

		testPlanID := mustUUID()
		if test.plan != nil {
			testPlanID = test.plan.GetID()
		}

		err := v.Delete(ctx, testPlanID)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestDelete(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestDelete(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
	}
}
