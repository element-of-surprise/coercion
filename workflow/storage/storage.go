// Package storage provides the storage interfaces for reading and writing workflow.Plan data.
// These interfaces can only be implemented from within the workflow package.
package storage

import (
	"fmt"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
)

// ErrNotFound is returned when an object is not found in storage.
var ErrNotFound = fmt.Errorf("plan not found")

// Filters is a filter for searching Plans.
type Filters struct {
	// ByIDs is a list of Plan IDs to search by.
	ByIDs []uuid.UUID
	// ByGroupIDs is a list of Group IDs to search by.
	ByGroupIDs []uuid.UUID
	// ByStatus is a list of Plan states to search by.
	ByStatus []workflow.Status
}

// Validate validates the search filter.
func (f Filters) Validate() error {
	if len(f.ByIDs)+len(f.ByGroupIDs)+len(f.ByStatus) == 0 {
		return fmt.Errorf("at least one search filter must be provided")
	}
	return nil
}

// Stream represents an entry in a stream of data.
type Stream[T any] struct {
	// Result is a result in the stream.
	Result T
	// Err is an error in the stream.
	Err error
}

// ListResult is a result from a List operation.
type ListResult struct {
	// ID is the Plan ID.
	ID uuid.UUID
	// GroupID is the Group ID.
	GroupID uuid.UUID
	// Name is the Plan name.
	Name string
	// Descr is the Plan description.
	Descr string
	// SubmitTime is the Plan submit time.
	SubmitTime time.Time
	// State is the Plan state.
	State *workflow.State
}

// Vault is a storage reader and writer for Plan data. An implementation of Vault must ensure
// atomic writes for all data. It also should never return an error for any operation that is
// not a permanent failure. It should otherwise retry unil the operations succeeds. A  permanent
// failure is one in which an unreasonable amount of time has passed or the an error is returned
// that is not recoverable under any circumstances (disk is full, but not transient network errors where
// you could reconnect).
type Vault interface {
	Reader
	Creator
	Updater
	Closer
	Deleter
}

// Creator allows for creating Plan data in storage.
type Creator interface {
	// Create creates a new Plan in storage. This fails if the Plan ID already exists.
	Create(ctx context.Context, plan *workflow.Plan) error

	private.Storage
}

// Closer allows for closing the storage.
type Closer interface {
	Close(ctx context.Context) error

	private.Storage
}

// Deleter allows for deleting Plan data from storage.
type Deleter interface {
	Delete(ctx context.Context, id uuid.UUID) error

	private.Storage
}

// Reader allows for reading Plan data from storage.
type Reader interface {
	// Exists returns true if the Plan ID exists in the storage.
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// Read returns a Plan from the storage.
	Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
	// Search returns a list of Plan IDs that match the filter.
	// TODO(jdoak): Consider changing this to return an iterator.
	Search(ctx context.Context, filters Filters) (chan Stream[ListResult], error)
	// ListPlans returns a list of all Plan IDs in the storage. This should
	// return with most recent submiited first.
	// TODO(jdoak): Consider changing this to return an iterator.
	List(ctx context.Context, limit int) (chan Stream[ListResult], error)

	private.Storage
}

// Updater allows for writing Plan data to storage.
type Updater interface {
	PlanUpdater
	ChecksUpdater
	BlockUpdater
	SequenceUpdater
	ActionUpdater

	private.Storage
}

// PlanUpdater allows for writing Plan data to storage.
type PlanUpdater interface {
	// UpdatePlan updates an existing Plan in storage. This fails if the Plan ID does not exist.
	// This only applies the changes in the Plan, not the entire Plan hierarchy.
	UpdatePlan(ctx context.Context, plan *workflow.Plan) error

	private.Storage
}

// BlockUpdater allows for writing Block data to storage.
type BlockUpdater interface {
	// UpdateBlock writes Block data to storage, but not underlying data.
	UpdateBlock(context.Context, *workflow.Block) error

	private.Storage
}

// ChecksUpdater is a storage writer for Checks in a specific Plan or Block.
type ChecksUpdater interface {
	// UpdateChecks writes Checks states data to storage but not underlying data.
	UpdateChecks(context.Context, *workflow.Checks) error

	private.Storage
}

// SequenceUpdater is a storage writer for Sequences in a specific Block.
type SequenceUpdater interface {
	// UpdateSequence writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
	UpdateSequence(context.Context, *workflow.Sequence) error

	private.Storage
}

// ActionUpdater is a storage writer for Actions in a specific Sequence, PreChecks, PostChecks or ContChecks.
type ActionUpdater interface {
	// UpdateAction writes Action data to storage for Actions in the specific Sequence, PreChecks, PostChecks or ContChecks.
	UpdateAction(context.Context, *workflow.Action) error

	private.Storage
}

// Recovery is a Vault that must do some recovery operation before it can be used after a failure
// or restart. Not all Vaults implement this.
type Recovery interface {
	Recovery(context.Context) error
}
