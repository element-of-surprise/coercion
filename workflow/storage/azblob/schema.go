package azblob

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unique"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
)

const (
	mdKeyPlanID     = "planid"
	mdKeyGroupID    = "groupid"
	mdKeyName       = "name"
	mdKeyDescr      = "descr"
	mdKeySubmitTime = "submittime"
	mdKeyState      = "state"
	mdPlanType      = "plantype"
)

const (
	// ptEntry means the file contains a planEntry structure.
	ptEntry = "entry"
	// ptBlocks means the file contains the actual plan object.
	ptObject = "object"
)

// planMeta is a wrapper around ListResult that includes the plan type.
type planMeta struct {
	storage.ListResult
	PlanType string
}

// mapToPlanMeta converts a metadata map to a planMeta structure. The keys in the map can have any case and that case
// can change between each pull of the metadata from blob storage. I'm not sure who to blame here, the SDK or the service,
// but this is completely nutso (they could have forced lower case keys in their own map type so everything would be case
// insensitive on match, or made this a list instead of a map. Nope, so we have to loop over all keys and do case insensitive
// matches ourselves doing another loop over the values. Ugh.
func mapToPlanMeta(m map[string]*string) (planMeta, error) {
	pm := planMeta{}
	lr := storage.ListResult{}
	for k, v := range m {
		if v == nil {
			continue
		}
		switch strings.ToLower(k) {
		case mdKeyPlanID:
			id, err := uuid.Parse(*v)
			if err != nil {
				return planMeta{}, fmt.Errorf("invalid plan ID in metadata: %w", err)
			}
			lr.ID = id
		case mdKeyGroupID:
			id, err := uuid.Parse(*v)
			if err != nil {
				return planMeta{}, fmt.Errorf("invalid group ID in metadata: %w", err)
			}
			lr.GroupID = id
		case mdKeyName:
			lr.Name = *v
		case mdKeyDescr:
			lr.Descr = *v
		case mdKeySubmitTime:
			t, err := time.Parse(time.RFC3339, *v)
			if err != nil {
				return planMeta{}, fmt.Errorf("invalid submit time in metadata: %w", err)
			}
			lr.SubmitTime = t
		case mdKeyState:
			var state workflow.State
			if err := json.Unmarshal([]byte(*v), &state); err != nil {
				return planMeta{}, fmt.Errorf("invalid state in metadata: %w", err)
			}
			lr.State = state
		case mdPlanType:
			pm.PlanType = *v
		}
	}
	pm.ListResult = lr
	return pm, nil
}

// planToMetadata converts a workflow.Plan to a metadata map for blob storage.
func planToMetadata(ctx context.Context, p *workflow.Plan) (map[string]*string, error) {
	stateJSON, err := json.Marshal(p.State.Get())
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal plan state: %w", err))
	}

	md := map[string]*string{
		mdKeyPlanID:     toPtr(p.ID.String()),
		mdKeyName:       toPtr(p.Name),
		mdKeyDescr:      toPtr(p.Descr),
		mdKeySubmitTime: toPtr(p.SubmitTime.Format(time.RFC3339Nano)),
		mdKeyState:      toPtr(bytesToStr(stateJSON)),
	}

	if p.GroupID != uuid.Nil {
		md[mdKeyGroupID] = toPtr(p.GroupID.String())
	}
	return md, nil
}

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
	EntranceDelay     time.Duration       `json:"entranceDelay,omitempty,format:iso8601"`
	ExitDelay         time.Duration       `json:"exitDelay,omitempty,format:iso8601"`
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
	Delay       time.Duration       `json:"delay,omitempty,format:iso8601"`
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
	Timeout     time.Duration       `json:"timeout,format:iso8601"`
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

	if state := p.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
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

	if state := b.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
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
	}
	b.State.Set(workflow.State{
		Status: entry.StateStatus,
		Start:  entry.StateStart,
		End:    entry.StateEnd,
	})
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

	if state := c.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
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
	}
	c.State.Set(workflow.State{
		Status: entry.StateStatus,
		Start:  entry.StateStart,
		End:    entry.StateEnd,
	})
	c.SetPlanID(entry.PlanID)

	return c, nil
}

