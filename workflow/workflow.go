// Package workflow provides a workflow plan that can be executed.
package workflow

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/element-of-surprise/workstream/plugins"
	"github.com/element-of-surprise/workstream/plugins/registry"

	"github.com/google/uuid"
)

//go:generate stringer -type=Status

// Status represents the status of a various workflow objects. Not all
// objects will have all statuses.
type Status int

const (
	// NotStarted represents an object that has not started execution.
	NotStarted Status = 0 // NotStarted
	// Running represents an object that is currently running.
	Running Status = 100 // Running
	// Completed represents an object that has completed successfully. For a Plan,
	// this indicates a successful execution, but does not mean that the workflow did not have errors.
	Completed Status = 200 // Completed
	// Failed represents an object that has failed.
	Failed Status = 300 // Failed
	// Stopped represents an object that has been stopped by a user action.
	Stopped Status = 400 // Stopped
)

//go:generate stringer -type=FailureReason

// FailureReason represents the reason that a workflow failed.
type FailureReason int

const (
	// FRUnknown represents a failure reason that is unknown.
	// This is the case when a workflow is not in a completed state (a state above 500)
	// or the state is WFCompleted.
	FRUnknown FailureReason = 0 // Unknown
	// FRPreCheck represents a failure reason that occurred during pre-checks.
	FRPreCheck FailureReason = 100 // PreCheck
	// FRBlock represents a failure reason that occurred during a block.
	FRBlock FailureReason = 200 // Block
	// FRPostCheck represents a failure reason that occurred during post-checks.
	FRPostCheck FailureReason = 300 // PostCheck
	// FRContCheck represents a failure reason that occurred during a continuous check.
	FRContCheck FailureReason = 400 // ContCheck
	// FRStopped represents a failure reason that occurred because the workflow was stopped.
	FRStopped FailureReason = 500 // Stopped
)

// State represents the internal state of a workflow object.
type State struct {
	// Status is the status of the object.
	Status Status
	// Start is the time that the object was started.
	Start time.Time
	// End is the time that the object was completed.
	End time.Time
}

// Reset resets the running state of the object. Not for use by users.
func (s *State) Reset() {
	s.Status = NotStarted
	s.Start = time.Time{}
	s.End = time.Time{}
}

// Clone returns a copy of the state. This is not used by any object Clone method,
// but can be used in testing.
func (s *State) Clone() *State {
	return &State{
		Status: s.Status,
		Start:  s.Start,
		End:    s.End,
	}
}

// validator is a type that validates its own fields. If the validator has sub-types that
// need validation, it returns a list of validators that need to be validated.
// This allows tests to be more modular instead of a super test of the entire object tree.
type validator interface {
	validate() ([]validator, error)
}

// ObjectType is the type of object.
type ObjectType int

const (
	// OTUnknown represents an unknown object type. This is
	// an indication of a bug.
	OTUnknown ObjectType = 0
	// OTPlan represents a workflow plan.
	OTPlan ObjectType = 1
	// OTCheck represents a check object.
	OTCheck ObjectType = 2
	// OTBlock represents a Block.
	OTBlock ObjectType = 5
	// OTSequence represents a Sequence.
	OTSequence ObjectType = 6
	// OTAction represents an Action.
	OTAction ObjectType = 7
)

// Object is an interface that all workflow objects must implement.
type Object interface {
	// Type returns the type of the object.
	Type() ObjectType
	object()
}

// Plan represents a workflow plan that can be executed. This is the main struct that is
// used to define the workflow.
type Plan struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Name is the name of the workflow. Required.
	Name string
	// Descr is a human-readable description of the workflow. Required.
	Descr string
	// A GroupID is a unique identifier for a group of workflows. This is used to group
	// workflows together for informational purposes. This is not required.
	GroupID uuid.UUID
	// Meta is any type of metadata that the user wants to store with the workflow.
	// This is not used by the workflow engine. Optional.
	Meta []byte

	// PreChecks are actions that are executed before the workflow starts.
	// Any error will cause the workflow to fail. Optional.
	PreChecks *Checks
	// ContChecks are actions that are executed while the workflow is running.
	// Any error will cause the workflow to fail. Optional.
	ContChecks *Checks
	// Checks are actions that are executed after the workflow has completed.
	// Any error will cause the workflow to fail. Optional.
	PostChecks *Checks

	// Blocks is a list of blocks that are executed in sequence.
	// If a block fails, the workflow will fail.
	// Only one block can be executed at a time. Required.
	Blocks []*Block

	// State is the internal state of the object. Should not be set by the user.
	State *State
	// SubmitTime is the time that the object was submitted. This is only
	// set for the Plan object
	SubmitTime time.Time
	// Reason is the reason that the object failed.
	// This will be set to FRUnknown if not in a failed state.
	Reason FailureReason
}

