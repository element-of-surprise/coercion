// Package storage provides the storage interfaces for reading and writing workflow.Plan data.
// These interfaces can only be implemented from within the workflow package.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/workstream/internal/private"
	"github.com/element-of-surprise/workstream/workflow"

	"github.com/google/uuid"
)

// Filters is a filter for searching Plans.
type Filters struct{
	// ByIDs is a list of Plan IDs to search by.
	ByIDs []uuid.UUID
	// ByGroupIDs is a list of Group IDs to search by.
	ByGroupIDs []uuid.UUID
	// ByStatus is a list of Plan states to search by.
	ByStatus []workflow.Status
}

// Validate validates the search filter.
func (f Filters) Validate() error {
	if len(f.ByIDs) + len(f.ByGroupIDs) + len(f.ByStatus) == 0 {
		return fmt.Errorf("at least one search filter must be provided")
	}
	return nil
}

// Stream represents an entry in a stream of data.
type Stream[T any] struct {
	// Result is a result in the stream.
	Result T
	// Err is an error in the stream.
	Err    error
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
// atomic writes for all data.
type Vault interface {
	Reader
	Creator
	Updater
	Closer
}

// Creator allows for creating Plan data in storage.
type Creator interface {
	// Create creates a new Plan in storage. This fails if the Plan ID already exists.
	Create (ctx context.Context, plan *workflow.Plan) error

	private.Storage
}

// Closer allows for closing the storage.
type Closer interface {
	Close(ctx context.Context) error

	private.Storage
}

// Reader allows for reading Plan data from storage.
type Reader interface {
	// Exists returns true if the Plan ID exists in the storage.
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// Read returns a Plan from the storage.
	Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
	// Search returns a list of Plan IDs that match the filter.
	Search(ctx context.Context, filters Filters) (chan Stream[ListResult], error)
	// ListPlans returns a list of all Plan IDs in the storage. This should
	// return with most recent submiited first.
	List(ctx context.Context, limit int) (chan Stream[ListResult], error)

	private.Storage
}

// Updater allows for writing Plan data to storage.
type Updater interface {
	ChecksUpdater
	BlockUpdater
	SequenceUpdater
	ActionUpdater

	private.Storage
}

// BlockUpdater allows for writing Block data to storage.
type BlockUpdater interface {
	// Update writes Block data to storage, but not underlying data.
	UpdateBlock(context.Context, *workflow.Block) error

	private.Storage
}

// ChecksUpdater is a storage writer for Checks in a specific Plan or Block.
type ChecksUpdater interface {
	// Update writes Checks states data to storage but not underlying data.
	UpdateChecks(context.Context, *workflow.Checks) error

	private.Storage
}

// SequenceUpdater is a storage writer for Sequences in a specific Block.
type SequenceUpdater interface {
	// Update writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
	UpdateSequence(context.Context, *workflow.Sequence) error

	private.Storage
}

// ActionUpdater is a storage writer for Actions in a specific Sequence, PreChecks, PostChecks or ContChecks.
type ActionUpdater interface {
	// Update writes Action data to storage for Actions in the specific Sequence, PreChecks, PostChecks or ContChecks.
	UpdateAction(context.Context, *workflow.Action) error

	private.Storage
}
