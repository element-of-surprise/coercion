package cosmosdb

import (
	"testing"

	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/workflow"
)

func TestObjsToIDs(t *testing.T) {
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
		name    string
		objs    []any
		want    []uuid.UUID
		wantErr bool
	}{
		{
			name:    "Success: empty objects",
			wantErr: false,
		},
		{
			name:    "Success: single sequence",
			objs:    []any{&workflow.Sequence{ID: id1}},
			want:    []uuid.UUID{id1},
			wantErr: false,
		},
		{
			name:    "Success: multiple blocks",
			objs:    []any{&workflow.Block{ID: id1}, &workflow.Block{ID: id2}},
			want:    []uuid.UUID{id1, id2},
			wantErr: false,
		},
		{
			name:    "Success: multiple actions",
			objs:    []any{&workflow.Action{ID: id1}, &workflow.Action{ID: id2}},
			want:    []uuid.UUID{id1, id2},
			wantErr: false,
		},
		{
			name:    "Error: type does not implement ider interface",
			objs:    []any{workflow.Block{ID: id1}, workflow.Block{ID: id2}},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Error: not all types implement ider interface",
			objs:    []any{&workflow.Block{ID: id1}, workflow.Action{ID: id2}},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Error: object is missing ID",
			objs:    []any{&workflow.Action{ID: id1}, &workflow.Action{}},
			want:    nil,
			wantErr: true,
		},
	}

	for _, test := range tests {
		ids, err := objsToIDs(test.objs)
		switch {
		case test.wantErr && err == nil:
			t.Errorf("TestObjsToIDs(%s): got err == nil, want err != nil", test.name)
			continue
		case !test.wantErr && err != nil:
			t.Errorf("TestObjsToIDs(%s): got err != %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if diff := pretty.Compare(test.want, ids); diff != "" {
			t.Errorf("TestObjsToIDs(%s): returned ids: -want/+got:\n%s", test.name, diff)
		}
	}
}
