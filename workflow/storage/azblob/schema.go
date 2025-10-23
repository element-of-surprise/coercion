package azblob

import (
	"fmt"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// planEntry represents a lightweight Plan structure in blob storage with IDs only.
// This is used for running plans and contains only references to sub-objects.
type planEntry struct {
	Type           workflow.ObjectType    `json:"type"`
	ID             uuid.UUID              `json:"id"`
	PlanID         uuid.UUID              `json:"planID"` // Duplicate of ID for consistency
	GroupID        uuid.UUID              `json:"groupID,omitempty"`
	Name           string                 `json:"name"`
	Descr          string                 `json:"descr"`
	Meta           []byte                 `json:"meta,omitempty"`
	BypassChecks   uuid.UUID              `json:"bypassChecks,omitempty"`
	PreChecks      uuid.UUID              `json:"preChecks,omitempty"`
	PostChecks     uuid.UUID              `json:"postChecks,omitempty"`
	ContChecks     uuid.UUID              `json:"contChecks,omitempty"`
	DeferredChecks uuid.UUID              `json:"deferredChecks,omitempty"`
	Blocks         []uuid.UUID            `json:"blocks,omitempty"`
	StateStatus    workflow.Status        `json:"stateStatus"`
	StateStart     time.Time              `json:"stateStart,omitempty"`
	StateEnd       time.Time              `json:"stateEnd,omitempty"`
	SubmitTime     time.Time              `json:"submitTime"`
	Reason         workflow.FailureReason `json:"reason,omitempty"`
}

// blocksEntry represents a Block object in blob storage.
type blocksEntry struct {
	Type              workflow.ObjectType `json:"type"`
	ID                uuid.UUID           `json:"id"`
	Key               uuid.UUID           `json:"key,omitempty"`
	PlanID            uuid.UUID           `json:"planID"`
	Name              string              `json:"name"`
	Descr             string              `json:"descr"`
	Pos               int                 `json:"pos"`
	EntranceDelay     time.Duration       `json:"entranceDelay,omitempty"`
	ExitDelay         time.Duration       `json:"exitDelay,omitempty"`
	BypassChecks      uuid.UUID           `json:"bypassChecks,omitempty"`
	PreChecks         uuid.UUID           `json:"preChecks,omitempty"`
	PostChecks        uuid.UUID           `json:"postChecks,omitempty"`
	ContChecks        uuid.UUID           `json:"contChecks,omitempty"`
	DeferredChecks    uuid.UUID           `json:"deferredChecks,omitempty"`
	Sequences         []uuid.UUID         `json:"sequences,omitempty"`
	Concurrency       int                 `json:"concurrency"`
	ToleratedFailures int                 `json:"toleratedFailures"`
	StateStatus       workflow.Status     `json:"stateStatus"`
	StateStart        time.Time           `json:"stateStart,omitzero"`
	StateEnd          time.Time           `json:"stateEnd,omitzero"`
}

// checksEntry represents a Checks object in blob storage.
type checksEntry struct {
	Type        workflow.ObjectType `json:"type"`
	ID          uuid.UUID           `json:"id"`
	Key         uuid.UUID           `json:"key,omitempty"`
	PlanID      uuid.UUID           `json:"planID"`
	Actions     []uuid.UUID         `json:"actions,omitempty"`
	Delay       time.Duration       `json:"delay,omitempty"`
	StateStatus workflow.Status     `json:"stateStatus"`
	StateStart  time.Time           `json:"stateStart,omitzero"`
	StateEnd    time.Time           `json:"stateEnd,omitzero"`
}

// sequencesEntry represents a Sequence object in blob storage.
type sequencesEntry struct {
	Type        workflow.ObjectType `json:"type"`
	ID          uuid.UUID           `json:"id"`
	Key         uuid.UUID           `json:"key,omitempty"`
	PlanID      uuid.UUID           `json:"planID"`
	Name        string              `json:"name"`
	Descr       string              `json:"descr"`
	Pos         int                 `json:"pos"`
	Actions     []uuid.UUID         `json:"actions,omitempty"`
	StateStatus workflow.Status     `json:"stateStatus"`
	StateStart  time.Time           `json:"stateStart,omitzero"`
	StateEnd    time.Time           `json:"stateEnd,omitzero"`
}

// actionsEntry represents an Action object in blob storage.
type actionsEntry struct {
	Type        workflow.ObjectType `json:"type"`
	ID          uuid.UUID           `json:"id"`
	Key         uuid.UUID           `json:"key,omitempty"`
	PlanID      uuid.UUID           `json:"planID"`
	Name        string              `json:"name"`
	Descr       string              `json:"descr"`
	Pos         int                 `json:"pos"`
	Plugin      string              `json:"plugin"`
	Timeout     time.Duration       `json:"timeout"`
	Retries     int                 `json:"retries"`
	Req         []byte              `json:"req,omitempty"`
	Attempts    []byte              `json:"attempts,omitempty"`
	StateStatus workflow.Status     `json:"stateStatus"`
	StateStart  time.Time           `json:"stateStart,omitzero"`
	StateEnd    time.Time           `json:"stateEnd,omitzero"`
}

// planToPlanEntry converts a workflow.Plan to a planEntry (lightweight, IDs only).
func planToPlanEntry(p *workflow.Plan) (planEntry, error) {
	if p == nil {
		return planEntry{}, fmt.Errorf("plan cannot be nil")
	}
	if p.ID == uuid.Nil {
		return planEntry{}, fmt.Errorf("plan must have an ID")
	}

	entry := planEntry{
		Type:        workflow.OTPlan,
		ID:          p.ID,
		PlanID:      p.ID, // Duplicate for consistency
		GroupID:     p.GroupID,
		Name:        p.Name,
		Descr:       p.Descr,
		Meta:        p.Meta,
		SubmitTime:  p.SubmitTime,
		Reason:      p.Reason,
		StateStatus: workflow.NotStarted,
	}

	if p.State != nil {
		entry.StateStatus = p.State.Status
		entry.StateStart = p.State.Start
		entry.StateEnd = p.State.End
	}

	// Set IDs for sub-objects (lightweight references only)
	if p.BypassChecks != nil {
		entry.BypassChecks = p.BypassChecks.ID
	}
	if p.PreChecks != nil {
		entry.PreChecks = p.PreChecks.ID
	}
	if p.PostChecks != nil {
		entry.PostChecks = p.PostChecks.ID
	}
	if p.ContChecks != nil {
		entry.ContChecks = p.ContChecks.ID
	}
	if p.DeferredChecks != nil {
		entry.DeferredChecks = p.DeferredChecks.ID
	}

	entry.Blocks = make([]uuid.UUID, len(p.Blocks))
	for i, b := range p.Blocks {
		entry.Blocks[i] = b.ID
	}

	return entry, nil
}

// blockToEntry converts a workflow.Block to a blocksEntry.
func blockToEntry(b *workflow.Block, pos int) (blocksEntry, error) {
	if b == nil {
		return blocksEntry{}, fmt.Errorf("block cannot be nil")
	}
	if b.ID == uuid.Nil {
		return blocksEntry{}, fmt.Errorf("block must have an ID")
	}

	entry := blocksEntry{
		Type:              workflow.OTBlock,
		ID:                b.ID,
		Key:               b.Key,
		PlanID:            b.GetPlanID(),
		Name:              b.Name,
		Descr:             b.Descr,
		Pos:               pos,
		EntranceDelay:     b.EntranceDelay,
		ExitDelay:         b.ExitDelay,
		Concurrency:       b.Concurrency,
		ToleratedFailures: b.ToleratedFailures,
		StateStatus:       workflow.NotStarted,
	}

	if b.State != nil {
		entry.StateStatus = b.State.Status
		entry.StateStart = b.State.Start
		entry.StateEnd = b.State.End
	}

	// Set IDs for sub-objects
	if b.BypassChecks != nil {
		entry.BypassChecks = b.BypassChecks.ID
	}
	if b.PreChecks != nil {
		entry.PreChecks = b.PreChecks.ID
	}
	if b.PostChecks != nil {
		entry.PostChecks = b.PostChecks.ID
	}
	if b.ContChecks != nil {
		entry.ContChecks = b.ContChecks.ID
	}
	if b.DeferredChecks != nil {
		entry.DeferredChecks = b.DeferredChecks.ID
	}

	entry.Sequences = make([]uuid.UUID, len(b.Sequences))
	for i, s := range b.Sequences {
		entry.Sequences[i] = s.ID
	}

	return entry, nil
}

// entryToBlock converts a blocksEntry back to a workflow.Block.
func entryToBlock(entry blocksEntry) (*workflow.Block, error) {
	b := &workflow.Block{
		ID:                entry.ID,
		Key:               entry.Key,
		Name:              entry.Name,
		Descr:             entry.Descr,
		EntranceDelay:     entry.EntranceDelay,
		ExitDelay:         entry.ExitDelay,
		Concurrency:       entry.Concurrency,
		ToleratedFailures: entry.ToleratedFailures,
		State: &workflow.State{
			Status: entry.StateStatus,
			Start:  entry.StateStart,
			End:    entry.StateEnd,
		},
	}
	b.SetPlanID(entry.PlanID)

	return b, nil
}

// checksToEntry converts a workflow.Checks to a checksEntry.
func checksToEntry(c *workflow.Checks) (checksEntry, error) {
	if c == nil {
		return checksEntry{}, fmt.Errorf("checks cannot be nil")
	}
	if c.ID == uuid.Nil {
		return checksEntry{}, fmt.Errorf("checks must have an ID")
	}

	entry := checksEntry{
		Type:        workflow.OTCheck,
		ID:          c.ID,
		Key:         c.Key,
		PlanID:      c.GetPlanID(),
		Delay:       c.Delay,
		StateStatus: workflow.NotStarted,
	}

	if c.State != nil {
		entry.StateStatus = c.State.Status
		entry.StateStart = c.State.Start
		entry.StateEnd = c.State.End
	}

	entry.Actions = make([]uuid.UUID, len(c.Actions))
	for i, a := range c.Actions {
		entry.Actions[i] = a.ID
	}

	return entry, nil
}

// entryToChecks converts a checksEntry back to a workflow.Checks.
func entryToChecks(entry checksEntry) (*workflow.Checks, error) {
	c := &workflow.Checks{
		ID:    entry.ID,
		Key:   entry.Key,
		Delay: entry.Delay,
		State: &workflow.State{
			Status: entry.StateStatus,
			Start:  entry.StateStart,
			End:    entry.StateEnd,
		},
	}
	c.SetPlanID(entry.PlanID)

	return c, nil
}

// sequenceToEntry converts a workflow.Sequence to a sequencesEntry.
func sequenceToEntry(s *workflow.Sequence, pos int) (sequencesEntry, error) {
	if s == nil {
		return sequencesEntry{}, fmt.Errorf("sequence cannot be nil")
	}
	if s.ID == uuid.Nil {
		return sequencesEntry{}, fmt.Errorf("sequence must have an ID")
	}

	entry := sequencesEntry{
		Type:        workflow.OTSequence,
		ID:          s.ID,
		Key:         s.Key,
		PlanID:      s.GetPlanID(),
		Name:        s.Name,
		Descr:       s.Descr,
		Pos:         pos,
		StateStatus: workflow.NotStarted,
	}

	if s.State != nil {
		entry.StateStatus = s.State.Status
		entry.StateStart = s.State.Start
		entry.StateEnd = s.State.End
	}

	entry.Actions = make([]uuid.UUID, len(s.Actions))
	for i, a := range s.Actions {
		entry.Actions[i] = a.ID
	}

	return entry, nil
}

// entryToSequence converts a sequencesEntry back to a workflow.Sequence.
func entryToSequence(entry sequencesEntry) (*workflow.Sequence, error) {
	s := &workflow.Sequence{
		ID:    entry.ID,
		Key:   entry.Key,
		Name:  entry.Name,
		Descr: entry.Descr,
		State: &workflow.State{
			Status: entry.StateStatus,
			Start:  entry.StateStart,
			End:    entry.StateEnd,
		},
	}
	s.SetPlanID(entry.PlanID)

	return s, nil
}

// actionToEntry converts a workflow.Action to an actionsEntry.
func actionToEntry(a *workflow.Action, pos int) (actionsEntry, error) {
	if a == nil {
		return actionsEntry{}, fmt.Errorf("action cannot be nil")
	}
	if a.ID == uuid.Nil {
		return actionsEntry{}, fmt.Errorf("action must have an ID")
	}

	entry := actionsEntry{
		Type:        workflow.OTAction,
		ID:          a.ID,
		Key:         a.Key,
		PlanID:      a.GetPlanID(),
		Name:        a.Name,
		Descr:       a.Descr,
		Pos:         pos,
		Plugin:      a.Plugin,
		Timeout:     a.Timeout,
		Retries:     a.Retries,
		StateStatus: workflow.NotStarted,
	}

	if a.State != nil {
		entry.StateStatus = a.State.Status
		entry.StateStart = a.State.Start
		entry.StateEnd = a.State.End
	}

	// Marshal Req and Attempts to JSON bytes
	var err error
	if a.Req != nil {
		entry.Req, err = json.Marshal(a.Req)
		if err != nil {
			return actionsEntry{}, fmt.Errorf("failed to marshal action request: %w", err)
		}
	}
	if len(a.Attempts) > 0 {
		entry.Attempts, err = json.Marshal(a.Attempts)
		if err != nil {
			return actionsEntry{}, fmt.Errorf("failed to marshal action attempts: %w", err)
		}
	}

	return entry, nil
}

// entryToAction converts an actionsEntry back to a workflow.Action.
func entryToAction(entry actionsEntry) (*workflow.Action, error) {
	a := &workflow.Action{
		ID:      entry.ID,
		Key:     entry.Key,
		Name:    entry.Name,
		Descr:   entry.Descr,
		Plugin:  entry.Plugin,
		Timeout: entry.Timeout,
		Retries: entry.Retries,
		State: &workflow.State{
			Status: entry.StateStatus,
			Start:  entry.StateStart,
			End:    entry.StateEnd,
		},
	}
	a.SetPlanID(entry.PlanID)

	// Unmarshal Req and Attempts from JSON bytes
	// Note: Req type will be set during reconstruction based on plugin
	if len(entry.Attempts) > 0 {
		var attempts []*workflow.Attempt
		if err := json.Unmarshal(entry.Attempts, &attempts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal action attempts: %w", err)
		}
		a.Attempts = attempts
	}

	return a, nil
}
