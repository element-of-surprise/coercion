package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/builder"
	"github.com/element-of-surprise/workstream/workflow/utils/walk"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

var plan *workflow.Plan

type fakeReq struct {
	Say string
}

type setters interface {
	SetID(uuid.UUID)
	SetState(*workflow.State)
}

func init() {
	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		panic(err)
	}

	checkAction1 := &workflow.Action{Name: "action", Descr: "action", Plugin: "plugin", Req: fakeReq{Say: "Hello"}}
	checkAction2 := &workflow.Action{Name: "action", Descr: "action", Plugin: "plugin", Req: fakeReq{Say: "You"}}
	checkAction3 := &workflow.Action{Name: "action", Descr: "action", Plugin: "plugin", Req: fakeReq{Say: "World"}}
	seqAction1 := &workflow.Action{Name: "action", Descr: "action", Plugin: "plugin", Req: fakeReq{Say: "Action!"}}

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
	build.AddAction(checkAction1.Clone())
	build.Up()

	build.AddCheck(builder.ContCheck, &workflow.Checks{Delay: 1 * time.Minute})
	build.AddAction(checkAction2.Clone())
	build.Up()

	build.AddCheck(builder.PostCheck, &workflow.Checks{})
	build.AddAction(checkAction3.Clone())
	build.Up()

	build.AddSequence(&workflow.Sequence{Name: "sequence", Descr: "sequence"})
	build.AddAction(seqAction1.Clone())
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

	if err := commitPlan(conn, plan); err != nil {
		t.Fatal(err)
	}

	// TODO(element-of-surprise): Add checks to verify the data in the database
}
