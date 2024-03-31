/*
Package builder allows building a workflow Plan sequentially instead of
constructing a Plan object manually.

Usage is simple:

	// This a fictional plan to upgrade a service in various clusters.
	// We have a continuous check to ensure the service is healthy cluster-wide.
	// We have a block level continous check to ensure the cluster is healthy.
	// We tolderate 1 failure in the block, but we may have up to 5 machines upgrading at once,
	// so there could be up to 5 failures in the block.
	// We leave the Sequence that each machine goes through to your imagination.

	// Create a new plan.
	bp := builder.New("cluster upgrade plane", "a description")
	bp.AddContChecks(serviceHealthyAction("service"))
	for _, cluster := range clusters {
		bp.AddBlock(
			builder.BlockArgs{
				Name: "Upgrade packages on machines in cluster X"
				Descr: "More details",
				ToleratedFailures: 1,
				Concurrency 5,
				ExitDelay: 10 * time.Minute,
			},
		)
		bp.AddContChecks(clusterHealthyAction("cluster"))
		for _, machine := range cluster.Machines() {
			bp.AddSequenceDirect(upgradeSeq(machine))
		}
	}

	plan := bp.Plan()
*/
package builder

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/google/uuid"
)

// BuildPlan is a builder for creating a workflow Plan. It is not safe for concurrent use.
type BuildPlan struct {
	chain []any

	emitted bool
}

// Option provides optional arguments to the New() constructor.
type Option func(*BuildPlan) error

// WithGroupID sets the group ID for the Plan.
func WithGroupID(id uuid.UUID) Option {
	return func(b *BuildPlan) error {
		if b.emitted {
			return errors.New("cannot call WithGroupID() after Plan() has been called")
		}

		if id == uuid.Nil {
			return errors.New("group ID must not be nil")
		}

		b.current().(*workflow.Plan).GroupID = id
		return nil
	}
}

// New creates a new BuildPlan with the internal Plan object having the given
// name and description.
func New(name, descr string, options ...Option) (*BuildPlan, error) {
	b := &BuildPlan{}
	if err := b.Reset(name, descr, options...); err != nil {
		return nil, err
	}

	for _, o := range options {
		if err := o(b); err != nil {
			return nil, err
		}
	}

	return b, nil
}

// current returns the current object in the chain.
func (b *BuildPlan) current() any {
	if len(b.chain) == 0 {
		panic("bug: chain is empty and that shouldn't happen")
	}

	return b.chain[len(b.chain)-1]
}

// Plan emits the built Plan object. No other methods except Reset()
// should be called after this. If called without a Reset(), it will return nil.
func (b *BuildPlan) Plan() *workflow.Plan {
	if b.emitted {
		return nil
	}

	b.emitted = true
	return b.chain[0].(*workflow.Plan)
}

// Reset resets the internal Plan object to a new object with the given name and description.
// This will allow you to call methods on the object after Plan() is called.
func (b *BuildPlan) Reset(name, descr string, options ...Option) error {
	b.emitted = false
	b.chain = b.chain[:0]

	switch "" {
	case strings.TrimSpace(name), strings.TrimSpace(descr):
		return errors.New("name and description must not be empty")
	}

	for _, o := range options {
		if err := o(b); err != nil {
			return err
		}
	}
	if len(b.chain) == 0 {
		plan := &workflow.Plan{Name: name, Descr: descr}
		b.chain = append(b.chain, plan)
	}

	return nil
}

// Up moves your current position up one level in the plan hierarchy.
func (b *BuildPlan) Up() error {
	if b.emitted {
		return errors.New("cannot call Up() after Plan() has been called")
	}

	if len(b.chain) < 2 {
		return errors.New("cannot go up from root")
	}
	b.chain = b.chain[:len(b.chain)-1]
	return nil
}

// AddPreChecks adds a pre-check actions to the current workflow Plan or Block.
// If at any other level of the plan hierarchy, AddPreChecks will return an error.
func (b *BuildPlan) AddPreChecks(actions ...*workflow.Action) error {
	if b.emitted {
		return errors.New("cannot call AddPreChecks after Plan() has been called")
	}

	if len(actions) == 0 {
		return errors.New("at least one action must be provided")
	}

	for _, action := range actions {
		if action == nil {
			return errors.New("action must not be nil")
		}
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		if t.PreChecks != nil {
			return errors.New("cannot add PreCheck to Plan with existing PreChecks")
		}
		t.PreChecks = &workflow.PreChecks{Actions: actions}
	case *workflow.Block:
		if t.PreChecks != nil {
			return errors.New("cannot add PreCheck to Block with existing PreChecks")
		}
		t.PreChecks = &workflow.PreChecks{Actions: actions}
	default:
		return fmt.Errorf("invalid type for PreChecks: %T", b.current)
	}
	return nil
}