var stateZero = unique.Make(workflow.State{})

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

	if state := s.State.Get(); unique.Make(state) != stateZero {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
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
	}
	s.State.Set(workflow.State{
		Status: entry.StateStatus,
		Start:  entry.StateStart,
		End:    entry.StateEnd,
	})
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

	if state := a.State.Get(); state != (workflow.State{}) {
		entry.StateStatus = state.Status
		entry.StateStart = state.Start
		entry.StateEnd = state.End
	}

	// Marshal Req and Attempts to JSON bytes
	var err error
	if a.Req != nil {
		entry.Req, err = json.Marshal(a.Req)
		if err != nil {
			return actionsEntry{}, fmt.Errorf("failed to marshal action request: %w", err)
		}
	}

	if len(a.Attempts.Get()) > 0 {
		attempts, err := json.Marshal(a.Attempts.Get())
		if err != nil {
			return actionsEntry{}, fmt.Errorf("can't encode action.Attempts: %w", err)
		}
		entry.Attempts = attempts
	}

	return entry, nil
}

// entryToAction converts an actionsEntry back to a workflow.Action.
func entryToAction(ctx context.Context, reg *registry.Register, response []byte) (*workflow.Action, error) {
	var err error
	var resp actionsEntry
	if err = json.Unmarshal(response, &resp); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal action: %w", err))
	}

	a := &workflow.Action{
		ID:      resp.ID,
		Key:     resp.Key,
		Name:    resp.Name,
		Descr:   resp.Descr,
		Plugin:  resp.Plugin,
		Timeout: resp.Timeout,
		Retries: resp.Retries,
	}
	a.State.Set(workflow.State{
		Status: resp.StateStatus,
		Start:  resp.StateStart,
		End:    resp.StateEnd,
	})
	a.SetPlanID(resp.PlanID)

	plug := reg.Plugin(a.Plugin)
	if plug == nil {
		return nil, fmt.Errorf("couldn't find plugin %s", a.Plugin)
	}
	b := resp.Req
	if len(b) > 0 {
		req := plug.Request()
		if req != nil {
			if reflect.TypeOf(req).Kind() != reflect.Pointer {
				if err := json.Unmarshal(b, &req); err != nil {
					return nil, fmt.Errorf("couldn't unmarshal request: %w", err)
				}
			} else {
				if err := json.Unmarshal(b, req); err != nil {
					return nil, fmt.Errorf("couldn't unmarshal request: %w", err)
				}
			}
			a.Req = req
		}
	}

	b = resp.Attempts
	if len(b) > 0 {
		attempts, err := decodeAttempts(ctx, b, plug)
		if err != nil {
			return nil, fmt.Errorf("couldn't decode attempts: %w", err)
		}
		a.Attempts.Set(attempts)
	}

	return a, nil
}

// decodeAttempts decodes a JSON array of JSON encoded attempts as byte slices into a slice of attempts.
func decodeAttempts(ctx context.Context, rawAttempts []byte, plug plugins.Plugin) ([]workflow.Attempt, error) {
	if len(rawAttempts) == 0 {
		return nil, nil
	}

	var attempts = []workflow.Attempt{}

	dec := jsontext.NewDecoder(bytes.NewReader(rawAttempts))
	if dec.PeekKind() != jsontext.BeginArray.Kind() {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("invalid attempts format"))
	}
	dec.ReadToken()

	for {
		k := dec.PeekKind()
		if k == jsontext.EndArray.Kind() {
			break
		}

		var a = workflow.Attempt{Resp: plug.Response()}
		if err := json.UnmarshalDecode(dec, &a); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to unmarshal attempt: %w", err))
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}
