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

	g := context.Pool(ctx).Limited(20).Group()
	results := make(chan testResult, 1)
	go func() {
		for i := 0; i < capture.Len(); i++ {
			g.Go(
				ctx,
				func(ctx context.Context) error {
					recoveryTestStage(ctx, i, reg, results)
					return nil
				},
			)
		}
	}()

	g2 := context.Pool(ctx).Group()
	g2.Go(
		ctx,
		func(ctx context.Context) error {
			for r := range results {
				if r.Err == nil {
					log.Printf("TestRecovery(stage %d): success", r.Stage)
				} else {
					t.Errorf("TestRecovery(stage %d): %v", r.Stage, r.Err)
				}
			}
			return nil
		},
	)

	g.Wait(ctx)
	close(results)
	g2.Wait(ctx)
}

type testResult struct {
	Stage  int
	Result *workflow.Plan
	Err    error
}

func recoveryTestStage(ctx context.Context, stage int, reg *registry.Register, ch chan testResult) {
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

	ws, err := workstream.New(ctx, reg, vault)
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
		log.Println("returned result")
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
				tr.Err = fmt.Errorf("sequence did not complete successfully(%s)", seq.State.Status)
				return
			}
			if err := testPlugResp(seq.Actions[1], "actionID"); err != nil {
				tr.Err = fmt.Errorf("sequence: %v", err)
				return
			}
		}
	}
}
