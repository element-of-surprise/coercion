package azblob

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/go-json-experiment/json"
	"github.com/gostdlib/base/concurrency/sync"
)

// uploadPlanToFakeB writes the plan entry and object blobs (with metadata) into the fake. It is the
// *testing.B twin of uploadPlanToFake.
func uploadPlanToFakeB(ctx context.Context, b *testing.B, fake *blobops.Fake, prefix string, plan *workflow.Plan) {
	b.Helper()

	containerName := containerForPlan(prefix, plan.ID)
	if err := fake.EnsureContainer(ctx, containerName); err != nil {
		b.Fatalf("uploadPlanToFakeB: EnsureContainer: %v", err)
	}

	md, err := planToMetadata(ctx, plan)
	if err != nil {
		b.Fatalf("uploadPlanToFakeB: planToMetadata: %v", err)
	}
	md[mdPlanType] = toPtr(ptEntry)

	entry, err := planToPlanEntry(plan)
	if err != nil {
		b.Fatalf("uploadPlanToFakeB: planToPlanEntry: %v", err)
	}
	entryData, err := json.Marshal(entry)
	if err != nil {
		b.Fatalf("uploadPlanToFakeB: marshal entry: %v", err)
	}
	if err := fake.UploadBlob(ctx, containerName, planEntryBlobName(plan.ID), md, entryData); err != nil {
		b.Fatalf("uploadPlanToFakeB: upload entry: %v", err)
	}

	md[mdPlanType] = toPtr(ptObject)
	objData, err := json.Marshal(plan)
	if err != nil {
		b.Fatalf("uploadPlanToFakeB: marshal object: %v", err)
	}
	if err := fake.UploadBlob(ctx, containerName, planObjectBlobName(plan.ID), md, objData); err != nil {
		b.Fatalf("uploadPlanToFakeB: upload object: %v", err)
	}
}

// latencyClient wraps a blobops.Fake and adds a fixed per-read-op latency plus round-trip
// counters, so benchmarks approximate real network round-trips (the recovery bottleneck) rather
// than in-memory map access.
type latencyClient struct {
	*blobops.Fake

	latency time.Duration
	gets    atomic.Int64 // GetBlob calls
	heads   atomic.Int64 // GetMetadata calls
}

func (c *latencyClient) GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	c.gets.Add(1)
	time.Sleep(c.latency)
	return c.Fake.GetBlob(ctx, containerName, blobName)
}

func (c *latencyClient) GetMetadata(ctx context.Context, containerName, blobName string) (map[string]*string, error) {
	c.heads.Add(1)
	time.Sleep(c.latency)
	return c.Fake.GetMetadata(ctx, containerName, blobName)
}

// createLargePlan builds a plan with blocks x seqsPerBlock x actionsPerSeq actions, plus one
// PreChecks (with actionsPerSeq actions) per block and one plan-level PreChecks. Every object is
// set Running when running is true, so the plan routes through fetchRunningPlan.
func createLargePlan(blocks, seqsPerBlock, actionsPerSeq int, running bool) *workflow.Plan {
	status := workflow.NotStarted
	if running {
		status = workflow.Running
	}

	mkAction := func(say string) *workflow.Action {
		a := &workflow.Action{
			ID:      workflow.NewV7(),
			Name:    "action",
			Descr:   "action",
			Plugin:  testPlugins.HelloPluginName,
			Timeout: 30 * time.Second,
			Req:     testPlugins.HelloReq{Say: say},
		}
		a.State.Set(workflow.State{Status: status})
		return a
	}

	mkChecks := func() *workflow.Checks {
		actions := make([]*workflow.Action, actionsPerSeq)
		for i := range actions {
			actions[i] = mkAction("check")
		}
		c := &workflow.Checks{ID: workflow.NewV7(), Actions: actions}
		c.State.Set(workflow.State{Status: status})
		return c
	}

	planBlocks := make([]*workflow.Block, blocks)
	for b := range planBlocks {
		seqs := make([]*workflow.Sequence, seqsPerBlock)
		for s := range seqs {
			actions := make([]*workflow.Action, actionsPerSeq)
			for a := range actions {
				actions[a] = mkAction("seq")
			}
			seq := &workflow.Sequence{ID: workflow.NewV7(), Name: "seq", Descr: "seq", Actions: actions}
			seq.State.Set(workflow.State{Status: status})
			seqs[s] = seq
		}
		block := &workflow.Block{ID: workflow.NewV7(), Name: "block", Descr: "block", PreChecks: mkChecks(), Sequences: seqs}
		block.State.Set(workflow.State{Status: status})
		planBlocks[b] = block
	}

	plan := &workflow.Plan{
		ID:         workflow.NewV7(),
		Name:       "large plan",
		Descr:      "large plan",
		SubmitTime: time.Now().UTC(),
		PreChecks:  mkChecks(),
		Blocks:     planBlocks,
	}
	plan.State.Set(workflow.State{Status: status})
	return plan
}