// GetID returns the ID of the object.
func (p *Plan) GetID() uuid.UUID {
	return p.ID
}

// SetID sets the ID of the object.
func (p *Plan) SetID(id uuid.UUID) {
	p.ID = id
}

// GetStates is a getter for the State settings.
// This violates Go naming for getters, but this is because we expose State on most objects by the
// State name (unlike most getter/setters). This is here to enable an interface for getting State on
// all objects.
func (p *Plan) GetState() *State {
	return p.State
}

// SetState is a setter for the State settings.
func (p *Plan) SetState(state *State) {
	p.State = state
}

// Type implements the Object.Type().
func (p *Plan) Type() ObjectType {
	return OTPlan
}

// object implements the Object interface.
func (p *Plan) object() {}

func (p *Plan) defaults() {
	if p == nil {
		return
	}
	p.ID = uuid.New()
	p.State = &State{
		Status: NotStarted,
	}
}

func (p *Plan) validate() ([]validator, error) {
	if p == nil {
		return nil, errors.New("plan is nil")
	}
	if p.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}

	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(p.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}
	if len(p.Blocks) == 0 {
		return nil, fmt.Errorf("at least one block is required")
	}
	if p.Reason != FRUnknown {
		return nil, fmt.Errorf("reason should not be set by the user")
	}
	if !p.SubmitTime.IsZero() {
		return nil, fmt.Errorf("submit time should not be set by the user")
	}

	vals := []validator{p.PreChecks, p.ContChecks, p.PostChecks}
	for _, b := range p.Blocks {
		vals = append(vals, b)
	}

	return vals, nil
}

// Checks represents a set of actions that are executed before the workflow starts.
type Checks struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Delay is the amount of time to wait before executing the checks. This
	// is only used by continuous checks. Optional. Defaults to 30 seconds.
	Delay time.Duration
	// Actions is a list of actions that are executed in parallel. Any error will
	// cause the workflow to fail. Required.
	Actions []*Action

	// State represents the internal state of the object. Should not be set by the user.
	State *State
}

// GetID is a getter for the ID field.
func (c *Checks) GetID() uuid.UUID {
	if c == nil {
		return uuid.Nil
	}
	return c.ID
}

// SetID is a setter for the ID field.
// This should not be used by the user.
func (c *Checks) SetID(id uuid.UUID) {
	c.ID = id
}

// GetState is a getter for the State settings.
func (c *Checks) GetState() *State {
	return c.State
}

// SetState is a setter for the State settings.
func (c *Checks) SetState(state *State) {
	c.State = state
}

// Type implements the Object.Type().
func (c *Checks) Type() ObjectType {
	return OTCheck
}

// object implements the Object interface.
func (c *Checks) object() {}

func (c *Checks) defaults() {
	if c == nil {
		return
	}
	c.ID = uuid.New()
	c.State = &State{
		Status: NotStarted,
	}
}

// Clone clones the Checks object. This does not copy the ID or the State object.
// All actions are cloned according to their Clone() method.
func (c *Checks) Clone() *Checks {
	if c == nil {
		return nil
	}

	clone := &Checks{
		Delay:   c.Delay,
		Actions: make([]*Action, len(c.Actions)),
	}

	for i := 0; i < len(c.Actions); i++ {
		clone.Actions[i] = c.Actions[i].Clone()
	}

	return clone
}

func (c *Checks) validate() ([]validator, error) {
	if c == nil {
		return nil, nil
	}
	if c.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if len(c.Actions) == 0 {
		return nil, fmt.Errorf("at least one action is required")
	}
	if c.State != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	vals := make([]validator, len(c.Actions))
	for i := 0; i < len(c.Actions); i++ {
		vals[i] = c.Actions[i]
	}

	return vals, nil
}

// Block represents a set of replated work. It contains a list of sequences that are executed with
// a configurable amount of concurrency. If a block fails, the workflow will fail. Only one block
// can be executed at a time.
type Block struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Name is the name of the block. Required.
	Name string
	// Descr is a description of the block. Required.
	Descr string

	// EntranceDelay is the amount of time to wait before the block starts. This defaults to 0.
	EntranceDelay time.Duration
	// ExitDelay is the amount of time to wait after the block has completed. This defaults to 0.
	ExitDelay time.Duration

	// PreChecks are actions that are executed before the block starts.
	// Any error will cause the block to fail. Optional.
	PreChecks *Checks
	// Checks are actions that are executed while the block is running. Optional.
	ContChecks *Checks
	// PostChecks are actions that are executed after the block has completed.
	// Any error will cause the block to fail. Optional.
	PostChecks *Checks

	// Sequences is a list of sequences that are executed. Required..
	Sequences []*Sequence

	// Concurrency is the number of sequences that are executed in parallel. This defaults to 1.
	Concurrency int
	// ToleratedFailures is the number of sequences that are allowed to fail before the block fails. This defaults to 0.
	// If set to -1, all sequences are allowed to fail.
	ToleratedFailures int

	// State represents settings that should not be set by the user, but users can query.
	State *State
}

