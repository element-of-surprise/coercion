package cosmosdb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/google/uuid"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestCreate(t *testing.T) {
	t.Parallel()

	existingPlan := NewTestPlan()
	goodPlan := NewTestPlan()

	badPlan := NewTestPlan()
	badPlan.ID = uuid.Nil

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

	planWithNilBlock := NewTestPlan()
	planWithNilBlock.Blocks[0] = nil

	planWithNilBlocks := NewTestPlan()
	planWithNilBlocks.Blocks = nil

	planWithBadIDs := NewTestPlan()
	planWithBadIDs.Blocks[0].ID = uuid.Nil

	tests := []struct {
		name        string
		plan        *workflow.Plan
		readErr     error
		createErr   error
		enforceETag bool
		wantErr     bool
	}{
		{
			name:    "Error: plan is nil",
			plan:    nil,
			wantErr: true,
		},
		{
			name:    "Error: plan ID is nil",
			plan:    badPlan,
			wantErr: true,
		},
		{
			name:    "Error: container client read error",
			plan:    goodPlan,
			readErr: fmt.Errorf("test error"),
			wantErr: true,
		},
		{
			name:      "Error: container client create error",
			plan:      goodPlan,
			createErr: fmt.Errorf("test error"),
			wantErr:   true,
		},
		{
			name:    "Error: plan exists",
			plan:    existingPlan,
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
			name:    "Error: nil block",
			plan:    planWithNilBlock,
			wantErr: true,
		},
		{
			name:    "Success: nil blocks",
			plan:    planWithNilBlocks,
			wantErr: false,
		},
		{
			name:    "Error: nil block ID",
			plan:    planWithBadIDs,
			wantErr: true,
		},
		// could test with bad plan data, like invalid list of actions, attempts encoding issue, etc.,
		// to make sure it causes the entire plan creation to fail.
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
		}
		r.reader = reader{cName: cName, Client: cc, reg: reg}
		r.creator = creator{mu: mu, Client: cc, reader: r.reader}
		r.updater = newUpdater(mu, cc, r.reader)
		r.closer = closer{Client: cc}
		r.deleter = deleter{mu: mu, Client: cc, reader: r.reader}
		if err := r.Create(ctx, existingPlan); err != nil {
			t.Fatalf("TestExists(%s): %s", test.name, err)
		}
		if test.readErr != nil {
			cc.client.readErr = test.readErr
		}
		if test.createErr != nil {
			cc.createErr = test.createErr
		}

		err = r.Create(ctx, test.plan)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestCreate(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestCreate(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
	}
}
