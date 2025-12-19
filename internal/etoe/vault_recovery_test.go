package etoe

import (
	"flag"
	"fmt"
	"log"
	"testing"
	"time"

	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	gofrsUUID "github.com/gofrs/uuid/v5"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
)

// newV7FromTime creates a UUIDv7 with the specified timestamp.
func newV7FromTime(t time.Time) uuid.UUID {
	u, err := gofrsUUID.NewV7AtTime(t)
	if err != nil {
		panic(fmt.Sprintf("failed to create UUIDv7: %v", err))
	}
	return uuid.UUID(u)
}

// TestStorageRecovery tests the recovery functionality for recovery with any vault.
// It is not as thorough as recovery_test.go, which tests the internal recovery logic using
// sqlite, but that complicated setup cannot be used with other vault types.
// This creates a long-running plan, starts execution of the plan, simulates a restart,
// then recreates the vault and workstream to verify recovery works correctly.
func TestStorageRecovery(t *testing.T) {
	flag.Parse()

	ctx := context.Background()

	initGlobals()

	// For azblob, insert an old running plan (2 days + 1 hour old) directly via the vault.
	// This plan should NOT be recovered because it's outside the retention period.
	var oldPlanID uuid.UUID
	if *vaultType == "azblob" {
		oldPlanID = insertOldRunningPlan(t, ctx)
	}

	// Create initial workstream
	ws, err := workstream.New(ctx, reg, vault)
	if err != nil {
		t.Fatalf("Failed to create workstream: %v", err)
	}

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

	time.Sleep(5 * time.Second)

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
	time.Sleep(5 * time.Minute)

	// Start plan again after recovery, if there is still work to be done.
	if err := recoveryWS.Start(ctx, planID); err != nil {
		t.Fatalf("Failed to start plan %s: %v", planID, err)
	}
	log.Printf("Started plan %s after restart", planID)

	log.Println("Waiting for recovered plan to complete...")
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

	// For azblob, verify that the old plan was NOT recovered
	if *vaultType == "azblob" {
		verifyOldPlanNotRecovered(t, ctx, oldPlanID)
	}
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

// insertOldRunningPlan creates and stores a running plan with a UUID from 14 days + 1 hour ago.
// This plan should be outside the 14-day retention period and should NOT be recovered.
func insertOldRunningPlan(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()

	// Create a plan with a UUID from 14 days + 1 hour ago (outside 14-day retention)
	oldTime := time.Now().UTC().AddDate(0, 0, -14).Add(-1 * time.Hour)

	plan, err := createOldRunningPlan(oldTime)
	if err != nil {
		t.Fatalf("Failed to create old plan: %v", err)
	}
	oldPlanID := plan.ID
	log.Printf("Creating old running plan with ID %s (timestamp: %s)", oldPlanID, oldTime)

	// Store the plan directly in the vault (not via workstream)
	if err := vault.Create(ctx, plan); err != nil {
		t.Fatalf("Failed to store old plan: %v", err)
	}

	log.Printf("Stored old running plan with ID %s", oldPlanID)
	return oldPlanID
}

// createOldRunningPlan creates a simple running plan with the specified ID and submit time.
func createOldRunningPlan(submitTime time.Time) (*workflow.Plan, error) {
	ctx := context.Background()

	seqs := &workflow.Sequence{
		Key:   workflow.NewV7(),
		Name:  "old-sequence",
		Descr: "Sequence for retention test",
		Actions: []*workflow.Action{
			{
				ID:      newV7FromTime(submitTime.Add(2 * time.Second)),
				Name:    "old-action",
				Descr:   "Action for retention test",
				Plugin:  testplugin.Name,
				Timeout: 30 * time.Second,
				Req:     testplugin.Req{Sleep: 1 * time.Second, Arg: "old"},
			},
		},
	}

	build, err := builder.New("old-retention-test", "Test plan for retention behavior")
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	build.AddBlock(
		builder.BlockArgs{
			Key:         workflow.NewV7(),
			Name:        "old-block",
			Descr:       "Block for retention test",
			Concurrency: 1,
		},
	)
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		return nil, fmt.Errorf("failed to build plan: %w", err)
	}

	type setIDer interface {
		SetID(uuid.UUID)
	}

	i := 0
	for item := range walk.Plan(plan) {
		item.Value.(setIDer).SetID(newV7FromTime(submitTime.Add(time.Duration(i) * time.Second)))
		i++
	}

	// Override the plan ID with our custom old UUID
	plan.SubmitTime = submitTime
	plan.State = &workflow.State{
		Status: workflow.Running,
		Start:  submitTime,
	}

	return plan, nil
}

