package azblob

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/values/chans"
	"github.com/gostdlib/base/values/generics/result"
	"github.com/kylelemons/godebug/pretty"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	testPlugins "github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// newFakeVault returns a Vault backed by an in-memory blob store.
func newFakeVault(t *testing.T) (*Vault, *blobops.Fake) {
	t.Helper()

	ctx := t.Context()
	reg := registry.New()
	reg.Register(&testPlugins.HelloPlugin{})

	fake := blobops.NewFake()
	v := &Vault{prefix: "test", mu: planlocks.New(ctx)}
	v.wire(ctx, Args{Prefix: "test", Reg: reg, RetentionDays: 14}, fake)
	return v, fake
}

// newRunningPlan creates a plan with one block in v and moves it to Running, the way execution starts a plan.
func newRunningPlan(t *testing.T, v *Vault) *workflow.Plan {
	t.Helper()

	ctx := t.Context()
	action := makeAction("action", "action", testPlugins.HelloPluginName, testPlugins.HelloReq{Say: "hello"}, workflow.NotStarted)
	seq := makeSequence("seq", "seq", []*workflow.Action{action}, workflow.NotStarted)
	block := makeBlock("block", "block", nil, []*workflow.Sequence{seq}, workflow.NotStarted)
	// A multiline description, which blob metadata cannot hold as is.
	plan := makePlan("plan", "a plan\nwith a second line", nil, []*workflow.Block{block}, workflow.NotStarted)
	plan.SubmitTime = time.Now().UTC()
	for item := range walk.Plan(plan) {
		if item.Value.Type() != workflow.OTPlan {
			item.Value.(setPlanIDer).SetPlanID(plan.ID)
		}
	}

	if err := v.Create(ctx, plan); err != nil {
		t.Fatalf("newRunningPlan: Create: %s", err)
	}
	setStatus(plan, workflow.Running)
	// sm.Start records a heartbeat when it marks the plan Running.
	plan.RuntimeUpdate.Set(time.Now().UTC().Truncate(time.Millisecond))
	if err := v.UpdatePlan(ctx, plan); err != nil {
		t.Fatalf("newRunningPlan: UpdatePlan(Running): %s", err)
	}
	return plan
}

// setStatus sets the plan's status, recording an end time for a final status.
func setStatus(plan *workflow.Plan, status workflow.Status) {
	state := plan.State.Get()
	state.Status = status
	if status > workflow.Running {
		state.End = time.Now().UTC()
	}
	plan.State.Set(state)
}

