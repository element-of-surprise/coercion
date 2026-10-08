package azblob

import (
	"fmt"
	"strings"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

const (
	// Directory prefixes for different object types
	plansDir           = "plans"
	blocksDir          = "blocks"
	sequencesDir       = "sequences"
	checksDir          = "checks"
	actionsDir         = "actions"
	deferredActionsDir = "deferredactions"
	deferBatchesDir    = "deferbatches"
)

// containerName returns the container name for a given date.
// Format: <prefix>-YYYY-MM-DD
func containerName(prefix string, date time.Time) string {
	dateStr := date.UTC().Format(time.DateOnly)
	return fmt.Sprintf("%s-%s", prefix, dateStr)
}

// containerForPlan returns the container name for a plan based on its submit time.
// This ensures the plan and all its sub-objects are in the same container.
func containerForPlan(prefix string, id uuid.UUID) string {
	return containerName(prefix, time.Unix(id.Time().UnixTime()).UTC())
}

// searchContainerNames returns a list of container names to search for plans,
// covering the full retention period. This ensures that recovery can find
// plans that may be several days old.
func searchContainerNames(prefix string, retentionDays int) []string {
	if retentionDays <= 0 {
		return nil
	}
	now := time.Now().UTC()
	containers := make([]string, 0, retentionDays)

	for i := 0; i < retentionDays; i++ {
		date := now.AddDate(0, 0, -i)
		containers = append(containers, containerName(prefix, date))
	}

	return containers
}

//go:generate go tool github.com/gostdlib/base/values/generators/stringer -type=blobKind -linecomment

// blobKind is which of a plan's two top-level blobs a blob is.
type blobKind uint8

const (
	unknownBlobKind blobKind = iota // Unknown
	entryBlob                       // Entry
	objectBlob                      // Object
)

// planType returns the plantype metadata value that blobs of kind k carry, or "" for an unknown kind.
func (k blobKind) planType() string {
	switch k {
	case entryBlob:
		return ptEntry
	case objectBlob:
		return ptObject
	}
	return ""
}

// parsePlanBlobName returns the plan ID and kind of a plan entry or object blob name, as written by planEntryBlobName
// and planObjectBlobName. ok is false for any other name.
func parsePlanBlobName(name string) (id uuid.UUID, kind blobKind, ok bool) {
	rest, ok := strings.CutPrefix(name, planBlobPrefix())
	if !ok {
		return uuid.Nil, unknownBlobKind, false
	}
	for _, k := range []blobKind{entryBlob, objectBlob} {
		idStr, ok := strings.CutSuffix(rest, "-"+k.planType()+".json")
		if !ok {
			continue
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return uuid.Nil, unknownBlobKind, false
		}
		return id, k, true
	}
	return uuid.Nil, unknownBlobKind, false
}

// planEntryBlobName returns the blob name for a lightweight planEntry object.
// This is always written first and contains only IDs for sub-objects.
// Format: plans/<plan-id>-entry.json
func planEntryBlobName(planID uuid.UUID) string {
	return fmt.Sprintf("%s/%s-entry.json", plansDir, planID.String())
}

// planObjectBlobName returns the blob name for a full workflow.Plan object.
// This is written last and contains the complete embedded hierarchy.
// Format: plans/<plan-id>-object.json
func planObjectBlobName(planID uuid.UUID) string {
	return fmt.Sprintf("%s/%s-object.json", plansDir, planID.String())
}

// blockBlobName returns the blob name for a Block object.
// Format: blocks/<plan-id>/<block-id>.json
func blockBlobName(planID, blockID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", blocksDir, planID.String(), blockID.String())
}

// sequenceBlobName returns the blob name for a Sequence object.
// Format: sequences/<plan-id>/<sequence-id>.json
func sequenceBlobName(planID, sequenceID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", sequencesDir, planID.String(), sequenceID.String())
}

// checksBlobName returns the blob name for a Checks object.
// Format: checks/<plan-id>/<checks-id>.json
func checksBlobName(planID, checksID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", checksDir, planID.String(), checksID.String())
}

// actionBlobName returns the blob name for an Action object.
// Format: actions/<plan-id>/<action-id>.json
func actionBlobName(planID, actionID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", actionsDir, planID.String(), actionID.String())
}

// deferredActionsBlobName returns the blob name for a DeferredActions object.
// Format: deferredactions/<plan-id>/<deferredactions-id>.json
func deferredActionsBlobName(planID, daID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", deferredActionsDir, planID.String(), daID.String())
}

// deferBatchBlobName returns the blob name for a DeferBatch object.
// Format: deferbatches/<plan-id>/<batch-id>.json
func deferBatchBlobName(planID, batchID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/%s.json", deferBatchesDir, planID.String(), batchID.String())
}

// blobNameForObject returns the blob name for any workflow object.
func blobNameForObject(obj workflow.Object) string {
	switch o := obj.(type) {
	case *workflow.Plan:
		return planObjectBlobName(o.ID)
	case *workflow.Block:
		return blockBlobName(o.GetPlanID(), o.ID)
	case *workflow.Sequence:
		return sequenceBlobName(o.GetPlanID(), o.ID)
	case *workflow.Checks:
		return checksBlobName(o.GetPlanID(), o.ID)
	case *workflow.Action:
		return actionBlobName(o.GetPlanID(), o.ID)
	case *workflow.DeferredActions:
		return deferredActionsBlobName(o.GetPlanID(), o.ID)
	case *workflow.DeferBatch:
		return deferBatchBlobName(o.GetPlanID(), o.ID)
	default:
		panic(fmt.Sprintf("bug: unknown object type %T", obj))
	}
}

// planBlobPrefix returns the prefix for listing all plan blobs in a container.
func planBlobPrefix() string {
	return plansDir + "/"
}

// objectBlobPrefix returns the prefix for listing all blobs for a specific plan.
func objectBlobPrefix(planID uuid.UUID) string {
	return fmt.Sprintf("%s/%s/", blocksDir, planID.String())
}

// toPtr is a generic helper to get a pointer to a value.
func toPtr[T any](v T) *T {
	return &v
}