// AddPostChecks adds post-check actions to the current workflow Plan or Block.
// If at any other level of the plan hierarchy, AddPostChecks will return an error.
func (b *BuildPlan) AddPostChecks(actions ...*workflow.Action) error {
	if b.emitted {
		return errors.New("cannot call AddPostChecks after Plan() has been called")
	}

	if len(actions) == 0 {
		return errors.New("at least one action must be provided")
	}

	for _, action := range actions {
		if action == nil {
			return errors.New("action must not be nil")
		}
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		if t.PostChecks != nil {
			return errors.New("cannot add PostCheck to Plan with existing PostChecks")
		}
		t.PostChecks = &workflow.PostChecks{
			Actions: actions,
		}
	case *workflow.Block:
		if t.PostChecks != nil {
			return errors.New("cannot add PostCheck to Block with existing PostChecks")
		}
		t.PostChecks = &workflow.PostChecks{
			Actions: actions,
		}
	default:
		return fmt.Errorf("invalid type for PostChecks: %T", b.current)
	}
	return nil
}

// AddContChecks adds continuous check actions to the current workflow Plan or Block.
// If at any other level of the plan hierarchy, AddContCheck will return an error.
func (b *BuildPlan) AddContChecks(delay time.Duration, actions ...*workflow.Action) error {
	if b.emitted {
		return errors.New("cannot call AddContChecks after Plan() has been called")
	}

	if len(actions) == 0 {
		return errors.New("at least one action must be provided")
	}

	for _, action := range actions {
		if action == nil {
			return errors.New("action must not be nil")
		}
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		if t.ContChecks != nil {
			return errors.New("cannot add ContCheck to Plan with existing ContChecks")
		}
		t.ContChecks = &workflow.ContChecks{
			Delay:   delay,
			Actions: actions,
		}
	case *workflow.Block:
		if t.ContChecks != nil {
			return errors.New("cannot add ContCheck to Block with existing ContChecks")
		}
		t.ContChecks = &workflow.ContChecks{
			Delay:   delay,
			Actions: actions,
		}
	default:
		return fmt.Errorf("invalid type for ContChecks: %T", b.current)
	}
	return nil
}

// BlockArgs are arguements for AddBlock that define a Block in the Plan.
type BlockArgs struct {
	Name                     string
	Descr                    string
	EntranceDelay, ExitDelay time.Duration
	Concurrenct              int
	ToleratedFailures        int
}

// AddBlock adds a Block to the current workflow Plan. If at any other level of the plan hierarchy,
// AddBlock will return an error. This moves the current position in the plan hierarchy to the new Block.
func (b *BuildPlan) AddBlock(args BlockArgs) error {
	if b.emitted {
		return errors.New("cannot call AddBlock() after Plan() has been called")
	}

	if args.Name == "" {
		return errors.New("block name must be provided")
	}
	if args.Descr == "" {
		return errors.New("block description must be provided")
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		block := &workflow.Block{
			Name:              args.Name,
			Descr:             args.Descr,
			EntranceDelay:     args.EntranceDelay,
			ExitDelay:         args.ExitDelay,
			Concurrency:       args.Concurrenct,
			ToleratedFailures: args.ToleratedFailures,
		}
		t.Blocks = append(t.Blocks, block)
		b.chain = append(b.chain, block)
		return nil
	}
	return fmt.Errorf("invalid type for AddBlock(): %T", b.current)
}

// AddSequence adds a Sequence to the current workflow Block with the name and descr provided.
// It will then move into the Sequence so that you may add Jobs. If at any other level of the plan hierarchy,
// AddSequence will return an error.
func (b *BuildPlan) AddSequence(name, descr string) error {
	if b.emitted {
		return errors.New("cannot call AddSequence() after Plan() has been called")
	}
	if name == "" {
		return errors.New("sequence name must be provided")
	}
	if descr == "" {
		return errors.New("sequence description must be provided")
	}

	switch t := b.current().(type) {
	case *workflow.Block:
		seq := &workflow.Sequence{Name: name, Descr: descr}
		t.Sequences = append(t.Sequences, seq)
		b.chain = append(b.chain, seq)
		return nil
	}
	return fmt.Errorf("invalid type for AddSequence(): %T", b.current)
}

// AddSequenceDirect adds a Sequence to the current workflow Block WITHOUT moving the current position
// in the plan hierarchy. This is useful when you have a function that directly creates a Sequence.
func (b *BuildPlan) AddSequenceDirect(seq *workflow.Sequence) error {
	if b.emitted {
		return errors.New("cannot call AddSequenceDirect() after Plan() has been called")
	}

	if seq.Name == "" {
		return errors.New("sequence name must be provided")
	}
	if seq.Descr == "" {
		return errors.New("sequence description must be provided")
	}
	if len(seq.Jobs) == 0 {
		return errors.New("sequence must have at least one job")
	}

	switch t := b.current().(type) {
	case *workflow.Block:
		t.Sequences = append(t.Sequences, seq)
		return nil
	}
	return fmt.Errorf("invalid type for AddSequenceDirect(): %T", b.current)
}

// AddJob adds a Job to the current workflow Sequence. If at any other level of the plan hierarchy,
// AddJob will return an error.
func (b *BuildPlan) AddJob(job *workflow.Job) error {
	if b.emitted {
		return errors.New("cannot call AddJob() after Plan() has been called")
	}

	if job.Name == "" {
		return errors.New("job name must be provided")
	}
	if job.Descr == "" {
		return errors.New("job description must be provided")
	}
	if job.Action == nil {
		return errors.New("job must have an action")
	}

	switch t := b.current().(type) {
	case *workflow.Sequence:
		t.Jobs = append(t.Jobs, job)
		return nil
	}
	return fmt.Errorf("invalid type for AddJobDirect(): %T", b.current)
}