// benchRecoverySetup uploads a large running plan into a fresh fake and returns a reader and
// recovery whose client injects per-op latency, plus the container name and plan.
func benchRecoverySetup(b *testing.B, latency time.Duration) (reader, recovery, string, *workflow.Plan) {
	b.Helper()

	ctx := context.Background()
	prefix := "bench"

	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	fake := blobops.NewFake()
	plan := createLargePlan(8, 8, 8, true)
	containerName := containerForPlan(prefix, plan.ID)

	// Upload the plan (entry + object) and all sub-objects with the plain fake (upload latency
	// is not what we are measuring).
	uploadPlanToFakeB(ctx, b, fake, prefix, plan)
	plainUploader := &uploader{
		mu:          planlocks.New(ctx),
		client:      fake,
		prefix:      prefix,
		planObjPool: context.Pool(ctx).Limited(ctx, "", 5),
		blockPool:   context.Pool(ctx).Limited(ctx, "", 10),
		leafObjPool: context.Pool(ctx).Limited(ctx, "", 20),
	}
	if err := plainUploader.uploadSubObjects(ctx, containerName, plan); err != nil {
		b.Fatalf("benchRecoverySetup: uploadSubObjects: %v", err)
	}

	lc := &latencyClient{Fake: fake, latency: latency}
	r := reader{
		mu:            planlocks.New(ctx),
		readFlight:    &sync.Flight[string, *workflow.Plan]{},
		existsFlight:  &sync.Flight[string, bool]{},
		prefix:        prefix,
		client:        lc,
		reg:           reg,
		retentionDays: 14,
	}
	rec := recovery{
		reader:   r,
		uploader: &uploader{mu: planlocks.New(ctx), client: lc, prefix: prefix, planObjPool: context.Pool(ctx).Limited(ctx, "", 5), blockPool: context.Pool(ctx).Limited(ctx, "", 10), leafObjPool: context.Pool(ctx).Limited(ctx, "", 20)},
	}
	return r, rec, containerName, plan
}

// BenchmarkRecoveryFetchRunningPlan exercises path A: reconstructing a running plan from its
// sub-object blobs.
func BenchmarkRecoveryFetchRunningPlan(b *testing.B) {
	ctx := context.Background()
	r, _, _, plan := benchRecoverySetup(b, 500*time.Microsecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := r.fetchPlan(ctx, plan.ID)
		if err != nil {
			b.Fatalf("BenchmarkRecoveryFetchRunningPlan: %v", err)
		}
		if got == nil {
			b.Fatal("BenchmarkRecoveryFetchRunningPlan: nil plan")
		}
	}
}

// BenchmarkRecoveryEnsureSubObjectBlobs exercises path B: verifying every sub-object blob exists
// (all present, so it is a pure HEAD walk).
func BenchmarkRecoveryEnsureSubObjectBlobs(b *testing.B) {
	ctx := context.Background()
	_, rec, containerName, plan := benchRecoverySetup(b, 500*time.Microsecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := rec.ensureSubObjectBlobs(ctx, containerName, plan); err != nil {
			b.Fatalf("BenchmarkRecoveryEnsureSubObjectBlobs: %v", err)
		}
	}
}
