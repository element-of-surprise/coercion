package sqlite

import (
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/gostdlib/base/context"

	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
)

var plan *workflow.Plan

type setters interface {
	SetID(uuid.UUID)
	SetState(workflow.State)
}

type setPlanIDer interface {
	SetPlanID(uuid.UUID)
}

func init() {
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
		Attempts: func() workflow.AtomicSlice[workflow.Attempt] {
			var a workflow.AtomicSlice[workflow.Attempt]
			a.Set(
				[]workflow.Attempt{
					{
						Err:   &pluglib.Error{Message: "internal error"},
						Start: time.Now().Add(-1 * time.Minute),
						End:   time.Now(),
					},
					{
						Resp:  plugins.HelloResp{Said: "hello"},
						Start: time.Now().Add(-1 * time.Second),
						End:   time.Now(),
					},
				},
			)
			return a
		}(),
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

	build.AddDeferredActions()
	build.AddDeferBatch(&workflow.DeferBatch{
		When:        workflow.OnFailure,
		FailElement: true,
		Sequence:    workflow.Sequence{Name: "fail-batch", Descr: "fail-batch"},
	})
	build.AddAction(clone.Action(ctx, checkAction1))
	build.Up()
	build.AddDeferBatch(&workflow.DeferBatch{
		When:     workflow.OnSuccess,
		Sequence: workflow.Sequence{Name: "success-batch", Descr: "success-batch"},
	})
	build.AddAction(clone.Action(ctx, checkAction2))
	build.Up()
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

	for item := range walk.Plan(plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		if item.Value.Type() != workflow.OTPlan {
			setter.(setPlanIDer).SetPlanID(plan.ID)
		}
		setter.SetState(
			workflow.State{
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

func TestCommitPlan(t *testing.T) {
	t.Parallel()

	pool, err := freshInMemoryPool(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := commitPlan(t.Context(), conn, plan, nil); err != nil {
		t.Fatal(err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	mu := &sync.RWMutex{}
	reader := reader{
		mu:   mu,
		pool: pool,
		reg:  reg,
	}

	storedPlan, err := reader.Read(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(
		plan,
		storedPlan,
		cmp.AllowUnexported(
			workflow.Action{},
			workflow.Block{},
			workflow.Checks{},
			workflow.Sequence{},
			workflow.DeferredActions{},
			workflow.DeferBatch{},
		),
	); diff != "" {
		t.Fatalf("Read plan does not match the original plan: -want/+got:\n%s", diff)
	}

	// Explicitly verify DeferredActions round-tripped — the top-level cmp.Diff
	// above covers it, but asserting presence here protects against a future
	// regression where the DeferredActions reader path is accidentally skipped.
	if storedPlan.DeferredActions == nil {
		t.Fatalf("TestCommitPlan: storedPlan.DeferredActions is nil, want non-nil")
	}
	if got, want := len(storedPlan.DeferredActions.DeferredBatches), len(plan.DeferredActions.DeferredBatches); got != want {
		t.Fatalf("TestCommitPlan: DeferredBatches count = %d, want %d", got, want)
	}
	failBatch := storedPlan.DeferredActions.DeferredBatches[0]
	successBatch := storedPlan.DeferredActions.DeferredBatches[1]
	if failBatch.When != workflow.OnFailure {
		t.Errorf("TestCommitPlan: DeferredBatches[0].When = %s, want OnFailure", failBatch.When)
	}
	if !failBatch.FailElement {
		t.Errorf("TestCommitPlan: DeferredBatches[0].FailElement = false, want true")
	}
	if got, want := failBatch.Name, "fail-batch"; got != want {
		t.Errorf("TestCommitPlan: DeferredBatches[0].Name = %q, want %q", got, want)
	}
	if successBatch.When != workflow.OnSuccess {
		t.Errorf("TestCommitPlan: DeferredBatches[1].When = %s, want OnSuccess", successBatch.When)
	}
	if got, want := successBatch.Name, "success-batch"; got != want {
		t.Errorf("TestCommitPlan: DeferredBatches[1].Name = %q, want %q", got, want)
	}
}