// verifyOldPlanNotRecovered checks that the old plan was NOT recovered by reading it
// directly (bypassing retention check) and verifying its state is still Running.
func verifyOldPlanNotRecovered(t *testing.T, ctx context.Context, oldPlanID uuid.UUID) {
	t.Helper()

	log.Printf("Verifying old plan %s was NOT recovered", oldPlanID)

	azblobVault, ok := vault.(*azblob.Vault)
	if !ok {
		t.Fatalf("Expected vault to be *azblob.Vault, got %T", vault)
	}

	// Read the plan directly (bypassing retention check)
	readPlan, err := azblobVault.ReadDirect(ctx, oldPlanID)
	if err != nil {
		t.Fatalf("Failed to read old plan directly: %v", err)
	}

	// Verify the plan is still in Running state (not recovered/modified)
	if readPlan.State.Status != workflow.Running {
		t.Errorf("Old plan state was modified during recovery: expected Running, got %s", readPlan.State.Status)
	} else {
		log.Printf("Old plan %s correctly remains in Running state (not recovered)", oldPlanID)
	}

	log.Println("Azblob retention test passed: old plans are not recovered")
}

// TestAzblobRetentionRecovery tests that recovery respects the retention period boundary.
// It creates 4 plans at different ages:
// - 1 hour old (should be recoverable)
// - 2 days old (should be recoverable)
// - 13 days old (should be recoverable)
// - 14 days + 1 minute old (should NOT be recoverable - outside 14 day retention)
//
// This test cannot run in parallel as it deletes containers before starting.
func TestAzblobRetentionRecovery(t *testing.T) {
	flag.Parse()

	if *vaultType != "azblob" {
		t.Skip("Skipping azblob retention test: vault type is not azblob")
	}

	ctx := context.Background()
	initGlobals()

	log.Println("Tearing down existing containers...")
	if err := azblob.Teardown(ctx, *azblobURL, *blobPrefix, cred); err != nil {
		t.Fatalf("[TestAzblobRetentionRecovery]: failed to teardown containers: %v", err)
	}
	log.Println("Teardown complete")

	log.Println("Waiting 1 minute for teardown to propagate...")
	time.Sleep(1 * time.Minute)

	if err := createVault(ctx); err != nil {
		t.Fatalf("[TestAzblobRetentionRecovery]: failed to recreate vault after teardown: %v", err)
	}

	now := time.Now().UTC()

	// Define plan ages for testing.
	tests := []struct {
		name          string
		age           time.Duration
		shouldRecover bool
	}{
		{
			name:          "1 hour old",
			age:           1 * time.Hour,
			shouldRecover: true,
		},
		{
			name:          "2 days old",
			age:           2 * 24 * time.Hour,
			shouldRecover: true,
		},
		{
			name:          "13 days old",
			age:           13 * 24 * time.Hour,
			shouldRecover: true,
		},
		{
			name:          "14 days + 1 minute old",
			age:           14*24*time.Hour + 1*time.Minute,
			shouldRecover: false,
		},
	}

	planIDs := make([]uuid.UUID, len(tests))
	for i, test := range tests {
		planTime := now.Add(-test.age)
		plan, err := createPlanAtTime(planTime)
		if err != nil {
			t.Fatalf("[TestAzblobRetentionRecovery]: failed to create plan for %s: %v", test.name, err)
		}
		planIDs[i] = plan.ID

		if err := vault.Create(ctx, plan); err != nil {
			t.Fatalf("[TestAzblobRetentionRecovery]: failed to store plan for %s: %v", test.name, err)
		}
		log.Printf("Created plan %s with ID %s (age: %s)", test.name, plan.ID, test.age)
	}

	if err := vault.Close(ctx); err != nil {
		log.Printf("Warning: error closing vault: %v", err)
	}

	if err := createVault(ctx); err != nil {
		t.Fatalf("[TestAzblobRetentionRecovery]: failed to recreate vault for recovery: %v", err)
	}

	log.Println("Creating workstream to trigger recovery...")
	_, err := workstream.New(ctx, reg, vault)
	if err != nil {
		t.Fatalf("[TestAzblobRetentionRecovery]: failed to create workstream: %v", err)
	}

	azblobVault, ok := vault.(*azblob.Vault)
	if !ok {
		t.Fatalf("[TestAzblobRetentionRecovery]: expected vault to be *azblob.Vault, got %T", vault)
	}

	for i, test := range tests {
		planID := planIDs[i]

		_, readErr := vault.Read(ctx, planID)

		directPlan, directErr := azblobVault.ReadDirect(ctx, planID)

		if test.shouldRecover {
			if readErr != nil {
				t.Errorf("[TestAzblobRetentionRecovery](%s): expected Read() to succeed, got error: %v", test.name, readErr)
				continue
			}
			log.Printf("Plan %s (%s) correctly readable via Read()", test.name, planID)
		} else {
			if readErr == nil {
				t.Errorf("[TestAzblobRetentionRecovery](%s): expected Read() to fail for plan outside retention, but it succeeded", test.name)
				continue
			}
			if directErr != nil {
				t.Errorf("[TestAzblobRetentionRecovery](%s): expected ReadDirect() to succeed, got error: %v", test.name, directErr)
				continue
			}
			if directPlan.State.Status != workflow.NotStarted {
				t.Errorf("[TestAzblobRetentionRecovery](%s): expected plan status to remain NotStarted, got %s", test.name, directPlan.State.Status)
				continue
			}
			log.Printf("Plan %s (%s) correctly outside retention: Read() failed, ReadDirect() succeeded, status unchanged", test.name, planID)
		}
	}

	log.Println("All retention boundary checks passed")
}

