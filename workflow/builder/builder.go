/*
Package builder allows building a workflow Plan sequentially instead of
constructing a Plan object manually.

This package lets you doa streaming build. While each method returns an error,
you generally ignore all errors until the final call to BuildPlan.Plan(). This emits
the Plan object or the first error encountered. All method calls after the first error
will return the same error and are no-ops.

Usage is simple:

	// This a fictional plan to upgrade a service in various clusters.
	// We have a continuous check to ensure the service is healthy cluster-wide.
	// We have a block level continous check to ensure the cluster is healthy.
	// We tolderate 1 failure in the block, but we may have up to 5 machines upgrading at once,
	// so there could be up to 5 failures in the block.
	// We leave the Sequence that each machine goes through to your imagination.

	// Create a new plan.
	bp := builder.New("cluster upgrade plane", "a description")
	bp.AddChecks(builder.PreCheck, serviceHealthyChecks("service"))
	bp.Up() // Moves us out of the Checks scope.
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
		bp.AddChecks(clusterHealthyAction("cluster"))
		bp.Up() // Moves us out of the checks scope.
		for _, machine := range cluster.Machines() {
			bp.AddSequence(upgradeSeq(machine))
		}
		bp.Up() // Moves us out of the Block scope.
	}

	plani, err := bp.Plan()

Note that in the abovee example, we use methods like clusterHealthyAction() and upgradeSeq() to
create the various checks and actions. You can also add the actions to a sequence or checks via the
AddAction() method instead of providding a function that returns a Sequence or Checks object.
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
	err     error
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
// should be called after this. If there was any call while building the Plan, this
// will return that first error. This can only be called once until a Reset() is called,
// otherwise it will return an error.
func (b *BuildPlan) Plan() (*workflow.Plan, error) {
	if b.emitted {
		return nil, errors.New("cannot call Plan() more than once")
	}
	if b.err != nil {
		return nil, b.err
	}

	b.emitted = true
	return b.chain[0].(*workflow.Plan), nil
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

	if len(b.chain) == 0 {
		plan := &workflow.Plan{Name: name, Descr: descr}
		b.chain = append(b.chain, plan)
	}
	for _, o := range options {
		if err := o(b); err != nil {
			return err
		}
	}
	b.err = nil

	return nil
}

func (b *BuildPlan) setErr(err error) error {
	b.err = err
	return err
}

// Up moves your current position up one level in the plan hierarchy.
func (b *BuildPlan) Up() error {
	if b.emitted {
		return errors.New("cannot call Up() after Plan() has been called")
	}
	if b.err != nil {
		return b.err
	}

	if len(b.chain) < 2 {
		return b.setErr(errors.New("cannot go up from root"))
	}
	b.chain = b.chain[:len(b.chain)-1]
	return nil
}

// ChecksType is the check type you are adding to a Plan or Block.
type ChecksType int

const (
	// CTUnknown is an unknown check type. This should never be used
	// and indicates a bug in the code.
	CTUnknown ChecksType = 0
	// PreCheck is a set of pre-checks.
	PreCheck ChecksType = 1
	// ContCheck is a continuous check.
	ContCheck ChecksType = 2
	// PostCheck is a set of post-checks.
	PostCheck ChecksType = 3
)

// AddChecks adds a check to the current Plan or Block. This moves you into the check.
// If at any other level of the plan hierarchy, AddChecks will return an error.
func (b *BuildPlan) AddCheck(cType ChecksType, check *workflow.Checks) error {
	if b.emitted {
		return errors.New("cannot call AddCheck after Plan() has been called")
	}
	if b.err != nil {
		return b.err
	}

	if check == nil {
		return b.setErr(errors.New("check must not be nil"))
	}

	for _, action := range check.Actions {
		if action == nil {
			return b.setErr(errors.New("action n a workflow.Checks must not be nil"))
		}
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		switch cType {
		case PreCheck:
			if t.PreChecks != nil {
				return b.setErr(errors.New("cannot add PreCheck to Plan with existing PreChecks"))
			}
			t.PreChecks = check
			b.chain = append(b.chain, check)
		case ContCheck:
			if t.ContChecks != nil {
				return b.setErr(errors.New("cannot add ContCheck to Plan with existing ContChecks"))
			}
			t.ContChecks = check
			b.chain = append(b.chain, check)
		case PostCheck:
			if t.PostChecks != nil {
				return b.setErr(errors.New("cannot add PostCheck to Plan with existing PostChecks"))
			}
			t.PostChecks = check
			b.chain = append(b.chain, check)
		default:
			return errors.New("unknown check type")
		}
	case *workflow.Block:
		switch cType {
		case PreCheck:
			if t.PreChecks != nil {
				return b.setErr(errors.New("cannot add PreCheck to Block with existing PreChecks"))
			}
			t.PreChecks = check
			b.chain = append(b.chain, check)
		case ContCheck:
			if t.ContChecks != nil {
				return b.setErr(errors.New("cannot add ContCheck to Block with existing ContChecks"))
			}
			t.ContChecks = check
			b.chain = append(b.chain, check)
		case PostCheck:
			if t.PostChecks != nil {
				return b.setErr(errors.New("cannot add PostCheck to Block with existing PostChecks"))
			}
			t.PostChecks = check
			b.chain = append(b.chain, check)
		default:
			return b.setErr(errors.New("unknown check type"))
		}
	default:
		return b.setErr(errors.New("cannot add checks to a non-Plan or non-Block object"))
	}
	return nil
}

// BlockArgs are arguements for AddBlock that define a Block in the Plan.
type BlockArgs struct {
	Name                     string
	Descr                    string
	EntranceDelay, ExitDelay time.Duration
	Concurrency              int
	ToleratedFailures        int
}

// AddBlock adds a Block to the current workflow Plan. If at any other level of the plan hierarchy,
// AddBlock will return an error. This moves the current position in the plan hierarchy to the new Block.
func (b *BuildPlan) AddBlock(args BlockArgs) error {
	if b.emitted {
		return b.setErr(errors.New("cannot call AddBlock() after Plan() has been called"))
	}
	if b.err != nil {
		return b.err
	}

	if args.Name == "" {
		return b.setErr(errors.New("block name must be provided"))
	}
	if args.Descr == "" {
		return b.setErr(errors.New("block description must be provided"))
	}

	switch t := b.current().(type) {
	case *workflow.Plan:
		block := &workflow.Block{
			Name:              args.Name,
			Descr:             args.Descr,
			EntranceDelay:     args.EntranceDelay,
			ExitDelay:         args.ExitDelay,
			Concurrency:       args.Concurrency,
			ToleratedFailures: args.ToleratedFailures,
		}
		t.Blocks = append(t.Blocks, block)
		b.chain = append(b.chain, block)
		return nil
	}
	return b.setErr(fmt.Errorf("invalid type for AddBlock(): %T", b.current))
}

// AddSequence adds a Sequence to the current workflow Block with the name and descr provided.
// This moves into the Sequence so that you can add Actions to it (or additional actions).
// If at any other level of the plan hierarchy, AddSequence will return an error.
func (b *BuildPlan) AddSequence(seq *workflow.Sequence) error {
	if b.emitted {
		return b.setErr(errors.New("cannot call AddSequence() after Plan() has been called"))
	}
	if b.err != nil {
		return b.err
	}

	if seq == nil {
		return b.setErr(errors.New("sequence must not be nil"))
	}

	if seq.Name == "" {
		return b.setErr(errors.New("sequence name must be provided"))
	}
	if seq.Descr == "" {
		return b.setErr(errors.New("sequence description must be provided"))
	}

	switch t := b.current().(type) {
	case *workflow.Block:
		t.Sequences = append(t.Sequences, seq)
		b.chain = append(b.chain, seq)
		return nil
	}
	return b.setErr(fmt.Errorf("invalid type for AddSequence(): %T", b.current))
}

// AddAction adds an Action to the current workflow Sequence or Checks object. If at any other level of the plan hierarchy,
// AddAction will return an error.
func (b *BuildPlan) AddAction(action *workflow.Action) error {
	if b.emitted {
		return b.setErr(errors.New("cannot call AddAction() after Plan() has been called"))
	}
	if b.err != nil {
		return b.err
	}

	if action.Name == "" {
		return b.setErr(errors.New("action name must be provided"))
	}
	if action.Descr == "" {
		return b.setErr(errors.New("action description must be provided"))
	}
	if action.Plugin == "" {
		return b.setErr(errors.New("action plugin must be provided"))
	}

	switch t := b.current().(type) {
	case *workflow.Sequence:
		t.Actions = append(t.Actions, action)
		return nil
	case *workflow.Checks:
		t.Actions = append(t.Actions, action)
		return nil
	}
	return b.setErr(fmt.Errorf("invalid type for AddAction(): %T", b.current))
}
