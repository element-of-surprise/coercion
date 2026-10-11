// Package changes allows detecting changes to a Plan by evaluating a child object's state and, for an Action,
// the number of attempts. This only works if you are snapshotting the same Plan at different states.
package changes

import (
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// Snapshot is a state snapshot of a Plan's child objects.
type Snapshot []ObjectSnapshot

// ObjectSnapshot is a snapshot of a single object below a Plan, recording its state and, for an Action,
// the number of attempts.
type ObjectSnapshot struct {
	// State is the state of the object.
	State workflow.State
	// Attempts is the number of attempts for an Action. For other objects, it is always 0.
	Attempts int
}

// Record records the state of every object below plan, in walk order.
func Record(plan *workflow.Plan) Snapshot {
	var snaps Snapshot
	for item := range walk.Plan(plan, walk.WithSkipPlan()) {
		snaps = append(snaps, snapshotOf(item))
	}
	return snaps
}

// Since returns the objects below plan whose state (or, for an Action, attempt count) differs from before, which Record
// took before the objects were changed in place. The Plan itself is never returned. Objects are compared by walk order,
// so plan must have the same shape it had when before was recorded; any object past the end of before counts as changed.
func Since(plan *workflow.Plan, before Snapshot) []walk.Item {
	var changed []walk.Item

	i := 0
	for item := range walk.Plan(plan, walk.WithSkipPlan()) {
		if i >= len(before) || snapshotOf(item) != before[i] {
			changed = append(changed, item)
		}
		i++
	}
	return changed
}

func snapshotOf(item walk.Item) ObjectSnapshot {
	snap := ObjectSnapshot{State: item.Value.(walk.Stateful).GetState()}
	if item.Value.Type() == workflow.OTAction {
		snap.Attempts = len(item.Action().Attempts.Get())
	}
	return snap
}
