package storage

import (
	"context"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"

	"github.com/google/uuid"
)

// Storage is the interface that must be implemented by all storage packages.
type Storage interface {
	Plans
	Blocks
	Sequences
	Checks
	Actions

	private.Storage
}

// TODO(element-of-surprise): Need to add something about metadata only loading.

type Plans interface {
	// IDExists returns true if the Plan ID exists in the storage.
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// ReadPlan returns a Plan from the storage.
	ReadPlan(ctx context.Context, id uuid.UUID) (*workflow.Plan, error)
	// WritePlan writes a Plan to the storage.
	WritePlan(ctx context.Context, plan *workflow.Plan) error
	// SearchPlans returns a list of Plan IDs that match the filter.
	SearchPlans(ctx context.Context, filter SearchFilter) ([]uuid.UUID, error)
	// ListPlans returns a list of all Plan IDs in the storage. This should
	// return with most recent submiited first.
	ListPlans(ctx context.Context, limit int) ([]uuid.UUID, error)
}

type Blocks interface {
	ReadBlock(ctx context.Context, id uuid.UUID) (*workflow.Block, error)
	WriteBlock(ctx context.Context, id uuid.UUID, block *workflow.Block) error
}

type Sequences interface {
	ReadSequence(ctx context.Context, id uuid.UUID) (*workflow.Sequence, error)
	WriteSequence(ctx context.Context, id uuid.UUID, sequence *workflow.Sequence) error
}

type Checks interface {
	ReadPreCheck(ctx context.Context, id uuid.UUID) (*workflow.PreCheck, error)
	WritePreCheck(ctx context.Context, id uuid.UUID, preCheck *workflow.PreCheck) error
	ReadPostCheck(ctx context.Context, id uuid.UUID) (*workflow.PostCheck, error)
	WritePostCheck(ctx context.Context, id uuid.UUID, postCheck *workflow.PostCheck) error
	ReadContCheck(ctx context.Context, id uuid.UUID) (*workflow.ContCheck, error)
	WriteContCheck(ctx context.Context, id uuid.UUID, contCheck *workflow.ContCheck) error
}

type Actions interface {
	ReadAction(ctx context.Context, id uuid.UUID) (*workflow.Action, error)
	WriteAction(ctx context.Context, id uuid.UUID, action *workflow.Action) error
}

type SearchFilter struct{}
