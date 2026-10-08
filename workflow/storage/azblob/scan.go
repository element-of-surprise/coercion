package azblob

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
)

// scanConcurrency is how many containers are listed at once during a scan.
const scanConcurrency = 10

// scannedPlan is what one listing of the plans/ directories says about a single plan.
type scannedPlan struct {
	id        uuid.UUID
	container string
	// hasEntry and hasObject report whether the plan's entry and object blobs exist, from their names.
	hasEntry  bool
	hasObject bool
	// entry is the plan's entry blob metadata, which is the authority for its state. nil if there is no entry blob or
	// its metadata could not be parsed.
	entry *planMeta
	// object is the plan's object blob metadata. nil if there is no object blob or its metadata could not be parsed.
	object *planMeta
}

// scanPlans lists the plans/ directory of every container in the retention window once and returns what it found
// for each plan, in listing order. Both entry and object blobs live under plans/, so this one pass gives recovery
// everything it needs without a per-plan request. Plans older than the retention window are left out, matching Read.
func (r reader) scanPlans(ctx context.Context) ([]scannedPlan, error) {
	containers := searchContainerNames(r.prefix, r.retentionDays)
	found := make([][]planBlob, len(containers))

	g := context.Pool(ctx).Default().Limited(ctx, "azBlobRecoveryScan", scanConcurrency).Group()
	for i, cn := range containers {
		g.Go(ctx, func(ctx context.Context) error {
			blobs, err := r.listPlanBlobs(ctx, cn, false)
			if err != nil {
				if blobops.IsNotFound(err) {
					return nil // Nothing was written that day.
				}
				return err
			}
			found[i] = blobs
			return nil
		})
	}
	if err := unwrapGroup(g.Wait(ctx)); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageList, fmt.Errorf("recovery scan failed: %w", err))
	}

	cutoff := r.now().AddDate(0, 0, -r.retentionDays)
	// byID maps a plan ID to its index in plans.
	byID := map[uuid.UUID]int{}
	var plans []scannedPlan
	for i, blobs := range found {
		for _, b := range blobs {
			if time.Unix(b.id.Time().UnixTime()).Before(cutoff) {
				continue
			}
			idx, ok := byID[b.id]
			if !ok {
				idx = len(plans)
				byID[b.id] = idx
				plans = append(plans, scannedPlan{id: b.id, container: containers[i]})
			}
			sp := plans[idx]
			switch b.kind {
			case entryBlob:
				sp.hasEntry, sp.entry = true, b.meta
			case objectBlob:
				sp.hasObject, sp.object = true, b.meta
			}
			plans[idx] = sp
		}
	}
	return plans, nil
}
