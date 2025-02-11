// Package workflow provides a workflow plan that can be executed.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"

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
	// FRDeferredCheck represents a failure reason that occurred during a deferred check.
	FRDeferredCheck FailureReason = 450 // DeferredCheck
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
	// Etag is a field that may be used internally by storage implementations for concurrency control.
	ETag string
}

// Reset resets the running state of the object. Not for use by users.
func (s *State) Reset() {
	s.Status = NotStarted
	s.Start = time.Time{}
	s.End = time.Time{}
	s.ETag = ""
}

// Duration returns the time between Start and End for this object.
func (s *State) Duration() time.Duration {
	return s.End.Sub(s.Start)
}

// validator is a type that validates its own fields. If the validator has sub-types that
// need validation, it returns a list of validators that need to be validated.
// This allows tests to be more modular instead of a super test of the entire object tree.
type validator interface {
	validate(ctx context.Context) ([]validator, error)
}

//go:generate stringer -type=ObjectType

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
	// GroupID is a unique identifier for a group of workflows. This is used to group
	// workflows together for informational purposes. This is not required.
	GroupID uuid.UUID
	// Meta is any type of metadata that the user wants to store with the workflow.
	// This is not used by the workflow engine. Optional.
	Meta []byte
	// BypassChecks are actions that if they succeed will cause the workflow to be skipped.
	// If any gate fails, the workflow will be executed. Optional.
	BypassChecks *Checks
	// PreChecks are actions that are executed before the workflow starts.
	// Any error will cause the workflow to fail. Optional.
	PreChecks *Checks
	// ContChecks are actions that are executed while the workflow is running.
	// Any error will cause the workflow to fail. Optional.
	ContChecks *Checks
	// Checks are actions that are executed after the workflow has completed.
	// Any error will cause the workflow to fail. Optional.
	PostChecks *Checks
	// DeferredChecks are actions that are executed after the workflow has completed.
	// This is executed regardless of Plan success or failure. However, if the
	// Plan is bypassed via BypassChecks, this will not run.
	// Useful for logging and similar operations. Optional.
	DeferredChecks *Checks

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

// Defaults sets the default values for the object. For use internally.
func (p *Plan) Defaults() {
	if p == nil {
		return
	}
	p.ID = NewV7()
	p.State = &State{
		Status: NotStarted,
	}
}

