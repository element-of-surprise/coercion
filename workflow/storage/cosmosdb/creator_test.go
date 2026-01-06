package cosmosdb

import (
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

func TestCreate(t *testing.T) {
	t.Parallel()

	existingPlan := NewTestPlan()
	goodPlan := NewTestPlan()

	badPlan := NewTestPlan()
	badPlan.ID = uuid.Nil

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

	planWithNilBlock := NewTestPlan()
	planWithNilBlock.Blocks[0] = nil

	planWithNilBlocks := NewTestPlan()
	planWithNilBlocks.Blocks = nil

	planWithBadIDs := NewTestPlan()
	planWithBadIDs.Blocks[0].ID = uuid.Nil

	tests := []struct {
		name      string
		plan      *workflow.Plan
		readErr   error
		exists    bool
		fetchErr  bool
		createErr bool

		wantErr bool
	}{
		/*
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
				name:    "Error: cosmosdb read error on Exists()",
				plan:    goodPlan,
				readErr: errors.New("error"),
				wantErr: true,
			},
			{
				name:      "Error: container client create error",
				plan:      goodPlan,
				createErr: true,
				wantErr:   true,
			},
			{
				name:    "Error: plan exists",
				plan:    existingPlan,
				exists:  true,
				wantErr: true,
			},
		*/
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
		{
			name:    "Error: nil block",
			plan:    planWithNilBlock,
			wantErr: true,
		},
		{
			name:    "Error: nil blocks",
			plan:    planWithNilBlocks,
			wantErr: true,
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

		store := newFakeStorage(testReg)
		store.WritePlan(ctx, existingPlan)
		store.readItemErr = test.readErr
		store.createItemErr = test.createErr
		mu := &sync.RWMutex{}

		v := &Vault{
			creator: creator{
				mu: mu,
				reader: reader{
					mu:           mu,
					container:    "container",
					client:       store,
					defaultIOpts: &azcosmos.ItemOptions{},
					reg:          testReg,
				},
				client: store,
			},
		}

		err := v.Create(ctx, test.plan)
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
