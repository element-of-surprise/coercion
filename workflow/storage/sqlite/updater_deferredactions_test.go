package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
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
	t.Parallel()

	pool, err := freshInMemoryPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := commitPlan(t.Context(), conn, plan, nil); err != nil {
		pool.Put(conn)
		t.Fatalf("TestUpdateDeferredActions: commitPlan: %s", err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	rdr := reader{mu: &sync.RWMutex{}, pool: pool, reg: reg}

	stored, err := rdr.Read(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("TestUpdateDeferredActions: read failed: %s", err)
	}
	if stored.DeferredActions == nil {
		t.Fatalf("TestUpdateDeferredActions: stored plan has no DeferredActions")
	}
	if len(stored.DeferredActions.DeferredBatches) == 0 {
		t.Fatalf("TestUpdateDeferredActions: stored DeferredActions.DeferredBatches is empty")
	}

	u := objectUpdater{mu: &sync.RWMutex{}, pool: pool}

	newDAState := workflow.State{
		Status: workflow.Failed,
		Start:  time.Unix(100, 0).UTC(),
		End:    time.Unix(200, 0).UTC(),
	}
	stored.DeferredActions.State.Set(newDAState)
	if err := u.UpdateDeferredActions(t.Context(), stored.DeferredActions); err != nil {
		t.Fatalf("TestUpdateDeferredActions: UpdateDeferredActions: %s", err)
	}

	newBatchState := workflow.State{
		Status: workflow.Completed,
		Start:  time.Unix(300, 0).UTC(),
		End:    time.Unix(400, 0).UTC(),
	}
	stored.DeferredActions.DeferredBatches[0].State.Set(newBatchState)
	if err := u.UpdateDeferBatch(t.Context(), stored.DeferredActions.DeferredBatches[0]); err != nil {
		t.Fatalf("TestUpdateDeferredActions: UpdateDeferBatch: %s", err)
	}

	reloaded, err := rdr.Read(t.Context(), plan.ID)
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
	gotBatch := reloaded.DeferredActions.DeferredBatches[0].State.Get()
	if gotBatch.Status != newBatchState.Status {
		t.Errorf("TestUpdateDeferredActions: batch status got %v, want %v", gotBatch.Status, newBatchState.Status)
	}
	if !gotBatch.Start.Equal(newBatchState.Start) || !gotBatch.End.Equal(newBatchState.End) {
		t.Errorf("TestUpdateDeferredActions: batch times got (%v, %v), want (%v, %v)", gotBatch.Start, gotBatch.End, newBatchState.Start, newBatchState.End)
	}
}

// freshInMemoryPool returns an isolated sqlite pool with schema applied. It uses a file under t.TempDir to sidestep
// in-memory cache-sharing quirks.
func freshInMemoryPool(t *testing.T) (*sqlitex.Pool, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), uuid.New().String()+".db")
	pool, err := sqlitex.NewPool(
		path,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 4,
		},
	)
	if err != nil {
		return nil, err
	}
	conn, err := pool.Take(t.Context())
	if err != nil {
		pool.Close()
		return nil, err
	}
	if err := createTables(t.Context(), conn); err != nil {
		pool.Put(conn)
		pool.Close()
		return nil, err
	}
	pool.Put(conn)
	return pool, nil
}
