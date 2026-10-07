package sqlite

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/kylelemons/godebug/pretty"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// storedStates returns the status and attempt count of every object below p, keyed by walk position and type.
func storedStates(p *workflow.Plan) map[string]string {
	out := map[string]string{}
	i := 0
	for item := range walk.Plan(p, walk.WithSkipPlan()) {
		v := item.Value.(walk.Stateful).GetState().Status.String()
		if item.Value.Type() == workflow.OTAction {
			v = fmt.Sprintf("%s attempts=%d", v, len(item.Action().Attempts.Get()))
		}
		out[fmt.Sprintf("%02d %v", i, item.Value.Type())] = v
		i++
	}
	return out
}

func TestUpdateChanges(t *testing.T) {
	t.Parallel()

	done := workflow.State{Status: workflow.Completed, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC()}

	tests := []struct {
		name string
		// change modifies the stored Plan in place after the before Snapshot is taken.
		change func(p *workflow.Plan)
		// missing returns a changed object whose row is deleted before the update, or nil.
		missing func(p *workflow.Plan) workflow.Object
		// deletePlan deletes the whole Plan before the update.
		deletePlan bool
		// wantWrites is how many object updates the transaction runs.
		wantWrites int
		wantErr    bool
		// errIs, if set, must report true for the error.
		errIs func(error) bool
	}{
		{
			name:   "Success: nothing changed writes nothing",
			change: func(p *workflow.Plan) {},
		},
		{
			name: "Success: every changed object is stored in one transaction",
			change: func(p *workflow.Plan) {
				p.Blocks[0].State.Set(done)
				p.Blocks[0].Sequences[0].State.Set(done)
				action := p.Blocks[0].Sequences[0].Actions[0]
				action.State.Set(done)
				action.Attempts.Append(workflow.Attempt{Start: done.Start, End: done.End})
				p.DeferredActions.DeferredBatches[0].State.Set(done)
			},
			wantWrites: 4,
		},
		{
			// Blocks are written before DeferredActions, so the block is written before the batch's action fails.
			name: "Error: an object that can't be written rolls back, storing and capturing nothing",
			change: func(p *workflow.Plan) {
				p.Blocks[0].State.Set(done)
				p.DeferredActions.DeferredBatches[0].Actions[0].Attempts.Append(workflow.Attempt{Resp: make(chan int), Start: done.Start, End: done.End})
			},
			wantErr: true,
		},
		{
			// Blocks are written before DeferredActions, so the block is written before the batch's missing row.
			name: "Error: a changed object with no stored row rolls back, storing and capturing nothing",
			change: func(p *workflow.Plan) {
				p.Blocks[0].State.Set(done)
				p.DeferredActions.DeferredBatches[0].State.Set(done)
			},
			missing: func(p *workflow.Plan) workflow.Object { return p.DeferredActions.DeferredBatches[0] },
			wantErr: true,
			errIs:   errors.IsStorageInconsistent,
		},
		{
			name: "Error: a change to a deleted Plan is not found, storing and capturing nothing",
			change: func(p *workflow.Plan) {
				p.DeferredActions.DeferredBatches[0].State.Set(done)
				p.Blocks[0].Sequences[0].Actions[0].State.Set(done)
			},
			deletePlan: true,
			wantErr:    true,
			errIs:      errors.IsNotFound,
		},
	}

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	for _, test := range tests {
		pool, err := freshInMemoryPool(t)
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): freshInMemoryPool: %s", test.name, err)
		}
		defer pool.Close()

		conn, err := pool.Take(t.Context())
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): Take: %s", test.name, err)
		}
		if err := commitPlan(t.Context(), conn, plan, nil); err != nil {
			pool.Put(conn)
			t.Fatalf("TestUpdateChanges(%s): commitPlan: %s", test.name, err)
		}
		pool.Put(conn)

		rdr := reader{mu: &sync.RWMutex{}, pool: pool, reg: reg}
		stored, err := rdr.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): Read: %s", test.name, err)
		}
		before := changes.Record(stored)
		test.change(stored)
		if test.missing != nil {
			deleteRow(t, pool, test.missing(stored))
		}
		if test.deletePlan {
			if err := (deleter{mu: &sync.RWMutex{}, pool: pool}).Delete(t.Context(), stored.ID); err != nil {
				t.Fatalf("TestUpdateChanges(%s): Delete: %s", test.name, err)
			}
		}
		rowsBefore := rowStates(t, pool, stored)

		capture := &CaptureStmts{}
		u := objectUpdater{mu: &sync.RWMutex{}, pool: pool, capture: capture}
		err = u.UpdateChanges(t.Context(), stored, before)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestUpdateChanges(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestUpdateChanges(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil && test.errIs != nil && !test.errIs(err):
			t.Errorf("TestUpdateChanges(%s): got err == %s, want it classified by %s", test.name, err, funcName(test.errIs))
		}
		if got := capture.Len(); got != test.wantWrites {
			t.Errorf("TestUpdateChanges(%s): got %d object updates, want %d", test.name, got, test.wantWrites)
		}

		if test.wantErr {
			if diff := pretty.Compare(rowsBefore, rowStates(t, pool, stored)); diff != "" {
				t.Errorf("TestUpdateChanges(%s): rows changed by a failed update -before +after:\n%s", test.name, diff)
			}
			continue
		}
		reloaded, err := rdr.Read(t.Context(), plan.ID)
		if err != nil {
			t.Fatalf("TestUpdateChanges(%s): reload Read: %s", test.name, err)
		}
		if diff := pretty.Compare(storedStates(stored), storedStates(reloaded)); diff != "" {
			t.Errorf("TestUpdateChanges(%s): stored states -want +got:\n%s", test.name, diff)
		}
	}
}

