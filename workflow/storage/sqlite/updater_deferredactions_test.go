package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
)

// TestUpdateDeferredActions exercises UpdateDeferredActions and UpdateDeferBatch
// against the same db that TestCommitPlan populated — it mutates the state of
// the DeferredActions container and one of its batches, writes the update, and
// reads the plan back to confirm the state round-tripped.
func TestUpdateDeferredActions(t *testing.T) {
	pool, cleanup, err := freshInMemoryPool()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	conn, err := pool.Take(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := commitPlan(context.Background(), conn, plan, nil); err != nil {
		pool.Put(conn)
		t.Fatalf("TestUpdateDeferredActions: commitPlan: %s", err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	rdr := reader{pool: pool, reg: reg}

	stored, err := rdr.Read(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("TestUpdateDeferredActions: read failed: %s", err)
	}
	if stored.DeferredActions == nil {
		t.Fatalf("TestUpdateDeferredActions: stored plan has no DeferredActions")
	}
	if len(stored.DeferredActions.OnFailure) == 0 {
		t.Fatalf("TestUpdateDeferredActions: stored DeferredActions.OnFailure is empty")
	}

	mu := &sync.Mutex{}
	daU := deferredActionsUpdater{mu: mu, pool: pool}
	bU := deferBatchUpdater{mu: mu, pool: pool}

	newDAState := workflow.State{
		Status: workflow.Failed,
		Start:  time.Unix(100, 0).UTC(),
		End:    time.Unix(200, 0).UTC(),
	}
	stored.DeferredActions.State.Set(newDAState)
	if err := daU.UpdateDeferredActions(context.Background(), stored.DeferredActions); err != nil {
		t.Fatalf("TestUpdateDeferredActions: UpdateDeferredActions: %s", err)
	}

	newBatchState := workflow.State{
		Status: workflow.Completed,
		Start:  time.Unix(300, 0).UTC(),
		End:    time.Unix(400, 0).UTC(),
	}
	stored.DeferredActions.OnFailure[0].State.Set(newBatchState)
	if err := bU.UpdateDeferBatch(context.Background(), stored.DeferredActions.OnFailure[0]); err != nil {
		t.Fatalf("TestUpdateDeferredActions: UpdateDeferBatch: %s", err)
	}

	reloaded, err := rdr.Read(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("TestUpdateDeferredActions: reload read failed: %s", err)
	}
	gotDA := reloaded.DeferredActions.State.Get()
	if gotDA.Status != newDAState.Status {
		t.Errorf("TestUpdateDeferredActions: DA status got %v, want %v", gotDA.Status, newDAState.Status)
	}
	if !gotDA.Start.Equal(newDAState.Start) || !gotDA.End.Equal(newDAState.End) {
		t.Errorf("TestUpdateDeferredActions: DA times got (%v, %v), want (%v, %v)", gotDA.Start, gotDA.End, newDAState.Start, newDAState.End)
	}
	gotBatch := reloaded.DeferredActions.OnFailure[0].State.Get()
	if gotBatch.Status != newBatchState.Status {
		t.Errorf("TestUpdateDeferredActions: batch status got %v, want %v", gotBatch.Status, newBatchState.Status)
	}
	if !gotBatch.Start.Equal(newBatchState.Start) || !gotBatch.End.Equal(newBatchState.End) {
		t.Errorf("TestUpdateDeferredActions: batch times got (%v, %v), want (%v, %v)", gotBatch.Start, gotBatch.End, newBatchState.Start, newBatchState.End)
	}
}

// freshInMemoryPool returns an isolated sqlite pool with schema applied, for
// tests that don't want to share dbPool (which TestDeletePlan closes). It uses
// a file under t.TempDir to sidestep in-memory cache-sharing quirks.
func freshInMemoryPool() (*sqlitex.Pool, func(), error) {
	dir, err := os.MkdirTemp("", "coercion-sqlite-*")
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, uuid.New().String()+".db")
	pool, err := sqlitex.NewPool(
		path,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 4,
		},
	)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	conn, err := pool.Take(context.Background())
	if err != nil {
		pool.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}
	if err := createTables(context.Background(), conn); err != nil {
		pool.Put(conn)
		pool.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}
	pool.Put(conn)
	return pool, func() { pool.Close(); os.RemoveAll(dir) }, nil
}
