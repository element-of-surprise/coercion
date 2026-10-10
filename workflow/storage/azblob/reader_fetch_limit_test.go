package azblob

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// countingOps records the most blob downloads in flight at once. Each download is held briefly so that downloads
// allowed to overlap do.
type countingOps struct {
	*blobops.Fake

	counting atomic.Bool
	inFlight atomic.Int64
	max      atomic.Int64
}

func (c *countingOps) GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	if c.counting.Load() {
		n := c.inFlight.Add(1)
		defer c.inFlight.Add(-1)
		for {
			m := c.max.Load()
			if n <= m || c.max.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return c.Fake.GetBlob(ctx, containerName, blobName)
}

// TestFetchPlanFromContainer is a regression test: every fetch fan-out made its own Limited pool, so the limits
// multiplied down the tree (sequences per block times actions per sequence) and across concurrent reads, and one large
// Running plan issued hundreds of downloads at once. The reader's shared pools must bound them all.
func TestFetchPlanFromContainer(t *testing.T) {
	t.Parallel()

	const (
		plans   = 2
		blocks  = 4
		seqs    = 8
		actions = 8
	)

	ctx := t.Context()
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})
	ops := &countingOps{Fake: blobops.NewFake()}
	v := &Vault{prefix: "test", mu: planlocks.New(ctx)}
	v.wire(ctx, Args{Prefix: "test", Reg: reg, RetentionDays: 14}, ops)

	ids := make([]uuid.UUID, 0, plans)
	for p := 0; p < plans; p++ {
		var bl []*workflow.Block
		for b := 0; b < blocks; b++ {
			var sl []*workflow.Sequence
			for s := 0; s < seqs; s++ {
				var al []*workflow.Action
				for a := 0; a < actions; a++ {
					al = append(al, makeAction("action", "action", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "hello"}, workflow.NotStarted))
				}
				sl = append(sl, makeSequence("seq", "seq", al, workflow.NotStarted))
			}
			bl = append(bl, makeBlock("block", "block", nil, sl, workflow.NotStarted))
		}
		plan := makePlan("plan", "plan", nil, bl, workflow.NotStarted)
		plan.SubmitTime = time.Now().UTC()
		for item := range walk.Plan(plan) {
			if item.Value.Type() != workflow.OTPlan {
				item.Value.(setPlanIDer).SetPlanID(plan.ID)
			}
		}
		if err := v.Create(ctx, plan); err != nil {
			t.Fatalf("TestFetchPlanFromContainer: Create: %s", err)
		}
		// Only a Running plan is rebuilt from its sub-objects.
		setStatus(plan, workflow.Running)
		plan.RuntimeUpdate.Set(time.Now().UTC().Truncate(time.Millisecond))
		if err := v.UpdatePlan(ctx, plan); err != nil {
			t.Fatalf("TestFetchPlanFromContainer: UpdatePlan(Running): %s", err)
		}
		ids = append(ids, plan.ID)
	}

	ops.counting.Store(true)
	g := context.Pool(ctx).Group()
	for _, id := range ids {
		g.Go(ctx, func(ctx context.Context) error {
			_, err := v.reader.fetchPlanFromContainer(ctx, id)
			return err
		})
	}
	if err := g.Wait(ctx); err != nil {
		t.Fatalf("TestFetchPlanFromContainer: got err == %s, want err == nil", err)
	}

	// Each read makes its own entry download before any fan-out; everything below it runs on the shared pools.
	limit := int64(plans + planFetchPoolSize + blockFetchPoolSize + leafFetchPoolSize)
	if got := ops.max.Load(); got > limit {
		t.Errorf("TestFetchPlanFromContainer: got %d downloads in flight at once, want at most %d", got, limit)
	}
}