func (p *Plan) validate(ctx context.Context) ([]validator, error) {
	if p == nil {
		return nil, errors.New("plan is nil")
	}
	if p.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if p.State != nil {
		return nil, fmt.Errorf("state should not be set by the user")
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

	vals := []validator{p.BypassChecks, p.PreChecks, p.ContChecks, p.PostChecks, p.DeferredChecks}
	for _, b := range p.Blocks {
		vals = append(vals, b)
	}

	return vals, nil
}

// Checks represents a set of actions that are executed before the workflow starts.
type Checks struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Key is a unique identifier within a Plan that the user supplies and can use to reference
	// the checks. Optional.
	Key uuid.UUID
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

// Defaults sets the default values for the object. For use internally.
func (c *Checks) Defaults() {
	if c == nil {
		return
	}
	c.ID = NewV7()
	c.State = &State{
		Status: NotStarted,
	}
}

func (c *Checks) validate(ctx context.Context) ([]validator, error) {
	if c == nil {
		return nil, nil
	}
	if c.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if err := addOrErrKey(ctx, c.Key); err != nil {
		return nil, fmt.Errorf("Checks object: %w", err)
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
	// Key is a unique identifier within a Plan that the user supplies and can use to reference
	// the block. Optional.
	Key uuid.UUID
	// Name is the name of the block. Required.
	Name string
	// Descr is a description of the block. Required.
	Descr string

	// EntranceDelay is the amount of time to wait before the block starts. This defaults to 0.
	EntranceDelay time.Duration
	// ExitDelay is the amount of time to wait after the block has completed. This defaults to 0.
	ExitDelay time.Duration

	// BypassChecks are actions that if they succeed will cause the block to be skipped.
	// If any gate fails, the workflow will be executed. Optional.
	BypassChecks *Checks
	// PreChecks are actions that are executed before the block starts.
	// Any error will cause the block to fail. Optional.
	PreChecks *Checks
	// Checks are actions that are executed while the block is running. Optional.
	ContChecks *Checks
	// PostChecks are actions that are executed after the block has completed.
	// Any error will cause the block to fail. Optional.
	PostChecks *Checks
	// DeferredChecks are actions that are executed after the workflow has completed.
	// This is executed regardless of Plan success or failure. However, if the
	// Block is bypassed via BypassChecks, this will not run.
	// Useful for logging and similar operations. Optional.
	DeferredChecks *Checks

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

// Defaults sets the default values for the object. For use internally.
func (b *Block) Defaults() {
	if b == nil {
		return
	}
	b.ID = NewV7()
	if b.Concurrency < 1 {
		b.Concurrency = 1
	}
	b.State = &State{
		Status: NotStarted,
	}
	return
}

func (b *Block) validate(ctx context.Context) ([]validator, error) {
	if b == nil {
		return nil, fmt.Errorf("cannot have a nil Block")
	}
	if b.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if err := addOrErrKey(ctx, b.Key); err != nil {
		return nil, fmt.Errorf("Block object(%s): %w", b.Name, err)
	}
	if strings.TrimSpace(b.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}

	if strings.TrimSpace(b.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if b.State != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if len(b.Sequences) == 0 {
		return nil, fmt.Errorf("at least one sequence is required")
	}

	vals := []validator{b.BypassChecks, b.PreChecks, b.ContChecks, b.PostChecks, b.DeferredChecks}
	for _, seq := range b.Sequences {
		vals = append(vals, seq)
	}
	return vals, nil
}

// Sequence represents a set of Actions that are executed in sequence. Any error will cause the workflow to fail.
type Sequence struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Key is a unique identifier within a Plan that the user supplies and can use to reference
	// the sequence. Optional.
	Key uuid.UUID
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

// Defaults sets the default values for the object. For use internally.
func (s *Sequence) Defaults() {
	if s == nil {
		return
	}
	s.ID = NewV7()
	s.State = &State{
		Status: NotStarted,
	}
}

func (s *Sequence) validate(ctx context.Context) ([]validator, error) {
	if s == nil {
		return nil, fmt.Errorf("cannot have a nil Sequence")
	}
	if s.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if err := addOrErrKey(ctx, s.Key); err != nil {
		return nil, fmt.Errorf("Sequence object(%s): %w", s.Name, err)
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
	End time.Time
}

// Action represents a single action that is executed by a plugin.
type Action struct {
	// ID is a unique identifier for the object. Should not be set by the user.
	ID uuid.UUID
	// Key is a unique identifier within a Plan that the user supplies and can use to reference
	// the action. Optional.
	Key uuid.UUID
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

	register *registry.Register `json:"-"`
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

// Type implements the Object.Type().
func (a *Action) Type() ObjectType {
	return OTAction
}

// object implements the Object interface.
func (a *Action) object() {}

// Defaults sets the default values for the object. For use internally.
func (a *Action) Defaults() {
	if a == nil {
		return
	}
	a.ID = NewV7()
	a.State = &State{
		Status: NotStarted,
	}
}

// HasRegister determines if a Register has been set.
func (a *Action) HasRegister() bool {
	return a.register != nil
}

// SetRegister sets the register for the Action. This should not be used by the user except in tests.
func (a *Action) SetRegister(r *registry.Register) {
	a.register = r
}

func (a *Action) validate(ctx context.Context) ([]validator, error) {
	if a == nil {
		return nil, fmt.Errorf("cannot have a nil Action")
	}
	if a.ID != uuid.Nil {
		return nil, fmt.Errorf("id should not be set by the user")
	}
	if err := addOrErrKey(ctx, a.Key); err != nil {
		return nil, fmt.Errorf("Action object(%s): %w", a.Name, err)
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

	plug := a.register.Plugin(a.Plugin)

	if plug == nil {
		return nil, fmt.Errorf("plugin %q not found", a.Plugin)
	}

	if err := plug.ValidateReq(a.Req); err != nil {
		return nil, fmt.Errorf("plugin %q: %w", a.Plugin, err)
	}

	return nil, nil
}

// FinalAttempt returns the last attempt of the action.
func (a *Action) FinalAttempt() *Attempt {
	if len(a.Attempts) == 0 {
		return nil
	}
	if len(a.Attempts) == 0 {
		return nil
	}
	return a.Attempts[len(a.Attempts)-1]
}

type queue[T any] struct {
	items []T
	mu    sync.Mutex
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

type keysMap struct{}

func getKeyMap(ctx context.Context) map[string]bool {
	m, ok := ctx.Value(keysMap{}).(map[string]bool)
	if !ok {
		return nil
	}
	return m
}

// addOrErrKey grabs the map of object.Key elements found and checks to see if k
// is already in the map. If it is, it returns an error. If it is not, it adds it.
// If k is uuid.Nil, it does nothing. If the key is not a version 7 UUID, it returns an error.
func addOrErrKey(ctx context.Context, k uuid.UUID) error {
	const v7 = uuid.Version(byte(7))

	if k != uuid.Nil {
		if k.Version() != v7 {
			return fmt.Errorf("had a .Key value(%s) with an invalid version(got %s, want %s)", k.String(), string(k.Version()), string(v7))
		}
		s := k.String()
		m := getKeyMap(ctx)
		if m[s] {
			return fmt.Errorf("Object.Key %q already exists on another object", s)
		}
		m[s] = true
	}
	return nil
}

// Validate validates the Plan. This is automatically called by workstream.Submit.
func Validate(p *Plan) error {
	if p == nil {
		return fmt.Errorf("cannot have a nil Plan")
	}

	q := &queue[validator]{}
	q.push(p)

	ctx := context.Background()
	m := map[string]bool{}
	ctx = context.WithValue(ctx, keysMap{}, m)

	for val := q.pop(); val != nil; val = q.pop() {
		vals, err := val.validate(ctx)
		if err != nil {
			return err
		}
		if len(vals) != 0 {
			q.push(vals...)
		}
	}
	return nil
}

// Secure sets all fields in anywhere in the Plan that are tagged with `coerce:"secure"` to their zero value.
func Secure(v *Plan) {
	secure(v)
}

func secure(v any) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if !field.CanSet() {
			field = field.Addr()
		}

		tags := getTags(typ.Field(i))
		if tags.hasTag("secure") {
			field.Set(reflect.Zero(field.Type()))
			continue
		}

		// Recursively coerce nested structs
		if field.Kind() == reflect.Struct || (field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct) {
			secure(field.Addr().Interface())
		}
	}
}

// tags is a set of tags for a field.
type tags map[string]bool

func (t tags) hasTag(tag string) bool {
	if t == nil {
		return false
	}
	return t[tag]
}

// getTags returns the tags for a field.
func getTags(f reflect.StructField) tags {
	strTags := f.Tag.Get("coerce")
	if strings.TrimSpace(strTags) == "" {
		return nil
	}
	t := make(tags)
	for _, tag := range strings.Split(strTags, ",") {
		tag = strings.TrimSpace(strings.ToLower(tag))
		t[tag] = true
	}
	return t
}

// NewV7 generates a new UUID. This is a wrapper around uuid.NewV7
// that retries until a valid UUID is generated.
func NewV7() uuid.UUID {
	for {
		u, err := uuid.NewV7()
		if err == nil {
			return u
		}
	}
}
