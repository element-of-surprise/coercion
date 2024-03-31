// Package workflow provides a workflow plan that can be executed.
package workflow

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/element-of-surprise/workstream/plugins"

	"github.com/google/uuid"
)

//go:generate stringer -type=Status

// Status represents the status of a various workflow objects. Not all
// objects will have all statuses.
type Status int

const (
	// NotStarted represents an object that has not started execution.
	NotStarted Status = 0 // NotStarted
	// Started represents an object that has been started by the user, but hasn't started
	// executing yet. Only a Plan can have this status.
	Started Status = 100 // Started
	// Running represents an object that is currently running something other than checks.
	Running Status = 200 // Running
	// Completed represents an object that has completed successfully. For a Plan,
	// this indicates a successful execution, but does not mean that the workflow did not have errors.
	Completed Status = 300 // Completed
	// Failed represents an object that has failed.
	Failed Status = 400 // Failed
	// Stopped represents an object that has been stopped by a user action.
	Stopped Status = 500 // Stopped
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

// Internal represents the internal state of a workflow object.
type Internal struct {
	// ID is a unique identifier for the object.
	ID uuid.UUID
	// Status is the status of the object.
	Status Status
	// Reason is the reason that the object failed.
	// This will be set to FRUnknown if not in a failed state.
	Reason FailureReason

	// Start is the time that the object was started.
	Start time.Time
	// End is the time that the object was completed.
	End time.Time
}

// validator is a type that validates its own fields. If the validator has sub-types that
// need validation, it returns a list of validators that need to be validated.
// This allows tests to be more modular instead of a super test of the entire object tree.
type validator interface {
	validate() ([]validator, error)
}

// defaulter is a type that sets default values for its fields.
type defaulter interface {
	defaults()
}

// ObjectType is the type of object.
type ObjectType int

const (
	// OTUnknown represents an unknown object type. This is
	// an indication of a bug.
	OTUnknown ObjectType = 0
	// OTPlan represents a workflow plan.
	OTPlan ObjectType = 1
	// OTPreCheck represents a pre-check.
	OTPreCheck ObjectType = 2
	// OTPostCheck represents a post-check.
	OTPostCheck ObjectType = 3
	// OTContCheck represents a continuous check.
	OTContCheck ObjectType = 4
	// OTBlock represents a Block.
	OTBlock ObjectType = 5
	// OTSequence represents a Sequence.
	OTSequence ObjectType = 6
	// OTJob represents a Job.
	OTJob ObjectType = 7
	// OTAction represents an Action.
	OTAction ObjectType = 8
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
	// Name is the name of the workflow.
	Name string
	// Descr is a human-readable description of the workflow.
	Descr string

	// PreChecks are actions that are executed before the workflow starts.
	// Any error will cause the workflow to fail.
	PreChecks *PreChecks
	// PostChecks are actions that are executed after the workflow has completed.
	// Any error will cause the workflow to fail.
	PostChecks *PostChecks
	// ContChecks are actions that are executed while the workflow is running.
	// Any error will cause the workflow to fail.
	ContChecks *ContChecks

	// Blocks is a list of blocks that are executed in sequence.
	// If a block fails, the workflow will fail.
	// Only one block can be executed at a time.
	Blocks []*Block

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// Type implements the Object.Type().
func (p *Plan) Type() ObjectType {
	return OTPlan
}

// object implements the Object interface.
func (p *Plan) object() {}

func (p *Plan) validate() ([]validator, error) {
	if p == nil {
		return nil, errors.New("plan is nil")
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

	if p.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	vals := []validator{p.PreChecks, p.PostChecks, p.ContChecks}
	for _, b := range p.Blocks {
		vals = append(vals, b)
	}

	return vals, nil
}

// PreChecks represents a set of actions that are executed before the workflow starts.
type PreChecks struct {
	// Actions is a list of actions that are executed in parallel. Any error will
	// cause the workflow to fail.
	Actions []*Action

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// Type implements the Object.Type().
func (p *PreChecks) Type() ObjectType {
	return OTPreCheck
}

// object implements the Object interface.
func (p *PreChecks) object() {}

func (p *PreChecks) validate() ([]validator, error) {
	if p == nil {
		return nil, nil
	}
	if len(p.Actions) == 0 {
		return nil, fmt.Errorf("at least one action is required")
	}
	if p.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	vals := make([]validator, len(p.Actions))
	for i := 0; i < len(p.Actions); i++ {
		vals[i] = p.Actions[i]
	}

	return vals, nil
}

// PostChecks represents a set of actions that are executed after the workflow has completed.
type PostChecks struct {
	// Actions is a list of actions that are executed in parallel. Any error will
	// cause the workflow to fail.
	Actions []*Action

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// object implements the Object interface.
func (p *PostChecks) object() {}

// Type implements the Object.Type().
func (p *PostChecks) Type() ObjectType {
	return OTPostCheck
}

func (p *PostChecks) validate() ([]validator, error) {
	if p == nil {
		return nil, nil
	}
	if len(p.Actions) == 0 {
		return nil, fmt.Errorf("at least one action is required")
	}
	if p.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	vals := make([]validator, len(p.Actions))
	for i := 0; i < len(p.Actions); i++ {
		vals[i] = p.Actions[i]
	}

	return vals, nil
}

// ContChecks represents a set of actions that are executed while the workflow is running.
// They will automatically be run during the PreCheck sequence.
type ContChecks struct {
	// Actions is a list of actions that are executed in parallel. Any error will
	// cause the workflow to fail.
	Actions []*Action
	// Delay is the amount of time to wait between ContCheck runs. This defaults to 30 seconds. If
	// you want no delay, set this to < 0.
	Delay time.Duration

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// Type implements the Object.Type().
func (c *ContChecks) Type() ObjectType {
	return OTContCheck
}

// object implements the Object interface.
func (c *ContChecks) object() {}

func (c *ContChecks) defaults() {
	if c == nil {
		return
	}
	if c.Delay == 0 {
		c.Delay = 30 * time.Second
	}
}

func (c *ContChecks) validate() ([]validator, error) {
	if c == nil {
		return nil, nil
	}

	if len(c.Actions) == 0 {
		return nil, fmt.Errorf("at least one action is required")
	}

	if c.Internal != nil {
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
	// Name is the name of the block.
	Name string
	// Descr is a description of the block.
	Descr string

	// EntranceDelay is the amount of time to wait before the block starts. This defaults to 0.
	EntranceDelay time.Duration
	// ExitDelay is the amount of time to wait after the block has completed. This defaults to 0.
	ExitDelay time.Duration

	// PreChecks are actions that are executed before the block starts.
	// Any error will cause the block to fail.
	PreChecks *PreChecks
	// PostChecks are actions that are executed after the block has completed.
	// Any error will cause the block to fail.
	PostChecks *PostChecks
	// ContChecks are actions that are executed while the block is running.
	ContChecks *ContChecks

	// Sequences is a list of sequences that are executed.
	Sequences []*Sequence

	// Concurrency is the number of sequences that are executed in parallel. This defaults to 1.
	Concurrency int
	// ToleratedFailures is the number of sequences that are allowed to fail before the block fails. This defaults to 0.
	// If set to -1, all sequences are allowed to fail.
	ToleratedFailures int

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
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
	if b.Concurrency < 1 {
		b.Concurrency = 1
	}
	return b
}

func (b *Block) validate() ([]validator, error) {
	if b == nil {
		return nil, fmt.Errorf("cannot have a nil Block")
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

	if b.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if len(b.Sequences) == 0 {
		return nil, fmt.Errorf("at least one sequence is required")
	}

	vals := []validator{b.PreChecks, b.PostChecks, b.ContChecks}
	for _, seq := range b.Sequences {
		vals = append(vals, seq)
	}
	return vals, nil
}

// Sequence represents a set of Jobs that are executed in sequence. Any error will cause the workflow to fail.
type Sequence struct {
	// Name is the name of the sequence.
	Name string
	// Descr is a description of the sequence.
	Descr string
	// Jobs is a list of jobs that are executed in sequence. Any error will cause the workflow to fail.
	Jobs []*Job

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// Type implements the Object.Type().
func (s *Sequence) Type() ObjectType {
	return OTSequence
}

// object implements the Object interface.
func (s *Sequence) object() {}

func (s *Sequence) validate() ([]validator, error) {
	if s == nil {
		return nil, fmt.Errorf("cannot have a nil Sequence")
	}

	if strings.TrimSpace(s.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(s.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if s.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if len(s.Jobs) == 0 {
		return nil, fmt.Errorf("at least one job is required")
	}

	vals := make([]validator, 0, len(s.Jobs))
	for _, j := range s.Jobs {
		vals = append(vals, j)
	}
	return vals, nil
}

type Job struct {
	// Name is the name of the job.
	Name string
	// Descr is a description of the job.
	Descr string
	// Actions is a list of actions that are executed in sequence. Any error will
	// cause the workflow to fail.
	Action *Action
	// Timeout is the amount of time to wait for the Action to complete. This defaults to 30 seconds and
	// must be at least 5 seconds.
	Timeout time.Duration
	// Retries is the number of times to retry the Action if it fails. This defaults to 0.
	Retries int
	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal
}

// Type implements the Object.Type().
func (j *Job) Type() ObjectType {
	return OTJob
}

// object implements the Object interface.
func (j *Job) object() {}

func (j *Job) defaults() *Job {
	if j == nil {
		return j
	}
	if j.Timeout == 0 {
		j.Timeout = 30 * time.Second
	}
	if j.Retries < 0 {
		j.Retries = 0
	}

	return j
}

func (j *Job) validate() ([]validator, error) {
	if j == nil {
		return nil, fmt.Errorf("cannot have a nil Job")
	}

	if strings.TrimSpace(j.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(j.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}

	if j.Timeout < 5*time.Second {
		return nil, fmt.Errorf("timeout must be at least 5 seconds")
	}

	if j.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if j.Action == nil {
		return nil, fmt.Errorf("action is required")
	}

	if j.Action.isCheck() {
		return nil, fmt.Errorf("job(%s) %s: check actions are not allowed in jobs", j.Name, j.Action.Name)
	}

	vals := []validator{j.Action}
	return vals, nil
}

// register is an interface that is used to get a plugin by name.
// Note: violates interface naming, but Getter is too generic.
type register interface {
	Get(name string) plugins.Plugin
}

// Action represents a single action that is executed by a plugin.
type Action struct {
	// Name is the name of the action.
	Name string
	// Descr is a description of the action.
	Descr string

	// Plugin is the name of the plugin that is executed.
	Plugin string
	// Req is the request object that is passed to the plugin.
	Req any
	// Resp is the response object that is returned by the plugin.
	// This should not be set by the user.
	Resp any

	// Internal represents settings that should not be set by the user, but users can query.
	Internal *Internal

	register register
	plugin   plugins.Plugin
}

// Type implements the Object.Type().
func (a *Action) Type() ObjectType {
	return OTAction
}

// object implements the Object interface.
func (a *Action) object() {}

func (a *Action) isCheck() bool {
	if a.plugin == nil {
		panic("bug: plugin not set")
	}

	return a.plugin.IsCheck()
}

func (a *Action) validate() ([]validator, error) {
	if a == nil {
		return nil, fmt.Errorf("cannot have a nil Action")
	}

	if strings.TrimSpace(a.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(a.Descr) == "" {
		return nil, fmt.Errorf("description is required")
	}
	if a.Resp != nil {
		return nil, fmt.Errorf("response should not be set by the user")
	}

	if a.Internal != nil {
		return nil, fmt.Errorf("internal settings should not be set by the user")
	}

	if strings.TrimSpace(a.Plugin) == "" {
		return nil, fmt.Errorf("plugin is required")
	}

	if a.register == nil {
		plug := plugins.Registry.Get(a.Plugin)
		if plug != nil {
			a.plugin = plug
		}
	} else {
		plug := a.register.Get(a.Plugin)
		if plug != nil {
			a.plugin = plug
		}
	}
	if a.plugin == nil {
		return nil, fmt.Errorf("plugin %q not found", a.Plugin)
	}

	if err := a.plugin.ValidateReq(a.Req); err != nil {
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
