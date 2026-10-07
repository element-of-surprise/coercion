package sqlite

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
	"github.com/kylelemons/godebug/pretty"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
)

func TestDelete(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestDelete: New: %s", err)
	}
	defer v.Close(t.Context())

	gone := createTestPlan(t, time.Now().UTC())
	kept := createTestPlan(t, time.Now().UTC())
	if err := v.Create(t.Context(), gone); err != nil {
		t.Fatalf("TestDelete: Create: %s", err)
	}
	if err := v.Create(t.Context(), kept); err != nil {
		t.Fatalf("TestDelete: Create: %s", err)
	}
	// orphan's Plan row is gone but every row below it remains.
	orphan := createTestPlan(t, time.Now().UTC())
	if err := v.Create(t.Context(), orphan); err != nil {
		t.Fatalf("TestDelete: Create: %s", err)
	}
	conn, err := v.Pool().Take(t.Context())
	if err != nil {
		t.Fatalf("TestDelete: Take: %s", err)
	}
	err = sqlitex.Execute(conn, deletePlanByID, &sqlitex.ExecOptions{Named: map[string]any{"$id": orphan.ID.String()}})
	v.Pool().Put(conn)
	if err != nil {
		t.Fatalf("TestDelete: deleting orphan's Plan row: %s", err)
	}
	keptRows := planRows(t, v.Pool(), kept.ID)

	// cancelledGone is deleted with a ctx that is already cancelled.
	cancelledGone := createTestPlan(t, time.Now().UTC())
	if err := v.Create(t.Context(), cancelledGone); err != nil {
		t.Fatalf("TestDelete: Create: %s", err)
	}

	tests := []struct {
		name string
		id   uuid.UUID
		// cancelled calls Delete with a ctx that is already cancelled.
		cancelled bool
		wantErr   bool
	}{
		{
			name:      "Success: a plan is deleted even when ctx is cancelled, so a started delete always finishes.",
			id:        cancelledGone.ID,
			cancelled: true,
		},
		{
			name: "Success: a stored plan and every row below it are deleted.",
			id:   gone.ID,
		},
		{
			name: "Success: the rows of a plan whose plan row is missing are deleted.",
			id:   orphan.ID,
		},
		{
			name:    "Error: a plan that is not stored is not deleted.",
			id:      mustUUID(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		ctx := t.Context()
		if test.cancelled {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		err := v.Delete(ctx, test.id)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestDelete(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestDelete(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		want := map[string]int{}
		for table := range planRows(t, v.Pool(), test.id) {
			want[table] = 0
		}
		if diff := pretty.Compare(want, planRows(t, v.Pool(), test.id)); diff != "" {
			t.Errorf("TestDelete(%s): rows left for the deleted plan -want +got:\n%s", test.name, diff)
		}
	}
	if diff := pretty.Compare(keptRows, planRows(t, v.Pool(), kept.ID)); diff != "" {
		t.Errorf("TestDelete: rows of the plan that was not deleted -before +after:\n%s", diff)
	}
}

// planRows returns how many rows plan id has in each table.
func planRows(t *testing.T, pool *sqlitex.Pool, id uuid.UUID) map[string]int {
	t.Helper()

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("planRows: Take: %s", err)
	}
	defer pool.Put(conn)

	queries := map[string]string{"plans": "SELECT COUNT(*) FROM plans WHERE id = $id;"}
	for _, table := range objectTables {
		queries[table] = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE plan_id = $id;", table)
	}
	out := map[string]int{}
	for table, q := range queries {
		err := sqlitex.Execute(conn, q, &sqlitex.ExecOptions{
			Named: map[string]any{"$id": id.String()},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				out[table] = stmt.ColumnInt(0)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("planRows(%s): %s", table, err)
		}
	}
	return out
}
