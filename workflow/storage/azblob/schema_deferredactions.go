package azblob

import (
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

// deferredActionsEntry represents a DeferredActions object in blob storage.
// DeferredBatches holds the batch IDs in order; the parent is the authority for
// ordering so DeferBatch entries don't duplicate position info.
type deferredActionsEntry struct {
	Type            workflow.ObjectType `json:"type"`
	ID              uuid.UUID           `json:"id"`
	PlanID          uuid.UUID           `json:"planID"`
	DeferredBatches []uuid.UUID         `json:"deferredBatches,omitempty"`
	StateStatus     workflow.Status     `json:"stateStatus"`
	StateStart      time.Time           `json:"stateStart,omitzero"`
	StateEnd        time.Time           `json:"stateEnd,omitzero"`
}

// deferBatchesEntry represents a DeferBatch object in blob storage. It carries
// only fields that live on the workflow.DeferBatch itself, so updates can
// re-marshal the entry without needing to remember position info (the parent
// deferredActionsEntry owns that ordering).
type deferBatchesEntry struct {
	Type        workflow.ObjectType   `json:"type"`
	ID          uuid.UUID             `json:"id"`
	Key         uuid.UUID             `json:"key,omitempty"`
	PlanID      uuid.UUID             `json:"planID"`
	When        workflow.WhenDeferred `json:"when"`
	FailElement bool                  `json:"failElement,omitempty"`
	Name        string                `json:"name"`
	Descr       string                `json:"descr"`
	Actions     []uuid.UUID           `json:"actions,omitempty"`
	StateStatus workflow.Status       `json:"stateStatus"`
	StateStart  time.Time             `json:"stateStart,omitzero"`
	StateEnd    time.Time             `json:"stateEnd,omitzero"`
}

// deferredActionsToEntry converts a workflow.DeferredActions to a deferredActionsEntry.
func deferredActionsToEntry(da *workflow.DeferredActions) (deferredActionsEntry, error) {
	if da == nil {
		return deferredActionsEntry{}, fmt.Errorf("deferred actions cannot be nil")
	}
	if da.ID == uuid.Nil {
		return deferredActionsEntry{}, fmt.Errorf("deferred actions must have an ID")
	}

	entry := deferredActionsEntry{
		Type:        workflow.OTDeferredActions,
		ID:          da.ID,
		PlanID:      da.GetPlanID(),
		StateStatus: workflow.NotStarted,
	}

	if state := da.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
	}

	entry.DeferredBatches = make([]uuid.UUID, len(da.DeferredBatches))
	for i, b := range da.DeferredBatches {
		entry.DeferredBatches[i] = b.ID
	}
	return entry, nil
}

// entryToDeferredActions converts a deferredActionsEntry back to a workflow.DeferredActions.
func entryToDeferredActions(entry deferredActionsEntry) *workflow.DeferredActions {
	da := &workflow.DeferredActions{ID: entry.ID}
	da.State.Set(workflow.State{
		Status: entry.StateStatus,
		Start:  entry.StateStart,
		End:    entry.StateEnd,
	})
	da.SetPlanID(entry.PlanID)
	return da
}

// deferBatchToEntry converts a workflow.DeferBatch to a deferBatchesEntry.
func deferBatchToEntry(b *workflow.DeferBatch) (deferBatchesEntry, error) {
	if b == nil {
		return deferBatchesEntry{}, fmt.Errorf("defer batch cannot be nil")
	}
	if b.ID == uuid.Nil {
		return deferBatchesEntry{}, fmt.Errorf("defer batch must have an ID")
	}

	entry := deferBatchesEntry{
		Type:        workflow.OTBatch,
		ID:          b.ID,
		Key:         b.Key,
		PlanID:      b.GetPlanID(),
		When:        b.When,
		FailElement: b.FailElement,
		Name:        b.Name,
		Descr:       b.Descr,
		StateStatus: workflow.NotStarted,
	}

	if state := b.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
	}

	entry.Actions = make([]uuid.UUID, len(b.Actions))
	for i, a := range b.Actions {
		entry.Actions[i] = a.ID
	}
	return entry, nil
}

// entryToDeferBatch converts a deferBatchesEntry back to a workflow.DeferBatch.
func entryToDeferBatch(entry deferBatchesEntry) *workflow.DeferBatch {
	b := &workflow.DeferBatch{When: entry.When, FailElement: entry.FailElement}
	b.ID = entry.ID
	b.Key = entry.Key
	b.Name = entry.Name
	b.Descr = entry.Descr
	b.State.Set(workflow.State{
		Status: entry.StateStatus,
		Start:  entry.StateStart,
		End:    entry.StateEnd,
	})
	b.SetPlanID(entry.PlanID)
	return b
}