// objectTables maps each object type below a Plan to its table.
var objectTables = map[workflow.ObjectType]string{
	workflow.OTCheck:           "checks",
	workflow.OTBlock:           "blocks",
	workflow.OTSequence:        "sequences",
	workflow.OTAction:          "actions",
	workflow.OTDeferredActions: "deferredactions",
	workflow.OTBatch:           "deferbatches",
}

// deleteRow deletes obj's row.
func deleteRow(t *testing.T, pool *sqlitex.Pool, obj workflow.Object) {
	t.Helper()

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("deleteRow: Take: %s", err)
	}
	defer pool.Put(conn)

	q := fmt.Sprintf("DELETE FROM %s WHERE id = $id;", objectTables[obj.Type()])
	if err := sqlitex.Execute(conn, q, &sqlitex.ExecOptions{Named: map[string]any{"$id": obj.(ider).GetID().String()}}); err != nil {
		t.Fatalf("deleteRow: %s", err)
	}
}

// rowStates returns the stored status of every object below p, read from its row, keyed by walk position and type. An
// object with no row is "missing".
func rowStates(t *testing.T, pool *sqlitex.Pool, p *workflow.Plan) map[string]string {
	t.Helper()

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("rowStates: Take: %s", err)
	}
	defer pool.Put(conn)

	out := map[string]string{}
	i := 0
	for item := range walk.Plan(p, walk.WithSkipPlan()) {
		v := "missing"
		q := fmt.Sprintf("SELECT state_status FROM %s WHERE id = $id;", objectTables[item.Value.Type()])
		err := sqlitex.Execute(conn, q, &sqlitex.ExecOptions{
			Named: map[string]any{"$id": item.Value.(ider).GetID().String()},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				v = workflow.Status(stmt.ColumnInt64(0)).String()
				return nil
			},
		})
		if err != nil {
			t.Fatalf("rowStates: %s", err)
		}
		out[fmt.Sprintf("%02d %v", i, item.Value.Type())] = v
		i++
	}
	return out
}

// TestObjectStmt checks that objectStmt writes a Plan and every type of object below it, so no update fails because a type
// was left out of its switch, and that it rejects an Item with no object instead of panicking.
func TestObjectStmt(t *testing.T) {
	t.Parallel()

	type test struct {
		name    string
		item    walk.Item
		wantErr bool
	}
	var tests []test
	i := 0
	for item := range walk.Plan(plan, walk.WithSkipPlan()) {
		tests = append(tests, test{name: fmt.Sprintf("Success: the %v at walk position %d is written.", item.Value.Type(), i), item: item})
		i++
	}
	tests = append(
		tests,
		test{name: "Success: the Plan is written.", item: walk.Item{Value: plan}},
		test{name: "Error: a zero Item is not written.", item: walk.Item{}, wantErr: true},
		test{name: "Error: a nil Plan is not written.", item: walk.Item{Value: (*workflow.Plan)(nil)}, wantErr: true},
		test{name: "Error: a nil Checks is not written.", item: walk.Item{Value: (*workflow.Checks)(nil)}, wantErr: true},
		test{name: "Error: a nil Block is not written.", item: walk.Item{Value: (*workflow.Block)(nil)}, wantErr: true},
		test{name: "Error: a nil Sequence is not written.", item: walk.Item{Value: (*workflow.Sequence)(nil)}, wantErr: true},
		test{name: "Error: a nil Action is not written.", item: walk.Item{Value: (*workflow.Action)(nil)}, wantErr: true},
		test{name: "Error: a nil DeferredActions is not written.", item: walk.Item{Value: (*workflow.DeferredActions)(nil)}, wantErr: true},
		test{name: "Error: a nil DeferBatch is not written.", item: walk.Item{Value: (*workflow.DeferBatch)(nil)}, wantErr: true},
	)

	want := map[workflow.ObjectType]bool{
		workflow.OTPlan:            true,
		workflow.OTCheck:           true,
		workflow.OTBlock:           true,
		workflow.OTSequence:        true,
		workflow.OTAction:          true,
		workflow.OTDeferredActions: true,
		workflow.OTBatch:           true,
	}
	got := map[workflow.ObjectType]bool{}
	for _, test := range tests {
		_, err := objectStmt(test.item)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestObjectStmt(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestObjectStmt(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		got[test.item.Value.Type()] = true
	}
	if diff := pretty.Compare(want, got); diff != "" {
		t.Errorf("TestObjectStmt: object types written -want +got:\n%s", diff)
	}
}

// funcName returns the name of f, for test messages.
func funcName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}
