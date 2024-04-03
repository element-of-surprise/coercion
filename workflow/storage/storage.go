package storage

import (
	"context"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"

	"github.com/google/uuid"
)

type SearchFilter struct{}

type ReadWriter interface {
	PlanReader
	PlanWriter
}

type PlanReader interface {
	// IDExists returns true if the Plan ID exists in the storage.
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// ReadPlan returns a Plan from the storage.
	ReadPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
	// SearchPlans returns a list of Plan IDs that match the filter.
	SearchPlans(ctx context.Context, filter SearchFilter) ([]uuid.UUID, error)
	// ListPlans returns a list of all Plan IDs in the storage. This should
	// return with most recent submiited first.
	ListPlans(ctx context.Context, limit int) ([]uuid.UUID, error)

	private.Storage
}

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

type BlockWriter interface {
	// Write writes Block data to storage, but not underlying data.
	Write(context.Context, *workflow.Block) error

	// Checks returns a ChecksWriter for Checks in this Block.
	Checks() ChecksWriter
	// Sequence returns a SequenceWriter for Sequence in this Block.
	Sequence(uuid.UUID) SequenceWriter

	private.Storage
}

type ChecksWriter interface {
	PreChecks(context.Context, *workflow.PreChecks) error
	ContChecks(context.Context, *workflow.ContChecks) error
	PostChecks(context.Context, *workflow.PostChecks) error

	// Action returns an ActionWriter for Actions in either PreChecks, ContChecks or PostChecks.
	// The type of the Action is determined by the workflow.ObjectType. This must be:
	// workflow.OTPreCheck, workflow.OTContCheck or workflow.OTPostCheck or this will panic.
	Action(t workflow.ObjectType) ActionWriter
}

type SequenceWriter interface {
	// Write writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
	Write(context.Context, *workflow.Sequence) error

	// Action returns an ActionWriter for Actions in this Sequence.
	Action() ActionWriter

	private.Storage
}

// ActionWriter is a storage writer for Actions in a specific Sequence, PreChecks, PostChecks or ContChecks.
type ActionWriter interface {
	Write(context.Context, *workflow.Action) error

	private.Storage
}
