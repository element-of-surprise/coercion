package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// plansBeforeRuntimeUpdate is the plans table as databases created before the runtime_update column have it.
const plansBeforeRuntimeUpdate = `
CREATE Table plans (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL,
	name TEXT NOT NULL,
	descr TEXT NOT NULL,
	meta BLOB,
	bypasschecks TEXT,
	prechecks TEXT,
	postchecks TEXT,
	contchecks TEXT,
	deferredchecks TEXT,
	deferredactions TEXT,
	blocks BLOB NOT NULL,
	state_status INTEGER NOT NULL,
	state_start INTEGER NOT NULL,
	state_end INTEGER NOT NULL,
	submit_time INTEGER NOT NULL,
	reason INTEGER
);`

// oldPlanID is the Plan row written into a database before it has the runtime_update column.
const oldPlanID = "0191d3a4-0000-7000-8000-000000000001"

func TestCreateTables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup prepares the database before createTables runs on it. An error fails the row's setup.
		setup func(t *testing.T, conn *sqlite.Conn) error
		// wantOldRow is whether the Plan row setup wrote must still be there, with no runtime update.
		wantOldRow bool
	}{
		{
			name:  "Success: a new database gets the runtime_update column.",
			setup: func(t *testing.T, conn *sqlite.Conn) error { return nil },
		},
		{
			// Regression: CREATE TABLE IF NOT EXISTS left an existing plans table without runtime_update, so the
			// Plan heartbeat was never stored and startup recovery aged out Plans that were still running.
			name: "Success: a database created before runtime_update gets the column and keeps its Plans.",
			setup: func(t *testing.T, conn *sqlite.Conn) error {
				execSQL(t, conn, plansBeforeRuntimeUpdate)
				execSQL(
					t,
					conn,
					`INSERT INTO plans (id, group_id, name, descr, blocks, state_status, state_start, state_end, submit_time, reason)
					VALUES ('`+oldPlanID+`', '', 'old', 'old', '[]', 2, 1, 0, 1, 0);`,
				)
				return nil
			},
			wantOldRow: true,
		},
		{
			name: "Success: a database that already has the column is left as it is.",
			setup: func(t *testing.T, conn *sqlite.Conn) error {
				return createTables(t.Context(), conn)
			},
		},
	}

	for _, test := range tests {
		path := filepath.Join(t.TempDir(), uuid.New().String()+".db")
		conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
		if err != nil {
			t.Fatalf("TestCreateTables(%s): OpenConn: %s", test.name, err)
		}
		t.Cleanup(func() { conn.Close() })

		if err := test.setup(t, conn); err != nil {
			t.Fatalf("TestCreateTables(%s): setup: %s", test.name, err)
		}

		if err := createTables(t.Context(), conn); err != nil {
			t.Errorf("TestCreateTables(%s): got err == %s, want err == nil", test.name, err)
			continue
		}

		has, err := hasColumn(conn, "plans", "runtime_update")
		if err != nil {
			t.Fatalf("TestCreateTables(%s): hasColumn: %s", test.name, err)
		}
		if !has {
			t.Errorf("TestCreateTables(%s): plans has no runtime_update column, want it", test.name)
			continue
		}

		if !test.wantOldRow {
			continue
		}
		found := false
		var runtimeUpdate int64
		err = sqlitex.Execute(
			conn,
			`SELECT runtime_update FROM plans WHERE id = $id;`,
			&sqlitex.ExecOptions{
				Named: map[string]any{"$id": oldPlanID},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					found = true
					runtimeUpdate = stmt.GetInt64("runtime_update")
					return nil
				},
			},
		)
		if err != nil {
			t.Fatalf("TestCreateTables(%s): reading the old Plan row: %s", test.name, err)
		}
		if !found {
			t.Errorf("TestCreateTables(%s): the Plan row stored before the migration is gone", test.name)
			continue
		}
		if runtimeUpdate != 0 {
			t.Errorf("TestCreateTables(%s): got runtime_update %d on the old Plan row, want 0", test.name, runtimeUpdate)
		}
	}
}

// execSQL runs a statement that returns no rows.
func execSQL(t *testing.T, conn *sqlite.Conn, query string) {
	t.Helper()
	if err := sqlitex.ExecuteTransient(conn, query, nil); err != nil {
		t.Fatalf("execSQL(%s): %s", query, err)
	}
}