// GetID is a getter for the ID field.
func (b *Block) GetID() uuid.UUID {
	if b == nil {
		return uuid.Nil
	}
	return b.ID
}

// SetID is a setter for the ID field.
// This should not be used by the user.
func (b *Block) SetID(id uuid.UUID) {
	b.ID = id
}

// GetState is a getter for the State settings.
func (b *Block) GetState() *State {
	return b.State
}

// SetState is a setter for the State settings.
func (b *Block) SetState(state *State) {
	b.State = state
}

// Type implements the Object.Type().
func (b *Block) Type() ObjectType {
	return OTBlock
}

// object implements the Object interface.
func (b *Block) object() {}

func (b *Block) defaults() *Block {
	if b == nil {
		return nil
	}
	b.ID = uuid.New()
	if b.Concurrency < 1 {
		b.Concurrency = 1
	}
	b.State = &State{
		Status: NotStarted,
	}
	return b
}

func (b *Block) validate() ([]validator, error) {
	if b == nil {
		return nil, fmt.Errorf("cannot have a nil Block")
	}
	if b.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}

	if strings.TrimSpace(b.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}

	if strings.TrimSpace(b.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if b.Concurrency < 1 {
		return nil, fmt.Errorf("concurrency must be at least 1")
	}

	if b.State != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if len(b.Sequences) == 0 {
		return nil, fmt.Errorf("at least one sequence is required")
	}

	vals := []validator{b.PreChecks, b.ContChecks, b.PostChecks}
	for _, seq := range b.Sequences {
		vals = append(vals, seq)
	}
	return vals, nil
}

// Clone makes a copy of the block, but without an ID or State. All checks and sequences are cloned acccording
// to their Clone() method.
func (b *Block) Clone() *Block {
	n := &Block{
		Name: 		   b.Name,
		Descr: 		   b.Descr,
		EntranceDelay: b.EntranceDelay,
		ExitDelay:     b.ExitDelay,
		Concurrency:   b.Concurrency,
		ToleratedFailures: b.ToleratedFailures,
	}

	if b.PreChecks != nil {
		n.PreChecks = b.PreChecks.Clone()
	}
	if b.ContChecks != nil {
		n.ContChecks = b.ContChecks.Clone()
	}
	if b.PostChecks != nil {
		n.PostChecks = b.PostChecks.Clone()
	}

	n.Sequences = make([]*Sequence, len(b.Sequences))
	for i, seq := range b.Sequences {
		n.Sequences[i] = seq.Clone()
	}

	return n
}

// Sequence represents a set of Actions that are executed in sequence. Any error will cause the workflow to fail.
type Sequence struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Name is the name of the sequence. Required.
	Name string
	// Descr is a description of the sequence. Required.
	Descr string
	// Actions is a list of actions that are executed in sequence. Any error will cause the workflow to fail. Required.
	Actions []*Action

	// State represents settings that should not be set by the user, but users can query.
	State *State
}

// GetID is a getter for the ID field.
func (s *Sequence) GetID() uuid.UUID {
	if s == nil {
		return uuid.Nil
	}
	return s.ID
}

// SetID is a setter for the ID field.
func (s *Sequence) SetID(id uuid.UUID) {
	s.ID = id
}

// GetState is a getter for the State settings.
func (s *Sequence) GetState() *State {
	return s.State
}

// SetState is a setter for the State settings.
func (s *Sequence) SetState(state *State) {
	s.State = state
}

// Type implements the Object.Type().
func (s *Sequence) Type() ObjectType {
	return OTSequence
}

// object implements the Object interface.
func (s *Sequence) object() {}

func (s *Sequence) defaults() {
	if s == nil {
		return
	}
	s.ID = uuid.New()
	s.State = &State{
		Status: NotStarted,
	}
}

