package cosmosdb

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
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
			name:       "Success: empty filters",
			filters:    storage.Filters{},
			wantQuery:  `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ORDER BY p.submitTime DESC`,
			wantParams: nil,
		},
		{
			name: "Success: by IDs with single ID",
			filters: storage.Filters{
				ByIDs: []uuid.UUID{
					id1,
				},
			},
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ARRAY_CONTAINS(@ids, p.id) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ARRAY_CONTAINS(@ids, p.id) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ARRAY_CONTAINS(@group_ids, p.groupID) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ARRAY_CONTAINS(@group_ids, p.groupID) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND p.stateStatus = @status0 ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND (p.stateStatus = @status0 OR p.stateStatus = @status1) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
			wantQuery: `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM test p WHERE p.type=1 AND ARRAY_CONTAINS(@ids, p.id) AND ARRAY_CONTAINS(@group_ids, p.groupID) AND (p.stateStatus = @status0 OR p.stateStatus = @status1) ORDER BY p.submitTime DESC`,
			wantParams: []azcosmos.QueryParameter{
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
	if err := store.WritePlan(context.Background(), "somekey", tp); err != nil {
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
	if err := store.WritePlan(context.Background(), "somekey", tp); err != nil {
		panic(err)
	}

	tests := []struct {
		name    string
		planID  uuid.UUID
		wantErr bool
	}{
		{
			name:    "Error: plan doesn't exist",
			planID:  mustUUID(),
			wantErr: true,
		},
		{
			name:   "Success",
			planID: tp.GetID(),
		},
	}

	for _, test := range tests {
		ctx := context.Background()

		r := reader{
			mu:     &sync.RWMutex{},
			client: store,
			reg:    testReg,
		}
		result, err := r.Read(ctx, test.planID)
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
		if diff := pretty.Compare(tp, result); diff != "" {
			t.Errorf("TestRead(%s): returned params: -want/+got:\n%s", test.name, diff)
			continue
		}
	}
}
