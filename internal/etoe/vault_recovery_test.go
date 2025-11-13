package etoe

import (
	"flag"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/gostdlib/base/context"

	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
)

// TestStorageRecovery tests the recovery functionality for recovery with any vault.
// It is not as thorough as recovery_test.go, which tests the internal recovery logic using
// sqlite, but that complicated setup cannot be used with other vault types.
// This creates a long-running plan, starts execution of the plan, simulates a restart,
// then recreates the vault and workstream to verify recovery works correctly.
func TestStorageRecovery(t *testing.T) {
	flag.Parse()

	ctx := context.Background()

	initGlobals()

	// Create initial workstream
	ws, err := workstream.New(ctx, reg, vault)
	if err != nil {
		t.Fatalf("Failed to create workstream: %v", err)
	}

	// Create and submit multiple plans
	plan, err := createLongRunningPlan()
	if err != nil {
		t.Fatalf("Failed to create plan: %v", err)
	}

	planID, err := ws.Submit(ctx, plan)
	if err != nil {
		t.Fatalf("Failed to submit plan: %v", err)
	}

	log.Printf("Submitted plan with ID: %s", planID)

	// Create a cancellable context for plan execution
	executionCtx, cancelExecution := context.WithCancel(ctx)

	// Start plan with the cancellable execution context
	if err := ws.Start(executionCtx, planID); err != nil {
		t.Fatalf("Failed to start plan %s: %v", planID, err)
	}
	log.Printf("Started plan %s, waiting for execution to begin...", planID)

	// Wait a short time to ensure execution has started
	time.Sleep(5 * time.Second)

	// Check that the first plan is running. Do I need to check all?
	status := ws.Status(ctx, planID, 1*time.Second)

	var lastResult *workflow.Plan
	for result := range status {
		if result.Err != nil {
			t.Fatalf("Error in status stream: %v", result.Err)
		}
		lastResult = result.Data
		break // Just get the first status update
	}

	pConfig.Print("Workflow result: \n", lastResult)

	if lastResult.State.Status != workflow.Running {
		t.Fatalf("Expected plan to be running, got status: %s", lastResult.State.Status)
	}

	log.Println("Plan is running, now simulating crash by canceling execution context...")

	// Simulate a crash by canceling the execution context first
	cancelExecution()

	// Wait a moment for the cancellation to take effect
	time.Sleep(1 * time.Second)

	// Then close the vault to simulate an abrupt shutdown. We don't do this
	// for sqlite since it's in-memory and would lose all data.
	if *vaultType != "sqlite" {
		if err := vault.Close(ctx); err != nil {
			log.Printf("Warning: Error closing vault: %v", err)
		}
	}

	// Simulate system restart by creating new vault and workstream
	log.Println("Simulating system restart - creating new vault and workstream...")

	// time.Sleep(5 * time.Minute) // wait for "leader election"
	time.Sleep(5 * time.Second)

	if *vaultType != "sqlite" {
		log.Println("recreating vault after simulated crash...")
		if err = createVault(ctx); err != nil {
			t.Fatalf("Failed to recreate vault after simulated crash: %v", err)
		}
	}

	log.Println("looking for plan: ", planID)
	log.Printf("vault is type: %T", vault)
	ch, err := vault.List(ctx, -1)
	if err != nil {
		t.Fatalf("Failed to list plans in vault after recreation: %v", err)
	}
	for p := range ch {
		log.Printf("Found plan in vault: %s|%s", p.Result.ID, p.Result.State.Status)
	}
	log.Println("reading plan from vault...")
	if p, err := vault.Read(ctx, planID); err == nil {
		pConfig.Print("Plan in vault: \n", p)
	} else {
		t.Fatalf("Failed to read plan from vault after recreation: %v", err)
	}

	log.Println("now creating new workstream for recovery...")
	// Create new workstream for recovery
	// This should rerun recovery.
	recoveryWS, err := workstream.New(ctx, reg, vault)
	if err != nil {
		t.Fatalf("Failed to create recovery workstream: %v", err)
	}

	log.Println("Recovery workstream created, attempting to recover plan(5 minute sleep)...")

	// Wait for the recovered first plan to complete or timeout
	// ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	// defer cancel()
	//
	time.Sleep(5 * time.Minute)
	log.Println("5 minute sleep complete, checking plan status...")

	fetchedPlan, err := recoveryWS.Plan(ctx, planID)
	if err != nil {
		t.Fatalf("Failed to retrieve plan %s for recovery: %v", planID, err)
	}

	// Start plan again after recovery, if there is still work to be done.
	if fetchedPlan.State.Status == workflow.NotStarted {
		if err := recoveryWS.Start(ctx, planID); err != nil {
			t.Fatalf("Failed to start plan %s: %v", planID, err)
		}
		log.Printf("Started plan %s after restart", planID)
	}

	result, err := recoveryWS.Wait(ctx, planID)
	if err != nil {
		t.Fatalf("Failed to wait for recovered plan: %v", err)
	}
	if result.State.Status != workflow.Completed {
		t.Fatalf("Expected recovered plan to complete, got status: %s", result.State.Status)
	}
	// Additional validation: Check that some actions were actually executed
	if len(result.Blocks) == 0 {
		t.Fatal("Expected plan to have blocks")
	}

	for _, block := range result.Blocks {
		if block.State.Status != workflow.Completed {
			t.Errorf("Block %s did not complete, status: %s", block.ID, block.State.Status)
		}
		for _, seq := range block.Sequences {
			if seq.State.Status != workflow.Completed {
				t.Errorf("Sequence %s did not complete, status: %s", seq.ID, seq.State.Status)
			}
			for _, action := range seq.Actions {
				if action.State.Status != workflow.Completed {
					t.Errorf("Action %s did not complete, status: %s", action.ID, action.State.Status)
				}
			}
		}
	}

	log.Printf("Recovery test successful: plan %s completed after recovery", planID)

	pConfig.Print("Workflow result: \n", result)
	log.Println("All validation checks passed")
}

