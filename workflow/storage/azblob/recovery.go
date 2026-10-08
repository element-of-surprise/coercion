package azblob

import (
	"fmt"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
)

var (
	_ storage.Recovery         = recovery{}
	_ storage.RecoveredRunning = recovery{}
)

// recovery implements the storage.Recovery and storage.RecoveredRunning interfaces.
type recovery struct {
	reader   reader
	uploader *uploader
	// running holds the Running plans the last Recovery found until startup recovery takes them. It is shared by every
	// copy of the recovery, so it must not be nil; wire sets it.
	running *sync.MutexValue[runningSnapshot]

	private.Storage
}

// runningSnapshot is the Running plans found by the last Recovery, held until startup recovery takes them instead of
// listing every container again with a Search. ok is false when nothing is held.
type runningSnapshot struct {
	results []storage.ListResult
	ok      bool
}

// Recovery implements storage.Recovery.Recovery().
func (r recovery) Recovery(ctx context.Context) error {
	plans, err := r.reader.scanPlans(ctx)
	if err != nil {
		return err
	}

	var running []storage.ListResult
	// Like scanPlans, the fan-out takes its limit from the default pool, never from a Limited pool the caller's Context
	// may carry: these jobs can wait minutes on plan locks and blob retries.
	g := context.Pool(ctx).Default().Limited(ctx, "azBlobRecoveryPlans", fetchConcurrency).Group()
	for _, sp := range plans {
		switch {
		case !sp.hasEntry:
			continue
		case sp.entry == nil:
			// The entry decides the plan's state and it cannot be read. Leave the plan alone rather than guess.
			context.Log(ctx).Error(fmt.Sprintf("azblob: plan(%s) entry metadata could not be read, skipping it in recovery", sp.id))
			continue
		case !sp.hasObject:
			g.Go(ctx, func(ctx context.Context) error {
				r.deleteOrphanedEntry(ctx, sp)
				return nil
			})
			continue
		case sp.object == nil:
			// The object exists, so this is not a create that never finished; only its metadata is unreadable. Do not
			// delete or repair anything based on it.
			context.Log(ctx).Error(fmt.Sprintf("azblob: plan(%s) object metadata could not be read, not checking it for a torn write", sp.id))
		case sp.entry.State.Status > workflow.Running && sp.object.State.Status != sp.entry.State.Status:
			g.Go(ctx, func(ctx context.Context) error {
				if err := r.repairObject(ctx, sp); err != nil {
					context.Log(ctx).Warn(fmt.Sprintf("azblob: could not repair plan(%s) object, reads rebuild it from the entry: %v", sp.id, err))
				}
				return nil
			})
		}
		if sp.entry.State.Status == workflow.Running {
			running = append(running, sp.entry.ListResult)
		}
	}
	if err := g.Wait(ctx); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("recovery did not finish: %w", unwrapGroup(err)))
	}

	r.running.Store(runningSnapshot{results: running, ok: true})
	return nil
}

// RecoveredRunning implements storage.RecoveredRunning.RecoveredRunning().
func (r recovery) RecoveredRunning() ([]storage.ListResult, bool) {
	snap := r.running.Swap(runningSnapshot{})
	return snap.results, snap.ok
}

// deleteOrphanedEntry deletes the entry blob of a plan whose create never wrote its object. The scan is a snapshot, so
// under the plan's lock it checks again that the object is still missing: a create that finished after the scan must
// not lose its entry.
func (r recovery) deleteOrphanedEntry(ctx context.Context, sp scannedPlan) {
	r.reader.mu.Lock(sp.id)
	defer r.reader.mu.Unlock(sp.id)

	_, err := r.reader.client.GetMetadata(ctx, sp.container, planObjectBlobName(sp.id))
	switch {
	case err == nil:
		return // The object exists now; the create finished.
	case !blobops.IsNotFound(err):
		context.Log(ctx).Warn(fmt.Sprintf("azblob: could not check plan(%s) object before deleting its entry, leaving it: %v", sp.id, err))
		return
	}

	if err := deleteEntry(ctx, r.reader.client, sp.container, sp.id); err != nil {
		context.Log(ctx).Warn(fmt.Sprintf("azblob: failed to delete orphaned planEntry for plan(%s), the next recovery will retry: %v", sp.id, err))
	}
}

// repairObject rewrites a plan's object blob to match its entry, after a final write updated the entry but not the
// object.
func (r recovery) repairObject(ctx context.Context, sp scannedPlan) error {
	r.reader.mu.Lock(sp.id)
	defer r.reader.mu.Unlock(sp.id)

	// fetchPlan, not Read: Read takes the plan's read lock, which is not reentrant with the write lock held here.
	plan, err := r.reader.fetchPlan(ctx, sp.id)
	if err != nil {
		return err
	}
	// A Completed Plan with Running objects cannot be settled from what is stored. Leave the tear rather than save the
	// contradiction: the entry and object would then agree, and reads would stop rebuilding and flagging it.
	if plan.State.Get().Status == workflow.Completed && hasRunningObjects(plan) {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageInconsistent, fmt.Errorf("plan(%s) is Completed but has Running objects, not repairing its object", sp.id))
	}
	md, err := planToMetadata(ctx, plan)
	if err != nil {
		return err
	}
	if err := r.uploader.uploadPlanObject(ctx, plan, md, uptComplete); err != nil {
		return err
	}
	context.Log(ctx).Info(fmt.Sprintf("azblob: repaired plan(%s) object to match its entry status %v", sp.id, sp.entry.State.Status))
	return nil
}
