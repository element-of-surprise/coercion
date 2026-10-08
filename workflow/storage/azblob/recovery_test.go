package azblob

import (
	"sync/atomic"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
)

// opCounts counts the fake blob operations a test cares about.
type opCounts struct {
	lists    atomic.Int32
	metadata atomic.Int32
	gets     atomic.Int32
}

// count installs counting hooks on f. The hooks never fail an operation, except that listErr (if set) is returned
// from every list call.
func (c *opCounts) count(f *blobops.Fake, listErr error) {
	f.NextListPageErr = func(string) error {
		c.lists.Add(1)
		return listErr
	}
	f.GetMetadataErr = func(string, string) error {
		c.metadata.Add(1)
		return nil
	}
	f.GetBlobErr = func(string, string) error {
		c.gets.Add(1)
		return nil
	}
}

// finishWithTornObject writes plan's final status the way the incident did: the entry lands, the object upload fails.
func finishWithTornObject(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan, status workflow.Status) {
	t.Helper()

	f.UploadBlobErr = func(_, blobName string) error {
		if blobName == planObjectBlobName(plan.ID) {
			return errFakeUpload
		}
		return nil
	}
	setStatus(plan, status)
	if err := v.UpdatePlan(t.Context(), plan); err == nil {
		t.Fatalf("finishWithTornObject: UpdatePlan: got err == nil, want the object upload to fail")
	}
	f.UploadBlobErr = nil
}

// leaveCompleted stores plan as Completed.
func leaveCompleted(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	setStatus(plan, workflow.Completed)
	if err := v.UpdatePlan(t.Context(), plan); err != nil {
		t.Fatalf("leaveCompleted: UpdatePlan(Completed): %s", err)
	}
	return nil
}

// leaveTornCompletion stores plan's completion in its entry but not its object.
func leaveTornCompletion(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	finishWithTornObject(t, v, f, plan, workflow.Completed)
	return nil
}

// leaveTornCompletionRunningChild stores plan's action as Running, then plan's completion in its entry but not its
// object.
func leaveTornCompletionRunningChild(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	action := plan.Blocks[0].Sequences[0].Actions[0]
	action.State.Set(workflow.State{Status: workflow.Running})
	if err := v.UpdateAction(t.Context(), action); err != nil {
		t.Fatalf("leaveTornCompletionRunningChild: UpdateAction(Running): %s", err)
	}
	finishWithTornObject(t, v, f, plan, workflow.Completed)
	return nil
}

// leaveNoObject deletes plan's object blob, as a create that never wrote it leaves it.
func leaveNoObject(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	if err := f.DeleteBlob(t.Context(), containerForPlan("test", plan.ID), planObjectBlobName(plan.ID)); err != nil {
		t.Fatalf("leaveNoObject: deleting object: %s", err)
	}
	return nil
}

// leaveNoEntry deletes plan's entry blob, as a delete that removed the entry first leaves it.
func leaveNoEntry(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	if err := f.DeleteBlob(t.Context(), containerForPlan("test", plan.ID), planEntryBlobName(plan.ID)); err != nil {
		t.Fatalf("leaveNoEntry: deleting entry: %s", err)
	}
	return nil
}

// leaveUnreadableObjectMeta rewrites plan's object blob with metadata that does not parse.
func leaveUnreadableObjectMeta(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	ctx := t.Context()
	containerName := containerForPlan("test", plan.ID)
	objectName := planObjectBlobName(plan.ID)
	data, err := f.GetBlob(ctx, containerName, objectName)
	if err != nil {
		t.Fatalf("leaveUnreadableObjectMeta: reading object: %s", err)
	}
	md := map[string]*string{mdPlanType: toPtr(ptObject), mdKeyPlanID: toPtr(plan.ID.String()), mdKeyState: toPtr("not json")}
	if err := f.UploadBlob(ctx, containerName, objectName, md, data); err != nil {
		t.Fatalf("leaveUnreadableObjectMeta: corrupting object metadata: %s", err)
	}
	return nil
}

// leaveObjectAppears deletes plan's object blob and returns a restore that writes it back, as a create that finishes
// after recovery's scan does.
func leaveObjectAppears(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func()) {
	t.Helper()

	ctx := t.Context()
	containerName := containerForPlan("test", plan.ID)
	objectName := planObjectBlobName(plan.ID)
	md, err := f.GetMetadata(ctx, containerName, objectName)
	if err != nil {
		t.Fatalf("leaveObjectAppears: reading object metadata: %s", err)
	}
	data, err := f.GetBlob(ctx, containerName, objectName)
	if err != nil {
		t.Fatalf("leaveObjectAppears: reading object: %s", err)
	}
	if err := f.DeleteBlob(ctx, containerName, objectName); err != nil {
		t.Fatalf("leaveObjectAppears: deleting object: %s", err)
	}
	return func() {
		if err := f.UploadBlob(ctx, containerName, objectName, md, data); err != nil {
			t.Errorf("leaveObjectAppears: restoring object: %s", err)
		}
	}
}

func TestRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// leave changes how the plan is stored before Recovery runs, starting from a Running plan. nil leaves it
		// Running. A restore it returns runs the first time recovery looks up the plan's object, after the scan.
		leave func(t *testing.T, v *Vault, f *blobops.Fake, plan *workflow.Plan) (restore func())
		// listFails makes every container listing fail.
		listFails bool

		wantRunning bool
		wantEntry   bool
		// wantNoPlanRequests means recovery must need nothing for the plan beyond the listing.
		wantNoPlanRequests bool
		// wantObjectStatus is the object's stored status afterwards. It is not checked if skipObjectStatus is set or
		// the object does not exist.
		wantObjectStatus workflow.Status
		skipObjectStatus bool
		wantErr          bool
	}{
		{
			name:               "Success: a running plan is handed to startup recovery and left alone",
			wantRunning:        true,
			wantEntry:          true,
			wantNoPlanRequests: true,
			wantObjectStatus:   workflow.Running,
		},
		{
			name:               "Success: a completed plan is left alone",
			leave:              leaveCompleted,
			wantEntry:          true,
			wantNoPlanRequests: true,
			wantObjectStatus:   workflow.Completed,
		},
		{
			name:             "Success: a completion whose object upload failed has its object repaired from the entry",
			leave:            leaveTornCompletion,
			wantEntry:        true,
			wantObjectStatus: workflow.Completed,
		},
		{
			name:  "Success: a create that never wrote its object has its entry deleted",
			leave: leaveNoObject,
		},
		{
			name:             "Success: an object left by a partial delete is ignored and not treated as running",
			leave:            leaveNoEntry,
			wantObjectStatus: workflow.Running,
		},
		{
			// Regression: repair used to save a Completed plan over an action still stored as Running, after which the
			// entry and object agreed and nothing ever flagged it again. The tear must be left for reads to rebuild.
			name:             "Success: a torn completion whose action is still Running is not repaired",
			leave:            leaveTornCompletionRunningChild,
			wantEntry:        true,
			wantObjectStatus: workflow.Running,
		},
		{
			// Regression: the scan skipped a blob whose metadata did not parse, so an object with bad metadata looked
			// missing and recovery deleted the plan's valid entry, hiding the plan.
			name:             "Success: an object blob with unreadable metadata does not get its entry deleted",
			leave:            leaveUnreadableObjectMeta,
			wantRunning:      true,
			wantEntry:        true,
			skipObjectStatus: true,
		},
		{
			// Regression: the orphan delete trusted the scan, so a create that wrote its object after the scan lost its
			// entry. The object is written back just before recovery checks for it.
			name:             "Success: an entry whose object appears after the scan is not deleted",
			leave:            leaveObjectAppears,
			wantEntry:        true,
			wantObjectStatus: workflow.Running,
		},
		{
			name:      "Error: a failed container listing fails recovery",
			listFails: true,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			v, fake := newFakeVault(t)
			plan := newRunningPlan(t, v)
			containerName := containerForPlan("test", plan.ID)
			var restore func()
			if test.leave != nil {
				restore = test.leave(t, v, fake, plan)
			}

			var listErr error
			if test.listFails {
				listErr = errors.New("list failed")
			}
			var ops opCounts
			ops.count(fake, listErr)
			if restore != nil {
				// Write the object back the first time recovery looks it up, after the scan has run.
				counted := fake.GetMetadataErr
				fake.GetMetadataErr = func(c, b string) error {
					if b == planObjectBlobName(plan.ID) && restore != nil {
						r := restore
						restore = nil
						r()
					}
					return counted(c, b)
				}
			}

			err := v.Recovery(ctx)
			switch {
			case err == nil && test.wantErr:
				t.Errorf("TestRecovery(%s): got err == nil, want err != nil", test.name)
				return
			case err != nil && !test.wantErr:
				t.Errorf("TestRecovery(%s): got err == %s, want err == nil", test.name, err)
				return
			case err != nil:
				if _, ok := v.RecoveredRunning(); ok {
					t.Errorf("TestRecovery(%s): a failed Recovery must not hand over running plans", test.name)
				}
				// Regression: failed listings of several containers were joined and returned with no category or type.
				e, ok := err.(errors.Error)
				if !ok || e.Type != errors.TypeStorageList {
					t.Errorf("TestRecovery(%s): got error %T(%v), want an errors.Error with type %v", test.name, err, err, errors.TypeStorageList)
				}
				return
			}

			// Each container in the retention window is listed exactly once.
			if got, want := int(ops.lists.Load()), len(searchContainerNames("test", 14)); got != want {
				t.Errorf("TestRecovery(%s): got %d container listings, want %d", test.name, got, want)
			}
			// A consistent plan costs nothing beyond the listing.
			if test.wantNoPlanRequests {
				if n := ops.metadata.Load() + ops.gets.Load(); n != 0 {
					t.Errorf("TestRecovery(%s): got %d per-plan requests for a consistent plan, want 0", test.name, n)
				}
			}

			results, ok := v.RecoveredRunning()
			if !ok {
				t.Errorf("TestRecovery(%s): RecoveredRunning() got ok == false, want true after Recovery", test.name)
			}
			gotRunning := false
			for _, lr := range results {
				if lr.ID == plan.ID {
					gotRunning = true
				}
			}
			if gotRunning != test.wantRunning {
				t.Errorf("TestRecovery(%s): got plan handed over as running == %v, want %v", test.name, gotRunning, test.wantRunning)
			}
			if _, ok := v.RecoveredRunning(); ok {
				t.Errorf("TestRecovery(%s): second RecoveredRunning() got ok == true, want false", test.name)
			}

			fake.GetMetadataErr, fake.GetBlobErr = nil, nil
			if got := fake.BlobExists(containerName, planEntryBlobName(plan.ID)); got != test.wantEntry {
				t.Errorf("TestRecovery(%s): got entry exists == %v, want %v", test.name, got, test.wantEntry)
			}
			if !fake.BlobExists(containerName, planObjectBlobName(plan.ID)) || test.skipObjectStatus {
				return
			}
			if got := blobState(t, fake, containerName, planObjectBlobName(plan.ID)); got != test.wantObjectStatus {
				t.Errorf("TestRecovery(%s): got object status %v, want %v", test.name, got, test.wantObjectStatus)
			}
		})
	}
}