// createLongRunningPlan creates a plan with actions that sleep for a significant duration
func createLongRunningPlan() (*workflow.Plan, error) {
	ctx := context.Background()

	// Create checks with shorter sleep times for quicker test execution
	checks := &workflow.Checks{
		Delay: 1 * time.Second,
		Actions: []*workflow.Action{
			{
				Name:   "check",
				Descr:  "Quick check action",
				Plugin: "check",
				Req:    testplugin.Req{Arg: "planid"},
			},
		},
	}

	// Create sequences with long-running actions
	longSeq := &workflow.Sequence{
		Key:   workflow.NewV7(),
		Name:  "long-running-sequence",
		Descr: "Sequence with long-running actions for recovery testing",
		Actions: []*workflow.Action{
			{
				Name:    "quick-action",
				Descr:   "Quick action that completes fast",
				Plugin:  testplugin.Name,
				Timeout: 30 * time.Second,
				Req:     testplugin.Req{Sleep: 1 * time.Second, Arg: "quick"},
			},
			{
				Name:    "long-action",
				Descr:   "Long-running action for recovery testing",
				Plugin:  testplugin.Name,
				Timeout: 7 * time.Minute,                                     // Timeout for the long-running action
				Req:     testplugin.Req{Sleep: 5 * time.Minute, Arg: "long"}, // 30 second sleep (shortened for testing)
			},
			{
				Name:    "final-action",
				Descr:   "Final action after recovery",
				Plugin:  testplugin.Name,
				Timeout: 30 * time.Second,
				Req:     testplugin.Req{Sleep: 1 * time.Second, Arg: "final"},
			},
		},
	}

	// Build the plan
	build, err := builder.New("blob-recovery-test", "Test plan for blob storage recovery functionality")
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Add plan-level checks (using cloning to avoid register conflicts)
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.DeferredChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()

	// Add a block with the long-running sequence
	build.AddBlock(
		builder.BlockArgs{
			Key:           workflow.NewV7(),
			Name:          "recovery-test-block",
			Descr:         "Block for testing blob storage recovery",
			EntranceDelay: 1 * time.Second,
			ExitDelay:     1 * time.Second,
			Concurrency:   1, // Single concurrency to ensure predictable execution order
		},
	)

	// Add block-level checks (using cloning to avoid register conflicts)
	build.AddChecks(builder.PreChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.PostChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.ContChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()
	build.AddChecks(builder.DeferredChecks, clone.Checks(ctx, checks, cloneOpts...)).Up()

	// Add the long-running sequence (using cloning to avoid register conflicts)
	build.AddSequence(clone.Sequence(ctx, longSeq, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		return nil, fmt.Errorf("failed to build plan: %w", err)
	}

	return plan, nil
}

// createBlobCredential creates the appropriate Azure credential for blob storage
func createBlobCredential(msi string) (azcore.TokenCredential, error) {
	if msi != "" {
		msiResc := azidentity.ResourceID(msi)
		msiOpts := azidentity.ManagedIdentityCredentialOptions{ID: msiResc}
		cred, err := azidentity.NewManagedIdentityCredential(&msiOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
		}
		return cred, nil
	}

	// Use Azure CLI credential
	azOptions := &azidentity.AzureCLICredentialOptions{}
	azCred, err := azidentity.NewAzureCLICredential(azOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure CLI credential: %w", err)
	}

	return azCred, nil
}
