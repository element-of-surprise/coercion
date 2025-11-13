package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestReaderList(t *testing.T) {
	// Create a new database for this test
	tmpDir := os.TempDir()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestReaderList: couldn't generate UUID: %s", err)
	}
	dbPath := filepath.Join(tmpDir, id.String())
	defer os.RemoveAll(dbPath)

	pool, err := sqlitex.NewPool(
		dbPath,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 1,
		},
	)
	if err != nil {
		t.Fatalf("TestReaderList: couldn't create pool: %s", err)
	}
	defer pool.Close()

	// Create tables
	conn, err := pool.Take(context.Background())
	if err != nil {
		t.Fatalf("TestReaderList: couldn't get connection: %s", err)
	}

	if err := createTables(context.Background(), conn); err != nil {
		pool.Put(conn)
		t.Fatalf("TestReaderList: couldn't create tables: %s", err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	reader := reader{
		pool: pool,
		reg:  reg,
	}

	ctx := t.Context()

	// Create three plans with different submit times
	plans := make([]*workflow.Plan, 3)
	now := time.Now()

	for i := 0; i < 3; i++ {
		p := createTestPlan(t, now.Add(time.Duration(-2+i)*time.Hour))
		plans[i] = p

		// Insert plan into database
		conn, err := pool.Take(ctx)
		if err != nil {
			t.Fatalf("TestReaderList: couldn't get connection: %s", err)
		}

		if err := commitPlan(ctx, conn, p, nil); err != nil {
			pool.Put(conn)
			t.Fatalf("TestReaderList: couldn't commit plan %d: %s", i, err)
		}
		pool.Put(conn)
	}

	tests := []struct {
		name      string
		limit     int
		wantCount int
		wantOrder []int // indices into plans array showing expected order
		wantErr   bool
	}{
		{
			name:      "Success: List all plans without limit",
			limit:     0,
			wantCount: 3,
			wantOrder: []int{2, 1, 0}, // newest to oldest
			wantErr:   false,
		},
		{
			name:      "Success: List with limit 2",
			limit:     2,
			wantCount: 2,
			wantOrder: []int{2, 1}, // newest two
			wantErr:   false,
		},
		{
			name:      "Success: List with limit 1",
			limit:     1,
			wantCount: 1,
			wantOrder: []int{2}, // only newest
			wantErr:   false,
		},
		{
			name:      "Success: List with limit larger than plan count",
			limit:     10,
			wantCount: 3,
			wantOrder: []int{2, 1, 0}, // all plans
			wantErr:   false,
		},
	}

	for _, test := range tests {
		ch, err := reader.List(ctx, test.limit)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestReaderList(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestReaderList(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		var results []storage.ListResult
		for item := range ch {
			if item.Err != nil {
				t.Errorf("TestReaderList(%s): got error in stream: %s", test.name, item.Err)
				continue
			}
			results = append(results, item.Result)
		}

		if len(results) != test.wantCount {
			t.Errorf("TestReaderList(%s): got %d results, want %d", test.name, len(results), test.wantCount)
			continue
		}

		// Verify order and contents
		for i, wantIdx := range test.wantOrder {
			wantPlan := plans[wantIdx]
			gotResult := results[i]

			if gotResult.ID != wantPlan.ID {
				t.Errorf("TestReaderList(%s): result[%d].ID = %s, want %s", test.name, i, gotResult.ID, wantPlan.ID)
			}
			if gotResult.GroupID != wantPlan.GroupID {
				t.Errorf("TestReaderList(%s): result[%d].GroupID = %s, want %s", test.name, i, gotResult.GroupID, wantPlan.GroupID)
			}
			if gotResult.Name != wantPlan.Name {
				t.Errorf("TestReaderList(%s): result[%d].Name = %s, want %s", test.name, i, gotResult.Name, wantPlan.Name)
			}
			if gotResult.Descr != wantPlan.Descr {
				t.Errorf("TestReaderList(%s): result[%d].Descr = %s, want %s", test.name, i, gotResult.Descr, wantPlan.Descr)
			}
			if !gotResult.SubmitTime.Equal(wantPlan.SubmitTime) {
				t.Errorf("TestReaderList(%s): result[%d].SubmitTime = %s, want %s", test.name, i, gotResult.SubmitTime, wantPlan.SubmitTime)
			}
			if gotResult.State.Status != wantPlan.State.Status {
				t.Errorf("TestReaderList(%s): result[%d].State.Status = %v, want %v", test.name, i, gotResult.State.Status, wantPlan.State.Status)
			}
		}

		// Additional verification: ensure results are in descending order by submit time
		for i := 1; i < len(results); i++ {
			if results[i].SubmitTime.After(results[i-1].SubmitTime) {
				t.Errorf("TestReaderList(%s): results not in descending order: result[%d].SubmitTime (%s) is after result[%d].SubmitTime (%s)",
					test.name, i, results[i].SubmitTime, i-1, results[i-1].SubmitTime)
			}
		}
	}
}

// createTestPlan creates a test plan with all required fields properly initialized.
func createTestPlan(t *testing.T, submitTime time.Time) *workflow.Plan {
	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		t.Fatalf("createTestPlan: couldn't create builder: %s", err)
	}

	checkAction := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.CheckPluginName,
		Req:    nil,
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction)
	build.Up()

	p, err := build.Plan()
	if err != nil {
		t.Fatalf("createTestPlan: couldn't build plan: %s", err)
	}

	// Set IDs and states for all objects
	p.SetID(mustUUID())
	p.SubmitTime = submitTime
	p.SetState(&workflow.State{
		Status: workflow.Running,
		Start:  submitTime,
		End:    time.Time{},
	})

	// Set IDs and states for checks
	if p.PreChecks != nil {
		p.PreChecks.SetID(mustUUID())
		p.PreChecks.Key = mustUUID()
		p.PreChecks.SetState(&workflow.State{
			Status: workflow.Running,
			Start:  submitTime,
			End:    time.Time{},
		})
		for _, action := range p.PreChecks.Actions {
			action.SetID(mustUUID())
			action.SetPlanID(p.ID)
			action.SetState(&workflow.State{
				Status: workflow.Running,
				Start:  submitTime,
				End:    time.Time{},
			})
		}
	}

	return p
}
