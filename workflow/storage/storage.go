// Package storage provides the storage interfaces for reading and writing workflow.Plan data.
// These interfaces can only be implemented from within the workflow package.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"

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

// ReadWriter is a storage reader and writer for Plan data. An implementation of ReadWriter must ensure
// atomic writes for all data.
type ReadWriter interface {
	PlanReader

	// Create creates a new Plan in storage. This fails if the Plan ID already exists.
	Create(ctx context.Context, plan *workflow.Plan) error

	// Writer returns a PlanWriter for the given Plan ID.
	Writer(context.Context, uuid.UUID) (PlanWriter, error)

	// Close closes the storage.
	Close(ctx context.Context) error
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

// PlanReader allows for reading Plan data from storage.
type PlanReader interface {
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

// PlanWriter allows for writing Plan data to storage.
type PlanWriter interface {
	// Write writes Plan data to storage, and all underlying data.
	Write(context.Context, *workflow.Plan) error

	// Checks returns a ChecksWriter.
	Checks() ChecksWriter
	// Block returns a BlockWriter.
	// this will panic.
	Block() BlockWriter
	// Sequence returns a SequenceWriter.
	Sequence() SequenceWriter
	// Action returns an ActionWriter.
	Action() ActionWriter

	private.Storage
}

// BlockWriter allows for writing Block data to storage.
type BlockWriter interface {
	// Write writes Block data to storage, but not underlying data.
	Write(context.Context, *workflow.Block) error

	private.Storage
}

// ChecksWriter is a storage writer for Checks in a specific Plan or Block.
type ChecksWriter interface {
	// Write writes Checks states data to storage but not underlying data.
	Write(context.Context, *workflow.Checks) error

	private.Storage
}

// SequenceWriter is a storage writer for Sequences in a specific Block.
type SequenceWriter interface {
	// Write writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
	Write(context.Context, *workflow.Sequence) error

	private.Storage
}

// ActionWriter is a storage writer for Actions in a specific Sequence, PreChecks, PostChecks or ContChecks.
type ActionWriter interface {
	// Write writes Action data to storage for Actions in the specific Sequence, PreChecks, PostChecks or ContChecks.
	Write(context.Context, *workflow.Action) error

	private.Storage
}