func (s *Sequence) validate() ([]validator, error) {
	if s == nil {
		return nil, fmt.Errorf("cannot have a nil Sequence")
	}
	if s.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}

	if strings.TrimSpace(s.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(s.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if s.State != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if len(s.Actions) == 0 {
		return nil, fmt.Errorf("at least one Action is required")
	}

	vals := make([]validator, 0, len(s.Actions))
	for _, a := range s.Actions {
		vals = append(vals, a)
	}
	return vals, nil
}

// Clone clones an Sequence with no ID and no State. Also clones all the Actions
// following the cloning rules of the Action.Clone() method.
func (s *Sequence) Clone() *Sequence {
	if s == nil {
		return nil
	}
	ns := &Sequence{
		Name:   s.Name,
		Descr:  s.Descr,
		Actions: make([]*Action, len(s.Actions)),
	}
	for i, a := range s.Actions {
		ns.Actions[i] = a.Clone()
	}
	return ns
}

// Attempt is the result of an action that is executed by a plugin.
// Nothing in Attempt should be set by the user.
type Attempt struct {
	// Resp is the response object that is returned by the plugin.
	Resp any
	// Err is the plugin error that is returned by the plugin. If this is not nil, the attempt failed.
	Err *plugins.Error

	// Start is the time the attempt started.
	Start time.Time
	// End is the time the attempt ended.
	End   time.Time
}

// Action represents a single action that is executed by a plugin.
type Action struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Name is the name of the Action. Required.
	Name string
	// Descr is a description of the Action. Required.
	Descr string
	// Plugin is the name of the plugin that is executed. Required.
	Plugin string
	// Timeout is the amount of time to wait for the Action to complete. This defaults to 30 seconds and
	// must be at least 5 seconds.
	Timeout time.Duration
	// Retries is the number of times to retry the Action if it fails. This defaults to 0.
	Retries int
	// Req is the request object that is passed to the plugin.
	Req any

	// Attempts is the attempts of the action. This should not be set by the user.
	Attempts []*Attempt
	// State represents settings that should not be set by the user, but users can query.
	State *State

	register *registry.Register
}

// GetID is a getter for the ID field.
func (a *Action) GetID() uuid.UUID {
	if a == nil {
		return uuid.Nil
	}
	return a.ID
}

// SetID is a setter for the ID field.
// This should not be used by the user.
func (a *Action) SetID(id uuid.UUID) {
	a.ID = id
}

// GetState is a getter for the State settings.
func (a *Action) GetState() *State {
	return a.State
}

// SetState is a setter for the State settings.
func (a *Action) SetState(state *State) {
	a.State = state
}

// Clone clones an Action with no ID, no Attempts and no State.
func (a *Action) Clone() *Action {
	if a == nil {
		return nil
	}
	na := &Action{
		Name:   a.Name,
		Descr:  a.Descr,
		Plugin: a.Plugin,
		Timeout: a.Timeout,
		Retries: a.Retries,
		Req:     a.Req,
	}
	return na
}

// Type implements the Object.Type().
func (a *Action) Type() ObjectType {
	return OTAction
}

// object implements the Object interface.
func (a *Action) object() {}

func (a *Action) defaults() {
	if a == nil {
		return
	}
	a.ID = uuid.New()
	a.State = &State{
		Status: NotStarted,
	}
}

func (a *Action) validate() ([]validator, error) {
	if a == nil {
		return nil, fmt.Errorf("cannot have a nil Action")
	}
	if a.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}

	if a.State != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if a.Timeout == 0 {
		a.Timeout = 30 * time.Second
	}
	if a.Timeout < 5*time.Second {
		return nil, fmt.Errorf("timeout must be at least 5 seconds")
	}

	if strings.TrimSpace(a.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(a.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if strings.TrimSpace(a.Plugin) == "" {
		return nil, fmt.Errorf("plugin is required")
	}
	if a.Attempts != nil {
		return nil, fmt.Errorf("attempts should not be set by the user")
	}

	if a.Retries < 0 {
		a.Retries = 0
	}

	var plug  plugins.Plugin
	if a.register == nil {
		plug = registry.Plugins.Plugin(a.Plugin)
	} else {
		plug = a.register.Plugin(a.Plugin)
	}
	if plug == nil {
		return nil, fmt.Errorf("plugin %q not found", a.Plugin)
	}

	if err := plug.ValidateReq(a.Req); err != nil {
		return nil, fmt.Errorf("plugin %q: %w", a.Plugin, err)
	}

	return nil, nil
}

type queue[T any] struct {
	items []T
	mu   sync.Mutex
}

func (q *queue[T]) push(items ...T) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.items = append(q.items, items...)
}

func (q *queue[T]) pop() T {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		var zero T
		return zero
	}

	item := q.items[0]
	q.items = q.items[1:]
	return item
}

// Validate validates the Plan. This is automatically called by workstream.Submit.
func Validate(p *Plan) error {
	if p == nil {
		return fmt.Errorf("cannot have a nil Plan")
	}

	q := &queue[validator]{}
	q.push(p)

	for val := q.pop(); val != nil; val = q.pop() {
		vals, err := val.validate()
		if err != nil {
			return err
		}
		if len(vals) != 0 {
			q.push(vals...)
		}
	}
	return nil
}
