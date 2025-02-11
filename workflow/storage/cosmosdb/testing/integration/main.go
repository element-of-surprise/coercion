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
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/cosmosdb"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
)

//+gocover:ignore:file No need to test fake store.

var (
	db        = flag.String("db-name", os.Getenv("AZURE_COSMOSDB_DBNAME"), "the name of the cosmosdb database")
	container = flag.String("container-name", os.Getenv("AZURE_COSMOSDB_CNAME"), "the name of the cosmosdb container")
	pk        = flag.String("partition-key", os.Getenv("AZURE_COSMOSDB_PK"), "the name of the cosmosdb partition key value")
	msi       = flag.String("msi", "", "the identity with vmss contributor role. If empty, az login is used")
	teardown  = flag.Bool("teardown", false, "teardown the cosmosdb container")
)

var zeroTime = time.Unix(0, 0)

var plan *workflow.Plan

func init() {
	flag.Parse()

	plan = cosmosdb.NewTestPlan()
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
			if err := cosmosdb.Teardown(ctx, *db, *container, cred, nil); err != nil {
				fatalErr(logger, "Failed to teardown: %v", err)
			}
		}()
	}

	vault, err := cosmosdb.New(ctx, *db, *container, *pk, cred, reg)
	if err != nil {
		fatalErr(logger, "Failed to create vault: %v", err)
	}

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
			plan.ID,
		},
		ByStatus: []workflow.Status{
			workflow.Running,
		},
	}
	var resultCount int
	results, err = vault.Search(context.Background(), filters)
	if err != nil {
		fatalErr(logger, "Failed to list plan entries: %v", err)
	}
	for res := range results {
		resultCount++
		if res.Err != nil {
			fatalErr(logger, "result err: %v", res.Err)
		}
	}
	if resultCount != 1 {
		fatalErr(logger, "expected 1 search result, got %d", resultCount)
	}

	result, err := vault.Read(ctx, plan.ID)
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

	result, err = vault.Read(ctx, plan.ID)
	if err != nil {
		fatalErr(logger, "Failed to read plan entry: %v", err)
	}
	if diff := pretty.Compare(plan, result); diff != "" {
		fatalErr(logger, "mismatch in submitted and returned plan with ID %s: returned plan: -want/+got:\n%s", plan.ID, diff)
	}

	log.Println("Success")
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
