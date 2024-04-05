// Package storage provides the storage interfaces for reading and writing workflow.Plan data.
// These interfaces can only be implemented from within the workflow package.
package storage

import (
	"context"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"

	"github.com/google/uuid"
)

// SearchFilter is a filter for searching Plans.
type SearchFilter struct{}

// ReadWriter is a storage reader and writer for Plan data. An implementation of ReadWriter must ensure
// atomic writes for all data.
type ReadWriter interface {
	PlanReader

	// CreatePlan creates a new Plan in storage. This fails if the Plan ID already exists.
	CreatePlan(ctx context.Context, plan *workflow.Plan) error

	// PlanWriter returns a PlanWriter for the given Plan ID.
	PlanWriter(context.Context, uuid.UUID) (PlanWriter, error)

	// Close closes the storage.
	Close(ctx context.Context) error
}

// PlanReader allows for reading Plan data from storage.
type PlanReader interface {
	// Exists returns true if the Plan ID exists in the storage.
	Exists(ctx context.Context, id uuid.UUID) (bool, error)
	// Read returns a Plan from the storage.
	Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
	// Search returns a list of Plan IDs that match the filter.
	Search(ctx context.Context, filter SearchFilter) ([]uuid.UUID, error)
	// ListPlans returns a list of all Plan IDs in the storage. This should
	// return with most recent submiited first.
	List(ctx context.Context, limit int) ([]uuid.UUID, error)

	private.Storage
}

// PlanWriter allows for writing Plan data to storage.
type PlanWriter interface {
	// Write writes Plan data to storage, and all underlying data.
	Write(context.Context, *workflow.Plan) error

	// Checks returns a PlanChecksWriter.
	Checks() ChecksWriter
	// Block returns a BlockWriter for the given Block ID. If the Block ID does not exist,
	// this will panic.
	Block(id uuid.UUID) BlockWriter

	private.Storage
}

// BlockWriter allows for writing Block data to storage.
type BlockWriter interface {
	// Write writes Block data to storage, but not underlying data.
	Write(context.Context, *workflow.Block) error

	// Checks returns a ChecksWriter for Checks in this Block.
	Checks() ChecksWriter
	// Sequence returns a SequenceWriter for Sequence in this Block.
	Sequence(uuid.UUID) SequenceWriter

	private.Storage
}

// ChecksWriter is a storage writer for Checks in a specific Plan or Block.
type ChecksWriter interface {
	// PreChecks writes PreChecks data to storage for PreChecks in the specific Plan or Block, but not underlying data.
	PreChecks(context.Context, *workflow.Checks) error
	// ContChecks writes ContChecks data to storage for ContChecks in the specific Plan or Block, but not underlying data.
	ContChecks(context.Context, *workflow.Checks) error
	// PostChecks writes PostChecks data to storage for PostChecks in the specific Plan or Block, but not underlying data.
	PostChecks(context.Context, *workflow.Checks) error

	// Action returns an ActionWriter for Actions in either PreChecks, ContChecks or PostChecks.
	// The type of the Action is determined by the workflow.ObjectType. This must be:
	// workflow.OTPreCheck, workflow.OTContCheck or workflow.OTPostCheck or this will panic.
	Action(t workflow.ObjectType) ActionWriter
}

// SequenceWriter is a storage writer for Sequences in a specific Block.
type SequenceWriter interface {
	// Write writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
	Write(context.Context, *workflow.Sequence) error

	// Action returns an ActionWriter for Actions in this Sequence.
	Action() ActionWriter

	private.Storage
}

// ActionWriter is a storage writer for Actions in a specific Sequence, PreChecks, PostChecks or ContChecks.
type ActionWriter interface {
	// Write writes Action data to storage for Actions in the specific Sequence, PreChecks, PostChecks or ContChecks.
	Write(context.Context, *workflow.Action) error

	private.Storage
}