// stripRuntimeUpdate rewrites plan's entry blob without its runtimeUpdate field, as entries were stored before the
// field existed. Its metadata is kept.
func stripRuntimeUpdate(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
	t.Helper()

	ctx := t.Context()
	containerName := containerForPlan("test", plan.ID)
	blobName := planEntryBlobName(plan.ID)
	data, err := fake.GetBlob(ctx, containerName, blobName)
	if err != nil {
		t.Fatalf("stripRuntimeUpdate: GetBlob: %s", err)
	}
	md, err := fake.GetMetadata(ctx, containerName, blobName)
	if err != nil {
		t.Fatalf("stripRuntimeUpdate: GetMetadata: %s", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("stripRuntimeUpdate: Unmarshal: %s", err)
	}
	if _, ok := entry["runtimeUpdate"]; !ok {
		t.Fatalf("stripRuntimeUpdate: entry blob has no runtimeUpdate field to strip")
	}
	delete(entry, "runtimeUpdate")
	if data, err = json.Marshal(entry); err != nil {
		t.Fatalf("stripRuntimeUpdate: Marshal: %s", err)
	}
	if err := fake.UploadBlob(ctx, containerName, blobName, md, data); err != nil {
		t.Fatalf("stripRuntimeUpdate: UploadBlob: %s", err)
	}
}

// undecodable is stored bytes that are not complete JSON.
var undecodable = []byte(`["trunc`)

// rewriteBlob replaces the bytes of a blob in plan's container with data, keeping its metadata.
func rewriteBlob(t *testing.T, fake *blobops.Fake, planID uuid.UUID, blobName string, data []byte) {
	t.Helper()

	ctx := t.Context()
	containerName := containerForPlan("test", planID)
	md, err := fake.GetMetadata(ctx, containerName, blobName)
	if err != nil {
		t.Fatalf("rewriteBlob(%s): GetMetadata: %s", blobName, err)
	}
	if err := fake.UploadBlob(ctx, containerName, blobName, md, data); err != nil {
		t.Fatalf("rewriteBlob(%s): UploadBlob: %s", blobName, err)
	}
}

// truncateBlob replaces the bytes of a blob in plan's container with bytes that do not decode, keeping its metadata.
func truncateBlob(t *testing.T, fake *blobops.Fake, planID uuid.UUID, blobName string) {
	t.Helper()
	rewriteBlob(t, fake, planID, blobName, undecodable)
}

// deleteBlockBlob removes the blob of plan's first block, so the plan cannot be rebuilt from its entry.
func deleteBlockBlob(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
	t.Helper()

	if err := fake.DeleteBlob(t.Context(), containerForPlan("test", plan.ID), blockBlobName(plan.ID, plan.Blocks[0].ID)); err != nil {
		t.Fatalf("deleteBlockBlob: DeleteBlob: %s", err)
	}
}

// corruptEntryState replaces the state in the metadata of plan's entry blob with a value that does not decode.
func corruptEntryState(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
	t.Helper()

	ctx := t.Context()
	containerName := containerForPlan("test", plan.ID)
	blobName := planEntryBlobName(plan.ID)
	data, err := fake.GetBlob(ctx, containerName, blobName)
	if err != nil {
		t.Fatalf("corruptEntryState: GetBlob: %s", err)
	}
	md, err := fake.GetMetadata(ctx, containerName, blobName)
	if err != nil {
		t.Fatalf("corruptEntryState: GetMetadata: %s", err)
	}
	if _, ok := md[mdKeyState]; !ok {
		t.Fatalf("corruptEntryState: entry metadata has no %q key", mdKeyState)
	}
	md[mdKeyState] = toPtr(string(undecodable))
	if err := fake.UploadBlob(ctx, containerName, blobName, md, data); err != nil {
		t.Fatalf("corruptEntryState: UploadBlob: %s", err)
	}
}

// corruptActionEntry rewrites the blob of plan's first sequence action after change is applied to its entry.
func corruptActionEntry(t *testing.T, fake *blobops.Fake, plan *workflow.Plan, change func(e *actionsEntry)) {
	t.Helper()

	blobName := actionBlobName(plan.ID, plan.Blocks[0].Sequences[0].Actions[0].ID)
	data, err := fake.GetBlob(t.Context(), containerForPlan("test", plan.ID), blobName)
	if err != nil {
		t.Fatalf("corruptActionEntry: GetBlob: %s", err)
	}
	var entry actionsEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("corruptActionEntry: Unmarshal: %s", err)
	}
	change(&entry)
	if data, err = json.Marshal(entry); err != nil {
		t.Fatalf("corruptActionEntry: Marshal: %s", err)
	}
	rewriteBlob(t, fake, plan.ID, blobName, data)
}

