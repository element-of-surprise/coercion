package cosmosdb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestDelete(t *testing.T) {
	t.Parallel()

	goodPlan := NewTestPlan()

	planWithETag := NewTestPlan()
	for item := range walk.Plan(context.Background(), planWithETag) {
		setter := item.Value.(setters)
		setter.SetState(
			&workflow.State{
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

	planWithNilBlocks := NewTestPlan()
	planWithNilBlocks.Blocks = nil

	tests := []struct {
		name        string
		plan        *workflow.Plan
		readErr     error
		deleteErr   error
		enforceETag bool
		wantErr     bool
	}{
		{
			name:    "Error: container client read error",
			plan:    goodPlan,
			readErr: fmt.Errorf("test error"),
			wantErr: true,
		},
		{
			name:      "Error: container client delete error",
			plan:      goodPlan,
			deleteErr: fmt.Errorf("test error"),
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
			name:        "Success with enforce etag and no etag set",
			plan:        goodPlan,
			enforceETag: true,
			wantErr:     false,
		},
		{
			name:        "Success with enforce etag and etag set",
			plan:        planWithETag,
			enforceETag: true,
			wantErr:     false,
		},
		{
			name:    "Success: not all checks defined",
			plan:    planWithNilChecks,
			wantErr: false,
		},
		{
			name:    "Success: nil blocks",
			plan:    planWithNilBlocks,
			wantErr: false,
		},
	}

	for _, test := range tests {
		ctx := context.Background()

		cName := "test"

		reg := registry.New()
		reg.MustRegister(&plugins.CheckPlugin{})
		reg.MustRegister(&plugins.HelloPlugin{})

		cc, err := NewFakeCosmosDBClient(test.enforceETag)
		if err != nil {
			t.Fatal(err)
		}
		mu := &sync.Mutex{}
		r := Vault{
			dbName:       "test-db",
			cName:        "test-container",
			partitionKey: "test-partition",
			enforceETag:  test.enforceETag,
		}
		r.reader = reader{cName: cName, Client: cc, reg: reg}
		r.creator = creator{mu: mu, Client: cc, reader: r.reader}
		r.updater = newUpdater(mu, cc, r.reader)
		r.closer = closer{Client: cc}
		r.deleter = deleter{mu: mu, Client: cc, reader: r.reader}
		testPlanID := mustUUID()
		if test.plan != nil {
			err = r.Create(ctx, test.plan)
			if err != nil {
				t.Fatalf("TestDelete(%s): %s", test.name, err)
			}
			testPlanID = test.plan.ID
		}

		if test.readErr != nil {
			cc.client.readErr = test.readErr
		}
		if test.deleteErr != nil {
			cc.deleteErr = test.deleteErr
		}

		err = r.Delete(ctx, testPlanID)
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
