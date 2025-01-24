package cosmosdb

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/workflow/storage"
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
	}

	for _, test := range tests {
		r := reader{
			cName: "test",
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