// corruptObjectReq rewrites plan's object blob so its first sequence action's request is a JSON array, which does not
// decode into the plugin's request type.
func corruptObjectReq(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
	t.Helper()

	blobName := planObjectBlobName(plan.ID)
	data, err := fake.GetBlob(t.Context(), containerForPlan("test", plan.ID), blobName)
	if err != nil {
		t.Fatalf("corruptObjectReq: GetBlob: %s", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("corruptObjectReq: Unmarshal: %s", err)
	}
	child := func(m map[string]any, key string) map[string]any {
		list, ok := m[key].([]any)
		if !ok || len(list) == 0 {
			t.Fatalf("corruptObjectReq: object blob has no %s", key)
		}
		c, ok := list[0].(map[string]any)
		if !ok {
			t.Fatalf("corruptObjectReq: object blob's first %s is %T", key, list[0])
		}
		return c
	}
	action := child(child(child(obj, "Blocks"), "Sequences"), "Actions")
	if _, ok := action["Req"]; !ok {
		t.Fatalf("corruptObjectReq: object blob's action has no Req")
	}
	action["Req"] = []any{1}
	if data, err = json.Marshal(obj); err != nil {
		t.Fatalf("corruptObjectReq: Marshal: %s", err)
	}
	rewriteBlob(t, fake, plan.ID, blobName, data)
}

// searchRunning returns the IDs Search reports as Running.
func searchRunning(t *testing.T, v *Vault) []string {
	t.Helper()

	ch, err := v.Search(t.Context(), storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		t.Fatalf("searchRunning: %s", err)
	}
	var ids []string
	for res := range ch {
		if res.Err != nil {
			t.Fatalf("searchRunning: %s", res.Err)
		}
		ids = append(ids, res.Result.ID.String())
	}
	return ids
}

// TestRead is the regression test for the 2026-09-29 incident. A storage account throttled with 503 ServerBusy made a
// plan's final write land its entry blob (Completed) but not its object blob (still Running). Search trusted the entry
// and skipped the plan, Read returned the stale object saying Running, so a waiter was told a finished plan was running
// with nobody executing it and the service crash-looped. Read must now agree with the entry, and so with Search.
func TestRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// final is the status written after the plan is Running. workflow.Running means no further write.
		final workflow.Status
		// failObject makes the object blob upload of that final write fail, leaving the object stale.
		failObject bool
		// failBlockRead makes reading the block blob fail, as a throttled read does.
		failBlockRead bool
		// entryGone makes the entry blob's download report not found after its metadata was read, as when something
		// outside this process deletes it in between.
		entryGone bool
		// runningAction stores the plan's action as Running before the final write and leaves it that way, as a
		// sub-object write that lagged the plan's final write does.
		runningAction bool
		// oldEntry strips runtimeUpdate from the entry blob after the final write, as a plan stored before the field
		// existed has it.
		oldEntry bool
		// damage, when set, changes the plan's stored blobs after the final write: it removes one, or rewrites one so it
		// does not decode.
		damage func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan)

		wantStatus workflow.Status
		// wantActionStatus, when set, is the action status Read must return.
		wantActionStatus workflow.Status
		wantSearchHit    bool
		// wantInconsistent means the error must have type errors.TypeStorageInconsistent and must not be classified as
		// not found, so callers don't treat a damaged plan as a missing one.
		wantInconsistent bool
		// wantNotFound means the error must be classified as not found.
		wantNotFound bool
		wantErr      bool
	}{
		{
			name:          "Success: a running plan reads as Running and is found by Search",
			final:         workflow.Running,
			wantStatus:    workflow.Running,
			wantSearchHit: true,
		},
		{
			name:       "Success: a completed plan reads as Completed and is not found by Search",
			final:      workflow.Completed,
			wantStatus: workflow.Completed,
		},
		{
			name:          "Success: a running plan stored before entries had runtimeUpdate reads as Running and is found by Search",
			final:         workflow.Running,
			oldEntry:      true,
			wantStatus:    workflow.Running,
			wantSearchHit: true,
		},
		{
			name:       "Success: a completed plan stored before entries had runtimeUpdate reads as Completed",
			final:      workflow.Completed,
			oldEntry:   true,
			wantStatus: workflow.Completed,
		},
		{
			name:       "Success: a completion whose object upload failed reads as Completed, agreeing with Search",
			final:      workflow.Completed,
			failObject: true,
			wantStatus: workflow.Completed,
		},
		{
			name:       "Success: a failure whose object upload failed reads as Failed, agreeing with Search",
			final:      workflow.Failed,
			failObject: true,
			wantStatus: workflow.Failed,
		},
		{
			// Regression: a torn Failed plan was rebuilt with its lagging Running action, and recovery then wrote
			// that back as the plan's object for good.
			name:             "Success: a torn failure rebuilt with a lagging Running action reads the action as Failed",
			final:            workflow.Failed,
			failObject:       true,
			runningAction:    true,
			wantStatus:       workflow.Failed,
			wantActionStatus: workflow.Failed,
		},
		{
			// Regression: a Running plan missing one sub-object was reported as a missing plan, and callers treated that
			// as permanent and gave up on a plan whose entry still said Running.
			name:             "Error: a running plan with a missing block blob is damaged storage, not a missing plan",
			final:            workflow.Running,
			damage:           deleteBlockBlob,
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			// Regression: a not-found from re-reading a running plan's entry was taken for a missing sub-object, so a
			// plan that is gone was reported as damaged storage.
			name:          "Error: a running plan whose entry disappears during the read is not found, not damaged storage",
			final:         workflow.Running,
			entryGone:     true,
			wantSearchHit: true,
			wantNotFound:  true,
			wantErr:       true,
		},
		{
			// Regression: every failure to rebuild a torn plan was reported as inconsistent storage, so a throttled read
			// looked like damage that retrying could not fix.
			name:          "Error: a torn completion whose block blob cannot be read is a storage error, not damaged storage",
			final:         workflow.Completed,
			failObject:    true,
			failBlockRead: true,
			wantErr:       true,
		},
		{
			name:             "Error: a torn completion whose block blob is missing cannot be rebuilt",
			final:            workflow.Completed,
			failObject:       true,
			damage:           deleteBlockBlob,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			// Regression: a stored blob that did not decode was a TypeStorageGet error, so recovery retried the
			// running plan until its restart time and then exited the process, on every restart.
			name:  "Error: a running plan whose entry blob does not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				truncateBlob(t, fake, plan.ID, planEntryBlobName(plan.ID))
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:             "Error: a running plan whose entry metadata does not decode is damaged storage",
			final:            workflow.Running,
			damage:           corruptEntryState,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a running plan whose block blob does not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				truncateBlob(t, fake, plan.ID, blockBlobName(plan.ID, plan.Blocks[0].ID))
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a running plan whose sequence blob does not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				truncateBlob(t, fake, plan.ID, sequenceBlobName(plan.ID, plan.Blocks[0].Sequences[0].ID))
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a running plan whose action blob does not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				truncateBlob(t, fake, plan.ID, actionBlobName(plan.ID, plan.Blocks[0].Sequences[0].Actions[0].ID))
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a running plan whose action request does not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				corruptActionEntry(t, fake, plan, func(e *actionsEntry) { e.Req = undecodable })
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a running plan whose action attempts do not decode is damaged storage",
			final: workflow.Running,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				corruptActionEntry(t, fake, plan, func(e *actionsEntry) { e.Attempts = undecodable })
			},
			wantSearchHit:    true,
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:  "Error: a completed plan whose object blob does not decode is damaged storage",
			final: workflow.Completed,
			damage: func(t *testing.T, fake *blobops.Fake, plan *workflow.Plan) {
				truncateBlob(t, fake, plan.ID, planObjectBlobName(plan.ID))
			},
			wantInconsistent: true,
			wantErr:          true,
		},
		{
			name:             "Error: a completed plan whose object blob holds an action request that does not decode is damaged storage",
			final:            workflow.Completed,
			damage:           corruptObjectReq,
			wantInconsistent: true,
			wantErr:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			v, fake := newFakeVault(t)
			plan := newRunningPlan(t, v)

			action := plan.Blocks[0].Sequences[0].Actions[0]
			if test.runningAction {
				action.State.Set(workflow.State{Status: workflow.Running, Start: time.Now().UTC()})
				if err := v.UpdateAction(ctx, action); err != nil {
					t.Fatalf("TestRead(%s): UpdateAction(Running): %s", test.name, err)
				}
			}

			switch {
			case test.final == workflow.Running:
			case test.failObject:
				finishWithTornObject(t, v, fake, plan, test.final)
			default:
				setStatus(plan, test.final)
				if err := v.UpdatePlan(ctx, plan); err != nil {
					t.Fatalf("TestRead(%s): final UpdatePlan: got err == %s, want err == nil", test.name, err)
				}
			}
			if test.oldEntry {
				stripRuntimeUpdate(t, fake, plan)
			}
			if test.damage != nil {
				test.damage(t, fake, plan)
			}

			if test.entryGone {
				entryName := planEntryBlobName(plan.ID)
				fake.GetBlobErr = func(_, blobName string) error {
					if blobName == entryName {
						return &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)}
					}
					return nil
				}
			}
			if test.failBlockRead {
				blockName := blockBlobName(plan.ID, plan.Blocks[0].ID)
				fake.GetBlobErr = func(_, blobName string) error {
					if blobName == blockName {
						return errors.New("storage busy")
					}
					return nil
				}
			}

			hit := false
			for _, id := range searchRunning(t, v) {
				if id == plan.ID.String() {
					hit = true
				}
			}
			if hit != test.wantSearchHit {
				t.Errorf("TestRead(%s): got Search(Running) found plan == %v, want %v", test.name, hit, test.wantSearchHit)
			}

			got, err := v.Read(ctx, plan.ID)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestRead(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestRead(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				// internal/execute (recovery.go and retry.go) skips a plan that IsStorageInconsistent and treats one that
				// IsNotFound as gone, so both classifications are part of Read's contract: recover.fetchPlans and
				// Plans.resumeOrExit/Plans.stranded skip on errors.IsStorageInconsistent, and retry anything else until
				// the process exits.
				if got := errors.IsStorageInconsistent(err); got != test.wantInconsistent {
					t.Errorf("TestRead(%s): got IsStorageInconsistent(err) == %v, want %v", test.name, got, test.wantInconsistent)
				}
				// Workstream.Wait (coercion.go) promises callers that an error retrying cannot fix wraps
				// errors.ErrPermanent, and exponential backoff stops retrying on it. Damaged storage is such an error.
				if test.wantInconsistent && !errors.Is(err, errors.ErrPermanent) {
					t.Errorf("TestRead(%s): got err == %s, want it to wrap errors.ErrPermanent", test.name, err)
				}
				if got := errors.IsNotFound(err); got != test.wantNotFound {
					t.Errorf("TestRead(%s): got IsNotFound(err) == %v, want %v", test.name, got, test.wantNotFound)
				}
				return
			}

			// Regression: a running plan's Name and Descr were read from blob metadata, which cannot hold them as is.
			if got.Name != plan.Name || got.Descr != plan.Descr {
				t.Errorf("TestRead(%s): got Name, Descr %q, %q, want %q, %q", test.name, got.Name, got.Descr, plan.Name, plan.Descr)
			}
			status := got.State.Get().Status
			if status != test.wantStatus {
				t.Errorf("TestRead(%s): got status %v, want %v", test.name, status, test.wantStatus)
			}
			// The invariant the incident broke: Read says Running exactly when Search lists the plan as Running.
			if (status == workflow.Running) != hit {
				t.Errorf("TestRead(%s): Read status %v disagrees with Search(Running) found == %v", test.name, status, hit)
			}
			if test.wantActionStatus != workflow.NotStarted {
				gotAction := got.Blocks[0].Sequences[0].Actions[0].State.Get().Status
				if gotAction != test.wantActionStatus {
					t.Errorf("TestRead(%s): got action status %v, want %v", test.name, gotAction, test.wantActionStatus)
				}
			}
			// Regression: a running plan is rebuilt from its entry, which used to drop the heartbeat, so recovery saw a
			// zero last update and aged out live plans whose current step ran longer than its limit. An entry stored
			// before the field existed has no heartbeat, so recovery falls back to the sub-object times as it always did.
			if status == workflow.Running {
				want := plan.RuntimeUpdate.Get()
				if test.oldEntry {
					want = time.Time{}
				}
				// Regression: a zero heartbeat was stored with Set, which marks the field set, so the plan read back no
				// longer matched the one submitted. An entry with no heartbeat must leave the field unset.
				if test.oldEntry && got.RuntimeUpdate.IsSet() {
					t.Errorf("TestRead(%s): got RuntimeUpdate set to %v, want it unset", test.name, got.RuntimeUpdate.Get())
				}
				if got := got.RuntimeUpdate.Get(); !got.Equal(want) {
					t.Errorf("TestRead(%s): got RuntimeUpdate %v, want %v", test.name, got, want)
				}
			}
		})
	}
}

func TestExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// missing checks a plan that was never created.
		missing bool
		want    bool
	}{
		{
			name: "Success: a created plan exists",
			want: true,
		},
		{
			name:    "Success: a plan that was never created does not exist",
			missing: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			v, _ := newFakeVault(t)
			plan := newRunningPlan(t, v)
			id := plan.ID
			if test.missing {
				id = workflow.NewV7()
			}

			got, err := v.Exists(ctx, id)
			if err != nil {
				t.Fatalf("TestExists(%s): got err == %s, want err == nil", test.name, err)
			}
			if got != test.want {
				t.Errorf("TestExists(%s): got %v, want %v", test.name, got, test.want)
			}
		})
	}
}

func TestSharedFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// fetchErr is returned by the fetch.
		fetchErr error
		// cancelCaller ends the caller's context from inside the fetch, while the fetch is still running.
		cancelCaller bool
		// cancelledFirst ends the caller's context before it calls.
		cancelledFirst bool
		// outlive makes the fetch run until its own context ends, past a short timeout.
		outlive bool
		// lockWait holds the fetch waiting for its lock for longer than its short timeout.
		lockWait bool
		want     int
		// wantFetchCtxErr means the fetch's context must have ended by the time the fetch returns.
		wantFetchCtxErr bool
		wantErr         bool
	}{
		{
			name: "Success: the caller gets the fetch's result",
			want: 1,
		},
		{
			name:     "Error: the caller gets the fetch's error",
			fetchErr: errors.New("fetch failed"),
			wantErr:  true,
		},
		{
			// Regression: the fetch ran on the first caller's context, so that caller giving up failed every caller
			// sharing it; then it ignored the caller's context, so a caller with a deadline waited for the whole fetch.
			name:         "Error: a caller whose context ends stops waiting, and the fetch it started still finishes",
			cancelCaller: true,
			wantErr:      true,
		},
		{
			// Regression: the fetch's timeout started before its lock was taken, so a fetch that waited behind a writer
			// started with its time already spent and failed every caller sharing it.
			name:     "Success: a fetch that waits for its lock longer than its timeout still gets its full time",
			lockWait: true,
			want:     1,
		},
		{
			// Regression: a caller whose context had already ended still started a fetch that no one would read, and
			// that fetch held the plan's read lock, blocking its writers, for up to its timeout.
			name:           "Error: a caller whose context has already ended does not start a fetch",
			cancelledFirst: true,
			wantErr:        true,
		},
		{
			// Regression: the shared fetch had no deadline of its own, so a stalled read held the plan's read lock, and
			// blocked every writer of the plan, for hours.
			name:            "Error: a fetch that runs past its timeout ends, and its caller gets an error",
			outlive:         true,
			wantFetchCtxErr: true,
			wantErr:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.outlive {
				// Bounds the caller if the fetch is never timed out, so the test fails instead of hanging.
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
				defer cancel()
			}

			f := &sync.Flight[string, int]{}
			release := make(chan struct{})
			// fetchCtxErr receives the fetch's context error when the fetch finishes.
			fetchCtxErr := make(chan error, 1)
			fetch := func(ctx context.Context) (int, error) {
				if test.outlive {
					<-ctx.Done()
					fetchCtxErr <- ctx.Err()
					return 0, ctx.Err()
				}
				if test.cancelCaller {
					cancel() // The caller gives up while this fetch is running.
				}
				<-release
				fetchCtxErr <- ctx.Err()
				return 1, test.fetchErr
			}
			// later receives what a caller asking for the same key gets when it asks just as the lock is released.
			later := make(chan (<-chan sync.FlightResult[int]), 1)
			lock := func() (unlock func()) {
				if test.lockWait {
					time.Sleep(300 * time.Millisecond) // Stands in for a writer holding the lock.
				}
				return func() {
					later <- f.DoChan(t.Context(), "key", func() (int, error) { return 2, nil })
				}
			}
			timeout := time.Minute
			switch {
			case test.outlive:
				timeout = 10 * time.Millisecond
			case test.lockWait:
				// Well under the lock wait, so the row fails if the wait counts against it, with room for scheduling.
				timeout = 100 * time.Millisecond
			}

			if test.cancelledFirst {
				cancel()
			}
			if !test.cancelCaller && !test.cancelledFirst {
				close(release)
			}

			got, err := sharedFetch(ctx, sharedFetchArgs[int]{flight: f, key: "key", lock: lock, fetch: fetch, timeout: timeout})

			if test.cancelledFirst {
				// A fetch the call started would still be blocked here, so a new caller would join it and get 1 rather
				// than start its own and get 2.
				probe := f.DoChan(t.Context(), "key", func() (int, error) { return 2, nil })
				close(release)
				probeCtx, probeCancel := context.WithTimeout(t.Context(), 5*time.Second)
				res, r := chans.Get(probeCtx, probe)
				probeCancel()
				if !r.OK() {
					t.Fatalf("TestSharedFetch(%s): a later caller did not get a result", test.name)
				}
				if res.Val != 2 {
					t.Errorf("TestSharedFetch(%s): got %d from a later caller, want 2 (no fetch left in flight)", test.name, res.Val)
				}
				if err == nil {
					t.Errorf("TestSharedFetch(%s): got err == nil, want err != nil", test.name)
				}
				return
			}
			if test.cancelCaller {
				// The caller has returned while the fetch is still blocked; let the fetch finish now.
				close(release)
			}

			fetchCtx, fetchCancel := context.WithTimeout(t.Context(), 5*time.Second)
			fetchErr, r := chans.Get(fetchCtx, fetchCtxErr)
			fetchCancel()
			if !r.OK() {
				t.Fatalf("TestSharedFetch(%s): the fetch did not finish", test.name)
			}
			if (fetchErr != nil) != test.wantFetchCtxErr {
				t.Errorf("TestSharedFetch(%s): got fetch context error %v, want ended == %v", test.name, fetchErr, test.wantFetchCtxErr)
			}

			// Regression: the fetch stayed shared until after its lock was released, so a caller arriving after a
			// writer took the lock could join it and get what was read before the write.
			laterCtx, laterCancel := context.WithTimeout(t.Context(), 5*time.Second)
			results, r := chans.Get(laterCtx, later)
			laterCancel()
			if !r.OK() {
				t.Fatalf("TestSharedFetch(%s): the lock was not released", test.name)
			}
			if res := <-results; res.Val != 2 {
				t.Errorf("TestSharedFetch(%s): a caller arriving as the lock was released got %d, want a new fetch (2)", test.name, res.Val)
			}

			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestSharedFetch(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestSharedFetch(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				return
			}
			if got != test.want {
				t.Errorf("TestSharedFetch(%s): got %d, want %d", test.name, got, test.want)
			}
		})
	}
}

