package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/gostdlib/base/context"

	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

var plan *workflow.Plan

type setters interface {
	SetID(uuid.UUID)
	SetState(workflow.State)
}

type setPlanIDer interface {
	SetPlanID(uuid.UUID)
}

func init() {
	ctx := context.Background()

	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		panic(err)
	}

	checkAction1 := &workflow.Action{Name: "action", Descr: "action", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction2 := &workflow.Action{Name: "action", Descr: "action", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction3 := &workflow.Action{Name: "action", Descr: "action", Plugin: plugins.CheckPluginName, Req: nil}
	seqAction1 := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.HelloPluginName,
		Req:    plugins.HelloReq{Say: "hello"},
		Attempts: func() workflow.AtomicSlice[workflow.Attempt] {
			var a workflow.AtomicSlice[workflow.Attempt]
			a.Set(
				[]workflow.Attempt{
					{
						Err:   &pluglib.Error{Message: "internal error"},
						Start: time.Now().Add(-1 * time.Minute),
						End:   time.Now(),
					},
					{
						Resp:  plugins.HelloResp{Said: "hello"},
						Start: time.Now().Add(-1 * time.Second),
						End:   time.Now(),
					},
				},
			)
			return a
		}(),
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction1))
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 32 * time.Second})
	build.AddAction(clone.Action(ctx, checkAction2))
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction3))
	build.Up()

	build.AddBlock(builder.BlockArgs{
		Name:              "block",
		Descr:             "block",
		EntranceDelay:     1 * time.Second,
		ExitDelay:         1 * time.Second,
		ToleratedFailures: 1,
		Concurrency:       1,
	})

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction1)
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 1 * time.Minute})
	build.AddAction(checkAction2)
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(checkAction3)
	build.Up()

	build.AddSequence(&workflow.Sequence{Name: "sequence", Descr: "sequence"})
	build.AddAction(seqAction1)
	build.Up()

	plan, err = build.Plan()
	if err != nil {
		panic(err)
	}

	for item := range walk.Plan(plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		if item.Value.Type() != workflow.OTPlan {
			setter.(setPlanIDer).SetPlanID(plan.ID)
		}
		setter.SetState(
			workflow.State{
				Status: workflow.Running,
				Start:  time.Now(),
				End:    time.Now(),
			},
		)
	}
}

func mustUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id
}

var (
	dbPath string
	dbPool *sqlitex.Pool
)

func dbSetup() (path string, pool *sqlitex.Pool, err error) {
	if dbPath != "" {
		return dbPath, dbPool, nil
	}

	tmpDir := os.TempDir()
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	path = filepath.Join(tmpDir, id.String())
	pool, err = sqlitex.NewPool(
		path,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 1,
		},
	)
	if err != nil {
		return "", nil, err
	}

	conn, err := pool.Take(context.Background())
	if err != nil {
		return "", nil, err
	}
	defer pool.Put(conn)

	if err := createTables(context.Background(), conn); err != nil {
		return "", nil, err
	}

	dbPath = path
	dbPool = pool

	return path, pool, nil
}

func TestCommitPlan(t *testing.T) {
	_, pool, err := dbSetup()
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Take(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := commitPlan(context.Background(), conn, plan, nil); err != nil {
		t.Fatal(err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	// TODO(element-of-surprise): Add checks to verify the data in the database
	reader := reader{
		pool: pool,
		reg:  reg,
	}

	storedPlan, err := reader.Read(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(plan, storedPlan, cmp.AllowUnexported(workflow.Action{}, workflow.Block{}, workflow.Checks{}, workflow.Sequence{})); diff != "" {
		t.Fatalf("Read plan does not match the original plan: -want/+got:\n%s", diff)
	}
}

func TestDeletePlan(t *testing.T) {
	path, pool, err := dbSetup()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)
	defer pool.Close()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	reader := reader{
		pool: pool,
		reg:  reg,
	}

	/*
		plan, err := reader.Read(context.Background(), plan.ID)
		if err != nil {
			t.Fatalf("couldn't fetch plan: %s", err)
		}
	*/

	deleter := deleter{
		mu:     &sync.Mutex{},
		pool:   pool,
		reader: reader,
	}

	countExpect(pool, "plans", 1, t)
	mustGetcount(pool, "blocks", t)
	mustGetcount(pool, "actions", t)
	mustGetcount(pool, "checks", t)
	mustGetcount(pool, "sequences", t)

	if err := deleter.Delete(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}

	countExpect(pool, "plans", 0, t)
	countExpect(pool, "blocks", 0, t)
	countExpect(pool, "actions", 0, t)
	countExpect(pool, "checks", 0, t)
	countExpect(pool, "sequences", 0, t)
}

func mustGetcount(pool *sqlitex.Pool, table string, t *testing.T) int64 {
	c, err := countTable(pool, table)
	if err != nil {
		t.Fatal(err)
	}
	if c == 0 {
		t.Fatalf("expected at least one row in %s", table)
	}
	return c
}

func countExpect(pool *sqlitex.Pool, table string, expect int64, t *testing.T) {
	count, err := countTable(pool, table)
	if err != nil {
		t.Fatal(err)
	}
	if count != expect {
		t.Fatalf("expected %d rows in %s, got %d", expect, table, count)
	}
}

func countTable(pool *sqlitex.Pool, table string) (int64, error) {
	conn, err := pool.Take(context.Background())
	if err != nil {
		return 0, err
	}
	defer pool.Put(conn)

	q := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	var count int64
	err = sqlitex.Execute(
		conn,
		q,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.GetInt64("COUNT(*)")
				return nil
			},
		},
	)
	if err != nil {
		return 0, err
	}
	return count, nil
}
