package etoe

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/kylelemons/godebug/pretty"

	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
)

var cloneOpts = []clone.Option{clone.WithKeepSecrets(), clone.WithKeepState()}

var pConfig = pretty.Config{
	IncludeUnexported: false,
	PrintStringers:    true,
	SkipZeroFields:    true,
}

func TestEtoE(t *testing.T) {
	ctx := context.Background()

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

	bypassChecks := &workflow.Checks{
		Delay: 0,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "check",
				Plugin: "check",
				Req:    testplugin.Req{Arg: "error"},
			},
			{
				Name:   "check",
				Descr:  "check",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
		},
	}

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

	build.AddChecks(builder.BypassChecks, bypassChecks).Up()
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.ContChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()

	build.AddBlock(
		builder.BlockArgs{
			Name:          "block0",
			Descr:         "block0",
			EntranceDelay: 1 * time.Second,
			ExitDelay:     1 * time.Second,
			Concurrency:   2,
		},
	)
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		panic(err)
	}

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

func TestBypassPlan(t *testing.T) {
	ctx := context.Background()

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

	bypassChecks := &workflow.Checks{
		Delay: 0,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "bypasscheck",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
			{
				Name:   "check",
				Descr:  "bypasscheck",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
		},
	}

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
		},
	}

	build, err := builder.New("end to end test", "tests that things work etoe in a basic way")
	if err != nil {
		panic(err)
	}

	build.AddChecks(builder.BypassChecks, bypassChecks).Up()
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.ContChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()

	build.AddBlock(
		builder.BlockArgs{
			Name:        "block0",
			Descr:       "block0",
			Concurrency: 2,
		},
	)
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		panic(err)
	}

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

	if result.Data.State.Status != workflow.Completed {
		t.Fatalf("TestBypassPlan: expected workflow to complete")
	}
	if result.Data.PreChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected Prechecks in NotStarted, got %s", result.Data.PreChecks.State.Status)
	}
	if result.Data.PostChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected Postchecks in NotStarted")
	}
	if result.Data.ContChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected ContChecks in NotStarted")
	}
	if result.Data.Blocks[0].State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected Block0 in NotStarted")
	}
}

func TestBypassBlock(t *testing.T) {
	ctx := context.Background()

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

	bypassChecksSuccess := &workflow.Checks{
		Delay: 0,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "bypasscheck",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
			{
				Name:   "check",
				Descr:  "bypasscheck",
				Plugin: "check",
				Req:    testplugin.Req{Sleep: 1 * time.Second},
			},
		},
	}
	bypassChecksFail := &workflow.Checks{
		Delay: 0,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "bypasscheck",
				Plugin: "check",
				Req:    testplugin.Req{Arg: "error"},
			},
		},
	}

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
		},
	}

	build, err := builder.New("end to end test", "tests that things work etoe in a basic way")
	if err != nil {
		panic(err)
	}

	build.AddBlock(
		builder.BlockArgs{
			Name:        "block0",
			Descr:       "block0",
			Concurrency: 2,
		},
	)
	build.AddChecks(builder.BypassChecks, bypassChecksSuccess).Up()
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.ContChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up().Up()

	build.AddBlock(
		builder.BlockArgs{
			Name:        "block1",
			Descr:       "block1",
			Concurrency: 2,
		},
	)
	build.AddChecks(builder.BypassChecks, bypassChecksFail).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	if build.Err() != nil {
		panic("problem building plan: " + build.Err().Error())
	}

	plan, err := build.Plan()
	if err != nil {
		panic(err)
	}

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
	count := 0
	for result = range ws.Status(ctx, id, 5*time.Second) {
		if result.Err != nil {
			panic(result.Err)
		}
		fmt.Println("Workflow status: ", result.Data.State.Status)
		count++
		if count > 5 {
			pConfig.Print("Workflow result: \n", result.Data)
			os.Exit(1)
		}
	}

	if result.Data.State.Status != workflow.Completed {
		t.Fatalf("TestBypassPlan: expected workflow to complete")
	}

	// Block 0 checks.
	if result.Data.Blocks[0].BypassChecks.State.Status != workflow.Completed {
		t.Fatalf("TestBypassPlan: expected block 0 BypassChecks in Completed, got %s", result.Data.Blocks[0].BypassChecks.State.Status)
	}
	if result.Data.Blocks[0].PreChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected block 0 Prechecks in NotStarted, got %s", result.Data.Blocks[0].PreChecks.State.Status)
	}
	if result.Data.Blocks[0].PostChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected block 0 Postchecks in NotStarted")
	}
	if result.Data.Blocks[0].ContChecks.State.Status != workflow.NotStarted {
		t.Fatalf("TestBypassPlan: expected block 0 ContChecks in NotStarted")
	}
	if result.Data.Blocks[0].State.Status != workflow.Completed {
		t.Fatalf("TestBypassPlan: expected Block0 in NotStarted")
	}
	for _, seq := range result.Data.Blocks[0].Sequences {
		if seq.State.Status != workflow.NotStarted {
			t.Fatalf("TestBypassPlan: expected block 0 Sequence in Completed")
		}
	}

	// Block 1 checks.
	if result.Data.Blocks[1].BypassChecks.State.Status != workflow.Failed {
		t.Fatalf("TestBypassPlan: expected block 1 BypassChecks in Failed")
	}
	if result.Data.Blocks[1].State.Status != workflow.Completed {
		t.Fatalf("TestBypassPlan: expected Block1 in Completed")
	}
	for _, seq := range result.Data.Blocks[1].Sequences {
		if seq.State.Status != workflow.Completed {
			t.Fatalf("TestBypassPlan: expected block 1 Sequence in Completed")
		}
	}
}
