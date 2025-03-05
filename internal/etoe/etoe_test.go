package etoe

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/cosmosdb"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
)

var cloneOpts = []clone.Option{clone.WithKeepSecrets(), clone.WithKeepState()}

var pConfig = pretty.Config{
	IncludeUnexported: false,
	PrintStringers:    true,
	SkipZeroFields:    true,
}

var (
	vaultType = flag.String("vault", "sqlite", "The type of storage vault to use.")

	// CosmosDB flags that are only used if vault is set to "cosmosdb".
	swarm     = flag.String("collection", os.Getenv("AZURE_COSMOSDB_SWARM"), "The name of the coercion swarm.")
	db        = flag.String("db", os.Getenv("AZURE_COSMOSDB_DBNAME"), "The name of the cosmosdb database.")
	container = flag.String("container", os.Getenv("AZURE_COSMOSDB_CNAME"), "The name of the cosmosdb container.")
	msi       = flag.String("msi", "", "The identity with vmss contributor role. If empty, az login is used.")
	teardown  = flag.Bool("teardown", false, "Teardown the cosmosdb container.")
)

func TestEtoE(t *testing.T) {
	flag.Parse()
	if err := validateFlags(); err != nil {
		t.Fatalf("TestEtoE: failed to validate flags: %v", err)
	}

	ctx := context.Background()
	logger := slog.Default()

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
				Req:    testplugin.Req{Arg: "planid"},
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
		Key:   workflow.NewV7(),
		Name:  "seq",
		Descr: "seq",
		Actions: []*workflow.Action{
			{Name: "action0", Descr: "action", Plugin: testplugin.Name, Req: testplugin.Req{Sleep: 1 * time.Second}},
			{Name: "action1", Descr: "action", Plugin: testplugin.Name, Req: testplugin.Req{Arg: "planid"}},
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
	build.AddChecks(builder.DeferredChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()

	build.AddBlock(
		builder.BlockArgs{
			Key:           workflow.NewV7(),
			Name:          "block0",
			Descr:         "block0",
			EntranceDelay: 1 * time.Second,
			ExitDelay:     1 * time.Second,
			Concurrency:   2,
		},
	)
	build.AddChecks(builder.BypassChecks, clone.Checks(ctx, bypassChecks, cloneOpts...)).Up()
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.ContChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.DeferredChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		panic(err)
	}

	// Gives each item a unique key. We do this here because we clone the same checks.
	for item := range walk.Plan(ctx, plan) {
		switch obj := item.Value.(type) {
		case *workflow.Action:
			obj.Key = workflow.NewV7()
		case *workflow.Sequence:
			obj.Key = workflow.NewV7()
		case *workflow.Checks:
			obj.Key = workflow.NewV7()
		}
	}

	var cred azcore.TokenCredential
	if *vaultType == "cosmosdb" {
		cred, err = msiCred(*msi)
		if err != nil {
			panic(err)
		}
	}

	if *teardown == true {
		defer func() {
			if *vaultType == "cosmosdb" {
				// Teardown the cosmosdb container
				if err := cosmosdb.Teardown(ctx, *db, *container, cred, nil); err != nil {
					panic(err)
				}
			}
		}()
	}

	var vault storage.Vault
	switch *vaultType {
	case "sqlite":
		vault, err = sqlite.New(ctx, "", reg, sqlite.WithInMemory())
	case "cosmosdb":
		logger.Info(fmt.Sprintf("TestEtoE: Using cosmosdb: %s, %s", *db, *container))
		vault, err = cosmosdb.New(ctx, *swarm, *db, *container, cred, reg)
	default:
		panic(fmt.Errorf("TestEtoE: unknown storage vault type: %s", *vaultType))
	}
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

	result, err := ws.Wait(ctx, id)
	if err != nil {
		panic(err)
	}

	pConfig.Print("Workflow result: \n", result)

	if result.State.Status != workflow.Completed {
		t.Fatalf("TestEtoE: workflow did not complete successfully(%s)", result.State.Status)
	}
	plugResp := result.PreChecks.Actions[0].Attempts[0].Resp.(plugins.Resp)
	if plugResp.Arg == "" {
		t.Fatalf("TestEtoE: planID not found")
	}
	_, err = uuid.Parse(plugResp.Arg)
	if err != nil {
		t.Fatalf("TestEtoE: planID not a valid UUID")
	}
	if result.DeferredChecks.State.Status != workflow.Completed {
		t.Fatalf("TestEtoE: deferred checks did not complete successfully(%s)", result.DeferredChecks.State.Status)
	}
	plugResp = result.DeferredChecks.Actions[0].Attempts[0].Resp.(plugins.Resp)
	if plugResp.Arg == "" {
		t.Fatalf("TestEtoE: planID not found")
	}
	_, err = uuid.Parse(plugResp.Arg)
	if err != nil {
		t.Fatalf("TestEtoE: planID not a valid UUID")
	}

	for _, block := range result.Blocks {
		if block.State.Status != workflow.Completed {
			t.Fatalf("TestEtoE: block did not complete successfully(%s)", block.State.Status)
		}
		if block.PreChecks.State.Status != workflow.Completed {
			t.Fatalf("TestEtoE: block pre checks did not complete successfully(%s)", block.PreChecks.State.Status)
		}
		if err := testPlugResp(block.PreChecks.Actions[0], "actionID"); err != nil {
			t.Fatalf("TestEtoE(block PreChecks): %v", err)
		}
		if block.PostChecks.State.Status != workflow.Completed {
			t.Fatalf("TestEtoE: block post checks did not complete successfully(%s)", block.PostChecks.State.Status)
		}
		if err := testPlugResp(block.PostChecks.Actions[0], "actionID"); err != nil {
			t.Fatalf("TestEtoE(block PostChecks): %v", err)
		}
		if block.ContChecks.State.Status != workflow.Completed {
			t.Fatalf("TestEtoE: block cont checks did not complete successfully(%s)", block.ContChecks.State.Status)
		}
		if err := testPlugResp(block.ContChecks.Actions[0], "actionID"); err != nil {
			t.Fatalf("TestEtoE(block ContChecks): %v", err)
		}
		if block.DeferredChecks.State.Status != workflow.Completed {
			t.Fatalf("TestEtoE: block deferred checks did not complete successfully(%s)", block.DeferredChecks.State.Status)
		}
		if err := testPlugResp(block.DeferredChecks.Actions[0], "actionID"); err != nil {
			t.Fatalf("TestEtoE(block DeferredChecks): %v", err)
		}

		for _, seq := range block.Sequences {
			if seq.State.Status != workflow.Completed {
				t.Fatalf("TestEtoE: sequence did not complete successfully(%s)", seq.State.Status)
			}
			if err := testPlugResp(seq.Actions[1], "actionID"); err != nil {
				t.Fatalf("TestEtoE(sequence): %v", err)
			}
		}
	}
}

func testPlugResp(action *workflow.Action, want string) error {
	plugResp := action.Attempts[0].Resp.(plugins.Resp)
	if plugResp.Arg == "" {
		return fmt.Errorf("%q was not found in the response", want)
	}
	_, err := uuid.Parse(plugResp.Arg)
	if err != nil {
		return fmt.Errorf("%q was not a valid UUID", plugResp.Arg)
	}
	return nil
}

func TestBypassPlan(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

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

	var cred azcore.TokenCredential
	if *vaultType == "cosmosdb" {
		cred, err = msiCred(*msi)
		if err != nil {
			panic(err)
		}
	}

	if *teardown == true {
		defer func() {
			if *vaultType == "cosmosdb" {
				// Teardown the cosmosdb container
				if err := cosmosdb.Teardown(ctx, *db, *container, cred, nil); err != nil {
					panic(err)
				}
			}
		}()
	}

	var vault storage.Vault
	switch *vaultType {
	case "sqlite":
		vault, err = sqlite.New(ctx, "", reg, sqlite.WithInMemory())
	case "cosmosdb":
		logger.Info(fmt.Sprintf("TestBypassPlan: Using cosmosdb: %s, %s", *db, *container))
		vault, err = cosmosdb.New(ctx, *swarm, *db, *container, cred, reg)
	default:
		panic(fmt.Errorf("TestBypassPlan: unknown storage vault type: %s", *vaultType))
	}
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
	logger := slog.Default()

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

	var cred azcore.TokenCredential
	if *vaultType == "cosmosdb" {
		cred, err = msiCred(*msi)
		if err != nil {
			panic(err)
		}
	}

	if *teardown == true {
		defer func() {
			if *vaultType == "cosmosdb" {
				// Teardown the cosmosdb container
				if err := cosmosdb.Teardown(ctx, *db, *container, cred, nil); err != nil {
					panic(err)
				}
			}
		}()
	}

	var vault storage.Vault
	switch *vaultType {
	case "sqlite":
		vault, err = sqlite.New(ctx, "", reg, sqlite.WithInMemory())
	case "cosmosdb":
		logger.Info(fmt.Sprintf("TestBypassBlock: Using cosmosdb: %s, %s", *db, *container))
		vault, err = cosmosdb.New(ctx, *swarm, *db, *container, cred, reg)
	default:
		panic(fmt.Errorf("TestBypassBlock: unknown storage vault type: %s", *vaultType))
	}
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

func validateFlags() error {
	if *vaultType == "cosmosdb" {
		if *db == "" {
			return fmt.Errorf("missing db name")
		}
		if *container == "" {
			return fmt.Errorf("missing container name")
		}
	}
	return nil
}

// msiCred returns a managed identity credential.
func msiCred(msi string) (azcore.TokenCredential, error) {
	if msi != "" {
		msiResc := azidentity.ResourceID(msi)
		msiOpts := azidentity.ManagedIdentityCredentialOptions{ID: msiResc}
		cred, err := azidentity.NewManagedIdentityCredential(&msiOpts)
		if err != nil {
			return nil, err
		}
		log.Println("Authentication is using identity token.")
		return cred, nil
	}
	// Otherwise, allow authentication via az login
	// Need following roles comosdb sql roles:
	// https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/security/how-to-grant-data-plane-role-based-access?tabs=built-in-definition%2Ccsharp&pivots=azure-interface-cli
	azOptions := &azidentity.AzureCLICredentialOptions{}
	azCred, err := azidentity.NewAzureCLICredential(azOptions)
	if err != nil {
		return nil, fmt.Errorf("creating az cli credential: %s", err)
	}

	log.Println("Authentication is using az cli token.")
	return azCred, nil
}