// TestReadShared is a regression test: Reads of one plan share a fetch, and every caller got the fetch's *workflow.Plan
// itself. A Start or Resume runs the Plan it reads, so a Status or Wait read that joined its fetch shared the run's
// working object: it saw changes not yet written, and the run's writes raced with its reads. Each caller must get its
// own copy, with the fetch still shared and every object still naming its plan, which the updaters lock and name blobs
// by.
func TestReadShared(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	v, fake := newFakeVault(t)
	stored := newRunningPlan(t, v)

	// Hold the shared fetch at its first storage read until both Reads have called.
	var fetches atomic.Int32
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	fake.GetMetadataErr = func(_, blob string) error {
		if blob != planEntryBlobName(stored.ID) {
			return nil
		}
		fetches.Add(1)
		chans.TryPut(entered, struct{}{})
		// gate is only closed.
		chans.Get(ctx, gate)
		return nil
	}

	read := func() *result.Value[*workflow.Plan] {
		res := result.New[*workflow.Plan]()
		context.Pool(ctx).Submit(ctx, func() {
			p, err := v.Read(ctx, stored.ID)
			res.Set(p, err)
		})
		return res
	}
	first := read()
	if _, r := chans.Get(ctx, entered); !r.OK() {
		t.Fatalf("TestReadShared: the first Read never fetched")
	}
	second := read()
	// Give the second Read time to join the fetch the first one holds open; the wait running out is expected.
	joinCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	chans.Get(joinCtx, second.Done())
	cancel()
	close(gate)

	a, err := first.Wait(ctx)
	if err != nil {
		t.Fatalf("TestReadShared: first Read: got err == %s, want err == nil", err)
	}
	b, err := second.Wait(ctx)
	if err != nil {
		t.Fatalf("TestReadShared: second Read: got err == %s, want err == nil", err)
	}

	if got := fetches.Load(); got != 1 {
		t.Errorf("TestReadShared: got %d fetches, want 1 shared by both Reads", got)
	}
	if a == b || a.Blocks[0] == b.Blocks[0] || a.Blocks[0].Sequences[0].Actions[0] == b.Blocks[0].Sequences[0].Actions[0] {
		t.Errorf("TestReadShared: the two Reads share Plan objects, want each to get its own copy")
	}
	if diff := pretty.Compare(a, b); diff != "" {
		t.Errorf("TestReadShared: the two Reads got different Plans, -first +second:\n%s", diff)
	}
	// A copy must keep everything a fetch returns.
	fake.GetMetadataErr = nil
	direct, err := v.reader.ReadDirect(ctx, stored.ID)
	if err != nil {
		t.Fatalf("TestReadShared: ReadDirect: got err == %s, want err == nil", err)
	}
	if diff := pretty.Compare(direct, a); diff != "" {
		t.Errorf("TestReadShared: a Read's copy differs from the fetched Plan, -fetched +read:\n%s", diff)
	}
	for _, p := range []*workflow.Plan{a, b} {
		for item := range walk.Plan(p) {
			if item.Value.Type() == workflow.OTPlan {
				continue
			}
			if got := item.Value.(interface{ GetPlanID() uuid.UUID }).GetPlanID(); got != stored.ID {
				t.Errorf("TestReadShared: got %v object with plan ID %s, want %s", item.Value.Type(), got, stored.ID)
			}
		}
	}
}