// createPlanAtTime creates a simple plan with IDs timestamped at the specified time.
// The plan is in NotStarted state so recovery would attempt to process it.
func createPlanAtTime(submitTime time.Time) (*workflow.Plan, error) {
	ctx := context.Background()

	seqs := &workflow.Sequence{
		Name:  "retention-test-sequence",
		Descr: "Sequence for retention boundary test",
		Actions: []*workflow.Action{
			{
				Name:    "retention-test-action",
				Descr:   "Action for retention boundary test",
				Plugin:  testplugin.Name,
				Timeout: 30 * time.Second,
				Req:     testplugin.Req{Sleep: 1 * time.Second, Arg: "retention"},
			},
		},
	}

	build, err := builder.New("retention-boundary-test", "Test plan for retention boundary behavior")
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	build.AddBlock(
		builder.BlockArgs{
			Name:        "retention-test-block",
			Descr:       "Block for retention boundary test",
			Concurrency: 1,
		},
	)
	build.AddSequence(clone.Sequence(ctx, seqs, cloneOpts...)).Up()

	plan, err := build.Plan()
	if err != nil {
		return nil, fmt.Errorf("failed to build plan: %w", err)
	}

	type setIDer interface {
		SetID(uuid.UUID)
	}

	// Set all object IDs to timestamps based on submitTime.
	i := 0
	for item := range walk.Plan(plan) {
		item.Value.(setIDer).SetID(newV7FromTime(submitTime.Add(time.Duration(i) * time.Millisecond)))
		i++
	}

	plan.SubmitTime = submitTime
	plan.State = &workflow.State{
		Status: workflow.NotStarted,
	}

	return plan, nil
}
