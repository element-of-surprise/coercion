package cosmosdb

import (
	"fmt"
	"testing"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/kylelemons/godebug/pretty"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
)

func TestBuildSearchQuery(t *testing.T) {
	t.Parallel()

	id1, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		filters    storage.Filters
		wantQuery  string
		wantParams []azcosmos.QueryParameter
	}{
		{
			name:      "Success: empty filters",
			filters:   storage.Filters{},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
			},
		},
		{
			name: "Success: by IDs with single ID",
			filters: storage.Filters{
				ByIDs: []uuid.UUID{
					id1,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND ARRAY_CONTAINS(@ids, c.id) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name: "@ids",
					Value: []uuid.UUID{
						id1,
					},
				},
			},
		},
		{
			name: "Success: by IDs with multiple IDs",
			filters: storage.Filters{
				ByIDs: []uuid.UUID{
					id1,
					id2,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND ARRAY_CONTAINS(@ids, c.id) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name: "@ids",
					Value: []uuid.UUID{
						id1,
						id2,
					},
				},
			},
		},
		{
			name: "Success: by IDs with single Group ID",
			filters: storage.Filters{
				ByGroupIDs: []uuid.UUID{
					id1,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND ARRAY_CONTAINS(@group_ids, c.groupID) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name: "@group_ids",
					Value: []uuid.UUID{
						id1,
					},
				},
			},
		},
		{
			name: "Success: by IDs with multiple Group IDs",
			filters: storage.Filters{
				ByGroupIDs: []uuid.UUID{
					id1,
					id2,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND ARRAY_CONTAINS(@group_ids, c.groupID) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name: "@group_ids",
					Value: []uuid.UUID{
						id1,
						id2,
					},
				},
			},
		},
		{
			name: "Success: by Status with single Status",
			filters: storage.Filters{
				ByStatus: []workflow.Status{
					workflow.Completed,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND c.stateStatus = @status0 ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name:  "@status0",
					Value: workflow.Completed,
				},
			},
		},
		{
			name: "Success: by Status with multiple Statuses",
			filters: storage.Filters{
				ByStatus: []workflow.Status{
					workflow.Completed,
					workflow.Failed,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND (c.stateStatus = @status0 OR c.stateStatus = @status1) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name:  "@status0",
					Value: workflow.Completed,
				},
				{
					Name:  "@status1",
					Value: workflow.Failed,
				},
			},
		},

		{
			name: "Success: with multiple filters",
			filters: storage.Filters{
				ByIDs: []uuid.UUID{
					id1,
				},
				ByGroupIDs: []uuid.UUID{
					id2,
				},
				ByStatus: []workflow.Status{
					workflow.Completed,
					workflow.Failed,
				},
			},
			wantQuery: `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm AND ARRAY_CONTAINS(@ids, c.id) AND ARRAY_CONTAINS(@group_ids, c.groupID) AND (c.stateStatus = @status0 OR c.stateStatus = @status1) ORDER BY c.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
				{
					Name:  "@swarm",
					Value: swarm,
				},
				{
					Name:  "@status0",
					Value: workflow.Completed,
				},
				{
					Name:  "@status1",
					Value: workflow.Failed,
				},
				{
					Name: "@ids",
					Value: []uuid.UUID{
						id1,
					},
				},
				{
					Name: "@group_ids",
					Value: []uuid.UUID{
						id2,
					},
				},
			},
		},
	}

	for _, test := range tests {
		r := reader{
			swarm:     swarm,
			container: "test",
		}
		query, params := r.buildSearchQuery(test.filters)
		if test.wantQuery != query {
			t.Errorf("TestBuildSearchQuery(%s): got query == %s, want query == %s", test.name, query, test.wantQuery)
			continue
		}
		if diff := pretty.Compare(test.wantParams, params); diff != "" {
			t.Errorf("TestBuildSearchQuery(%s): returned params: -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestExists(t *testing.T) {
	t.Parallel()

	store := newFakeStorage(nil)

	tp := NewTestPlan()
	if err := store.WritePlan(context.Background(), tp); err != nil {
		panic(err)
	}

	tests := []struct {
		name    string
		id      uuid.UUID
		err     error
		want    bool
		wantErr bool
	}{
		{
			name:    "Error: container client error",
			id:      mustUUID(),
			err:     fmt.Errorf("test error"),
			want:    false,
			wantErr: true,
		},
		{
			name:    "Success: plan doesn't exist",
			id:      mustUUID(),
			want:    false,
			wantErr: false,
		},
		{
			name:    "Success: exists",
			id:      tp.GetID(),
			want:    true,
			wantErr: false,
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		store.readItemErr = test.err
		r := reader{
			mu:     &sync.RWMutex{},
			client: store,
		}

		result, err := r.Exists(ctx, test.id)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestExists(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestExists(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if test.want != result {
			t.Errorf("TestExists(%s): got exists == %t, want exists == %t", test.name, result, test.want)
			continue
		}
	}
}

func TestRead(t *testing.T) {
	t.Parallel()

	store := newFakeStorage(testReg)

	tp := NewTestPlan()
	if err := store.WritePlan(t.Context(), tp); err != nil {
		t.Fatalf("TestRead: WritePlan: %s", err)
	}

	tests := []struct {
		name   string
		planID uuid.UUID
		// deleteSubDoc deletes the plan's PreChecks document first. The plan is shared, so this row must come last.
		deleteSubDoc bool
		// wantNotFound means errors.IsNotFound(err), which callers such as Wait and Resume rely on.
		wantNotFound     bool
		wantInconsistent bool
		wantErr          bool
	}{
		{
			// Regression: a missing plan came back as a plain storage error that callers could not tell from a
			// transient failure.
			name:         "Error: a plan that doesn't exist is a not-found error",
			planID:       mustUUID(),
			wantNotFound: true,
			wantErr:      true,
		},
		{
			name:   "Success: a stored plan is read back unchanged",
			planID: tp.GetID(),
		},
		{
			// Regression: a plan missing one sub-document was reported as a missing plan, and callers treated that as
			// permanent and gave up on a plan whose document still exists.
			name:             "Error: a plan with a missing sub-document is damaged storage, not a missing plan",
			planID:           tp.GetID(),
			deleteSubDoc:     true,
			wantInconsistent: true,
			wantErr:          true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()

		r := reader{
			mu:     &sync.RWMutex{},
			client: store,
			reg:    testReg,
		}
		if test.deleteSubDoc {
			if err := store.deleteItem(ctx, tp.PreChecks.ID.String()); err != nil {
				t.Fatalf("TestRead(%s): deleting PreChecks: %s", test.name, err)
			}
		}
		result, err := r.Read(ctx, test.planID)
		if got := errors.IsNotFound(err); got != test.wantNotFound {
			t.Errorf("TestRead(%s): got errors.IsNotFound(err) == %v, want %v", test.name, got, test.wantNotFound)
		}
		if got := errors.IsStorageInconsistent(err); got != test.wantInconsistent {
			t.Errorf("TestRead(%s): got errors.IsStorageInconsistent(err) == %v, want %v", test.name, got, test.wantInconsistent)
		}
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestRead(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestRead(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if diff := prettyConfig.Compare(tp, result); diff != "" {
			t.Errorf("TestRead(%s): returned params: -want/+got:\n%s", test.name, diff)
			continue
		}
	}
}

// TestReadMissingAction verifies that a query returning fewer Actions than its parent references is reported as
// inconsistent storage instead of silently returning a truncated Plan.
func TestReadMissingAction(t *testing.T) {
	t.Parallel()

	store := newFakeStorage(testReg)
	plan := NewTestPlan()
	if err := store.WritePlan(t.Context(), plan); err != nil {
		t.Fatalf("TestReadMissingAction: WritePlan: %s", err)
	}
	missing := plan.PreChecks.Actions[0].ID
	if err := store.deleteItem(t.Context(), missing.String()); err != nil {
		t.Fatalf("TestReadMissingAction: deleting Action(%s): %s", missing, err)
	}

	r := reader{
		mu:     &sync.RWMutex{},
		client: store,
		reg:    testReg,
	}
	_, err := r.Read(t.Context(), plan.ID)
	if !errors.IsStorageInconsistent(err) {
		t.Errorf("TestReadMissingAction: got err == %v, want a storage-inconsistent error", err)
	}
	if errors.IsNotFound(err) {
		t.Errorf("TestReadMissingAction: got errors.IsNotFound(err) == true, want false")
	}
}
