/*
Hey you, MIGHT WANT TO READ THIS.

TestRecovery uses the run of TestEtoE() to capture all the SQL statements used by SQLite to store the workflow state.
We then replay those statements up to each stage of the workflow in order to simulate a crash/recovery at that point.
That means that this test also only runs the SQLite storage backend, but it is also testing how the recovery
works, which as long as the storage works correctly, is independent of the storage backend.

There is a vault_recovery_test.go that does some testing for a specific vault and the vault tests itself
should be proving the the storage backend works correctly.

You must run TestEtoE() first to generate the capture data used by this test, so if isolating to this test:
go test -v . -run '^(TestEtoE|TestRecovery)$'

Now, if you have a particular stage you want to debug, you can uncomment the check in recoveryTestStage() to only run that stage.
This will speed up debugging since you won't have to run all stages.

You will also want to uncomment the plan output line in TestRecovery() to see the final plan state on failure.

//t.Errorf("plan final:\n%s", pConfig.Sprint(r.Result))

Agents are not great at figuring out how to use this.
*/

package etoe

import (
	"fmt"
	"log"
	"testing"

	workstream "github.com/element-of-surprise/coercion"
	"github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	testplugin "github.com/element-of-surprise/coercion/internal/execute/sm/testing/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite"
	"github.com/google/uuid"
)

// SEE recoveryTestStage for a way to do a single stage.
// This test cannot be run in isolation, as it depends on the captured stages from
// the etoe end-to-end test.

func TestRecovery(t *testing.T) {
	log.Println("TestRecovery")

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

	g1 := context.Pool(ctx).Group()
	g2 := context.Pool(ctx).Limited(20).Group()
	results := make(chan testResult, 1)
	g1.Go(
		ctx,
		func(ctx context.Context) error {
			for i := 0; i < capture.Len(); i++ {
				g2.Go(
					ctx,
					func(ctx context.Context) error {
						recoveryTestStage(ctx, i, reg, results)
						return nil
					},
				)
			}
			return nil
		},
	)

	g3 := context.Pool(ctx).Group()
	g3.Go(
		ctx,
		func(ctx context.Context) error {
			for r := range results {
				if r.Err == nil {
					log.Printf("TestRecovery(stage %d): success", r.Stage)
				} else {
					t.Errorf("TestRecovery(stage %d): %v", r.Stage, r.Err)
					// Uncomment to see plan output on failure.
					//t.Errorf("plan final:\n%s", pConfig.Sprint(r.Result))
				}
			}
			return nil
		},
	)

	g1.Wait(ctx)
	g2.Wait(ctx)
	close(results)
	g3.Wait(ctx)
}

type testResult struct {
	Stage  int
	Result *workflow.Plan
	Err    error
}

func recoveryTestStage(ctx context.Context, stage int, reg *registry.Register, ch chan testResult) {
	// Uncomment and put in the stage number you wish to debug.
	/*
		if stage != 0 {
			return
		}
	*/

	vault, err := sqlite.New(ctx, "", reg, sqlite.WithInMemory())
	if err != nil {
		panic(fmt.Sprintf("TestEtoE: failed to create vault: %v", err))
	}

	func() {
		conn, err := vault.Pool().Take(ctx)
		if err != nil {
			panic(err)
		}
		defer vault.Pool().Put(conn)

		// Insert all data up to the current stage.
		for _, insert := range capture.Inserts() {
			sStmt, err := insert.Prepare(conn)
			if err != nil {
				panic(err)
			}
			_, err = sStmt.Step()
			if err != nil {
				panic(err)
			}
		}

		// Replay all stages up to the current stage.
		for x := 0; x <= stage; x++ {
			stmt := capture.Stmt(x)
			sStmt, err := stmt.Prepare(conn)
			if err != nil {
				panic(err)
			}
			_, err = sStmt.Step()
			if err != nil {
				panic(err)
			}
		}
	}()

	// Used to get the etoeID without replaying the entire workflow.
	ws, err := workstream.New(ctx, reg, vault, workstream.WithNoRecovery())
	if err != nil {
		panic(err)
	}

	ws, err = workstream.New(ctx, reg, vault)
	if err != nil {
		panic(err)
	}

	result, err := ws.Wait(ctx, etoeID)
	if err != nil {
		panic(err)
	}

	tr := testResult{
		Stage:  stage,
		Result: result,
	}
	defer func() {
		ch <- tr
	}()

	if result.State.Status != workflow.Completed {
		tr.Err = fmt.Errorf("workflow did not complete successfully(%s)", result.State.Status)
		return
	}
	plugResp := result.PreChecks.Actions[0].Attempts[0].Resp.(plugins.Resp)
	if plugResp.Arg == "" {
		tr.Err = fmt.Errorf("planID not found")
		return
	}
	_, err = uuid.Parse(plugResp.Arg)
	if err != nil {
		tr.Err = fmt.Errorf("planID not a valid UUID")
		return
	}
	if result.DeferredChecks.State.Status != workflow.Completed {
		tr.Err = fmt.Errorf("deferred checks did not complete successfully(%s)", result.DeferredChecks.State.Status)
		return
	}
	plugResp = result.DeferredChecks.Actions[0].Attempts[0].Resp.(plugins.Resp)
	if plugResp.Arg == "" {
		tr.Err = fmt.Errorf("planID not found")
		return
	}
	_, err = uuid.Parse(plugResp.Arg)
	if err != nil {
		tr.Err = fmt.Errorf("planID not a valid UUID")
		return
	}

	for _, block := range result.Blocks {
		if block.State.Status != workflow.Completed {
			tr.Err = fmt.Errorf("block did not complete successfully(%s)", block.State.Status)
			return
		}
		if block.PreChecks.State.Status != workflow.Completed {
			tr.Err = fmt.Errorf("block pre checks did not complete successfully(%s)", block.PreChecks.State.Status)
			return
		}
		if err := testPlugResp(block.PreChecks.Actions[0], "actionID"); err != nil {
			tr.Err = fmt.Errorf("block PreChecks: %v", err)
			return
		}
		if block.PostChecks.State.Status != workflow.Completed {
			tr.Err = fmt.Errorf("block post checks did not complete successfully(%s)", block.PostChecks.State.Status)
			return
		}
		if err := testPlugResp(block.PostChecks.Actions[0], "actionID"); err != nil {
			tr.Err = fmt.Errorf("block PostChecks: %v", err)
			return
		}
		if block.ContChecks.State.Status != workflow.Completed {
			tr.Err = fmt.Errorf("block cont checks did not complete successfully(%s)", block.ContChecks.State.Status)
		}
		if err := testPlugResp(block.ContChecks.Actions[0], "actionID"); err != nil {
			tr.Err = fmt.Errorf("block ContChecks: %v", err)
			return
		}
		if block.DeferredChecks.State.Status != workflow.Completed {
			tr.Err = fmt.Errorf("block deferred checks did not complete successfully(%s)", block.DeferredChecks.State.Status)
			return
		}
		if err := testPlugResp(block.DeferredChecks.Actions[0], "actionID"); err != nil {
			tr.Err = fmt.Errorf("block DeferredChecks: %v", err)
			return
		}

		for _, seq := range block.Sequences {
			if seq.State.Status != workflow.Completed {
				tr.Err = fmt.Errorf("sequence did not complete successfully(%s)(%s)", seq.ID, seq.State.Status)
				return
			}
			if err := testPlugResp(seq.Actions[1], "actionID"); err != nil {
				tr.Err = fmt.Errorf("sequence: %v", err)
				return
			}
		}
	}
}
