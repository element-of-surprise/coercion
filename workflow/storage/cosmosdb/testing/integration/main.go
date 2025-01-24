package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/cosmosdb"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
)

var (
	dbName   = flag.String("db-name", os.Getenv("AZURE_COSMOSDB_DBNAME"), "the name of the cosmosdb database")
	cName    = flag.String("container-name", os.Getenv("AZURE_COSMOSDB_CNAME"), "the name of the cosmosdb container")
	pk       = flag.String("partition-key", os.Getenv("AZURE_COSMOSDB_PK"), "the name of the cosmosdb partition key")
	msi      = flag.String("msi", "", "the identity with vmss contributor role. If empty, az login is used")
	teardown = flag.Bool("teardown", false, "teardown the cosmosdb container")
)

var zeroTime = time.Unix(0, 0)

var plan *workflow.Plan

type setters interface {
	SetID(uuid.UUID)
	GetState() *workflow.State
	SetState(*workflow.State)
}

func init() {
	flag.Parse()

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
		Attempts: []*workflow.Attempt{
			{
				Err:   &pluglib.Error{Message: "internal error"},
				Start: time.Now().Add(-1 * time.Minute).UTC(),
				End:   time.Now().UTC(),
			},
			{
				Resp:  plugins.HelloResp{Said: "hello"},
				Start: time.Now().Add(-1 * time.Second).UTC(),
				End:   time.Now().UTC(),
			},
		},
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

	for item := range walk.Plan(context.Background(), plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		setter.SetState(
			&workflow.State{
				Status: workflow.Running,
				Start:  time.Now().UTC(),
				End:    time.Now().UTC(),
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

func main() {
	var err error

	defer func() {
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
	}()

	ctx := context.Background()
	logger := slog.Default()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	cred, err := msiCred(*msi)
	if err != nil {
		fatalErr(logger, "Failed to create credential: %v", err)
	}

	if *teardown == true {
		defer func() {
			// Teardown the cosmosdb container
			if err := cosmosdb.Teardown(ctx, *dbName, *cName, cred, nil); err != nil {
				fatalErr(logger, "Failed to teardown: %v", err)
			}
		}()
	}

	vault, err := cosmosdb.New(ctx, *dbName, *cName, *pk, cred, reg, cosmosdb.WithEnforceETag())
	if err != nil {
		fatalErr(logger, "Failed to create vault: %v", err)
	}

	planID := plan.ID

	if err := vault.Create(ctx, plan); err != nil {
		fatalErr(logger, "Failed to create plan entry: %v", err)
	}

	results, err := vault.List(context.Background(), 0)
	if err != nil {
		fatalErr(logger, "Failed to list plan entries: %v", err)
	}
	for res := range results {
		if res.Err != nil {
			fatalErr(logger, "result err: %v", res.Err)
		}
	}

	filters := storage.Filters{
		ByIDs: []uuid.UUID{
			planID,
		},
	}
	results, err = vault.Search(context.Background(), filters)
	if err != nil {
		fatalErr(logger, "Failed to list plan entries: %v", err)
	}
	for res := range results {
		if res.Err != nil {
			fatalErr(logger, "result err: %v", res.Err)
		}
	}

	result, err := vault.Read(ctx, planID)
	if err != nil {
		fatalErr(logger, "Failed to read plan entry: %v", err)
	}

	// creator will set to zero time
	plan.SubmitTime = zeroTime
	if diff := pretty.Compare(plan, result); diff != "" {
		fatalErr(logger, "mismatch in submitted and returned plan with ID %s: returned plan: -want/+got:\n%s", plan.ID, diff)
	}

	plan.State.Status = workflow.Completed
	if err := vault.UpdatePlan(ctx, plan); err != nil {
		fatalErr(logger, "Failed to update plan entry: %v", err)
	}

	result, err = vault.Read(ctx, planID)
	if err != nil {
		fatalErr(logger, "Failed to read plan entry: %v", err)
	}
	if diff := pretty.Compare(plan, result); diff != "" {
		fatalErr(logger, "mismatch in submitted and returned plan with ID %s: returned plan: -want/+got:\n%s", plan.ID, diff)
	}
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

	log.Println("Authentication is using az cli token.", azCred)
	return azCred, nil
}

func fatalErr(logger *slog.Logger, msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	logger.Error(s, "fatal", "true")
	os.Exit(1)
}
