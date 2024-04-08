package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pluglib "github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/builder"
	"github.com/element-of-surprise/workstream/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

var plan *workflow.Plan

type setters interface {
	SetID(uuid.UUID)
	SetState(*workflow.State)
}

func init() {
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
		Attempts: []*workflow.Attempt{
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
	}

	build.AddCheck(builder.PreCheck, &workflow.Checks{})
	build.AddAction(checkAction1.Clone())
	build.Up()

	build.AddCheck(builder.ContCheck, &workflow.Checks{Delay: 32 * time.Second})
	build.AddAction(checkAction2.Clone())
	build.Up()

	build.AddCheck(builder.PostCheck, &workflow.Checks{})
	build.AddAction(checkAction3.Clone())
	build.Up()

	build.AddBlock(builder.BlockArgs{
		Name:              "block",
		Descr:             "block",
		EntranceDelay:     1 * time.Second,
		ExitDelay:         1 * time.Second,
		ToleratedFailures: 1,
		Concurrency:       1,
	})

	build.AddCheck(builder.PreCheck, &workflow.Checks{})
	build.AddAction(checkAction1)
	build.Up()

	build.AddCheck(builder.ContCheck, &workflow.Checks{Delay: 1 * time.Minute})
	build.AddAction(checkAction2)
	build.Up()

	build.AddCheck(builder.PostCheck, &workflow.Checks{})
	build.AddAction(checkAction3)
	build.Up()

	build.AddSequence(&workflow.Sequence{Name: "sequence", Descr: "sequence"})
	build.AddAction(seqAction1)
	build.Up()

	plan, err = build.Plan()
	if err != nil {
		panic(err)
	}

	for item := range walk.Plan(context.Background(), plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		setter.SetState(
			&workflow.State{
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

func dbSetup() (path string, conn *sqlite.Conn, err error) {
	tmpDir := os.TempDir()
	id := uuid.New()
	path = filepath.Join(tmpDir, id.String())
	conn, err = sqlite.OpenConn(path, sqlite.OpenReadWrite, sqlite.OpenCreate)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	if err := createTables(context.Background(), conn); err != nil {
		return "", nil, err
	}

	return path, conn, nil
}

func TestCommitPlan(t *testing.T) {
	path, conn, err := dbSetup()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)
	defer conn.Close()

	if err := commitPlan(context.Background(), conn, plan); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	// TODO(element-of-surprise): Add checks to verify the data in the database
	reader := &planReader{
		conn: conn,
		reg:  reg,
	}

	storedPlan, err := reader.Read(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(plan, storedPlan, cmp.AllowUnexported(workflow.Action{})); diff != "" {
		t.Fatalf("Read plan does not match the original plan: -want/+got:\n%s", diff)
	}
}