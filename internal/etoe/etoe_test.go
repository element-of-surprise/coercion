package etoe

import (
	"context"
	"fmt"
	"testing"
	"time"

	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/kylelemons/godebug/pretty"

	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
)

var pConfig = pretty.Config{
	IncludeUnexported: false,
	PrintStringers:    true,
	SkipZeroFields:    true,
}

func TestEtoE(t *testing.T) {
	plugCheck := &testplugin.Plugin{
		AlwaysRespond: true,
		IsCheckPlugin: true,
		PlugName:      "check",
	}

	plugAction := &testplugin.Plugin{
		AlwaysRespond: true,
	}

	reg := registry.New()
	reg.Register(plugCheck)
	reg.Register(plugAction)

	checks := &workflow.Checks{
		Delay: 2 * time.Second,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "check",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
			{
				Name:   "check",
				Descr:  "check",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
		},
	}

	seqs := &workflow.Sequence{
		Name:  "seq",
		Descr: "seq",
		Actions: []*workflow.Action{
			{Name: "action0", Descr: "action", Plugin: testplugin.Name, Req: testplugin.Req{Sleep: 1 * time.Second}},
			{Name: "action1", Descr: "action", Plugin: testplugin.Name, Req: testplugin.Req{Sleep: 1 * time.Second}},
		},
	}

	build, err := builder.New("end to end test", "tests that things work etoe in a basic way")
	if err != nil {
		panic(err)
	}

	build.AddChecks(builder.PreChecks, checks.Clone()).Up()
	build.AddChecks(builder.PostChecks, checks.Clone()).Up()
	build.AddChecks(builder.ContChecks, checks.Clone()).Up()

	build.AddBlock(
		builder.BlockArgs{
			Name:          "block0",
			Descr:         "block0",
			EntranceDelay: 1 * time.Second,
			ExitDelay:     1 * time.Second,
			Concurrency:   2,
		},
	)
	build.AddSequence(seqs.Clone()).Up()
	build.AddSequence(seqs.Clone()).Up()
	build.AddSequence(seqs.Clone()).Up()
	build.AddSequence(seqs.Clone()).Up()

	plan, err := build.Plan()
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	vault, err := sqlite.New(ctx, "", reg, sqlite.WithInMemory())
	if err != nil {
		panic(err)
	}

	ws, err := workstream.New(ctx, reg, vault)
	if err != nil {
		panic(err)
	}

	id, err := ws.Submit(ctx, plan)
	if err != nil {
		panic(err)
	}

	if err := ws.Start(ctx, id); err != nil {
		panic(err)
	}

	var result workstream.Result[*workflow.Plan]
	for result = range ws.Status(ctx, id, 5*time.Second) {
		if result.Err != nil {
			panic(result.Err)
		}
		fmt.Println("Workflow status: ", result.Data.State.Status)
	}

	pConfig.Print("Workflow result: \n", result.Data)
}
