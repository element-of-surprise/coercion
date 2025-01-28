package cosmosdb

import (
	"context"
	"fmt"
	"sync"
	"testing"

	// "github.com/google/uuid"
	// "github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	// "github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
)

func TestCreate(t *testing.T) {
	t.Parallel()

	plan0 := NewTestPlan()
	// plan1 := NewTestPlan()

	tests := []struct {
		name      string
		plan      *workflow.Plan
		readErr   error
		createErr error
		wantErr   bool
	}{
		{
			name:    "Error: container client read error",
			plan:    plan1,
			readErr: fmt.Errorf("test error"),
			wantErr: true,
		},
		{
			name:      "Error: container client create error",
			plan:      plan1,
			createErr: fmt.Errorf("test error"),
			wantErr:   true,
		},
		{
			name:    "Error: plan exists",
			plan:    plan0,
			wantErr: true,
		},
		{
			name:    "Success",
			plan:    plan1,
			wantErr: false,
		},
	}

	for _, test := range tests {
		ctx := context.Background()

		cName := "test"

		reg := registry.New()
		reg.MustRegister(&plugins.CheckPlugin{})
		reg.MustRegister(&plugins.HelloPlugin{})

		cc, err := NewFakeCosmosDBClient()
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
		if err := r.Create(ctx, plan0); err != nil {
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
