package cosmosdb

import (
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage/cosmosdb/internal/types"
	"github.com/google/uuid"
)

// Type is the type of cosmosdb document.
type Type = types.Type

const (
	// Unknown indicates that a type was not provided. This is a bug.
	Unknown = types.Unknown
	// Plan indicates that the document stores a plan.
	Plan = types.Plan
	// Block indicates that the document stores a block.
	Block = types.Block
	// Checks indicates that the document stores checks.
	Checks = types.Checks
	// Sequence indicates that the document stores a sequence.
	Sequence = types.Sequence
	// Action indicates that the document stores an action.
	Action = types.Action
)

type plansEntry struct {
	PK             string                 `json:"pk,omitempty"`
	Type           Type                   `json:"type,omitempty"`
	ID             uuid.UUID              `json:"id,omitempty"`
	GroupID        uuid.UUID              `json:"groupID,omitempty"`
	Name           string                 `json:"name,omitempty"`
	Descr          string                 `json:"descr,omitempty"`
	Meta           []byte                 `json:"meta,omitempty"`
	BypassChecks   uuid.UUID              `json:"bypassChecks,omitempty"`
	PreChecks      uuid.UUID              `json:"preChecks,omitempty"`
	PostChecks     uuid.UUID              `json:"postChecks,omitempty"`
	ContChecks     uuid.UUID              `json:"contChecks,omitempty"`
	DeferredChecks uuid.UUID              `json:"deferredChecks,omitempty"`
	Blocks         []uuid.UUID            `json:"blocks,omitempty"`
	StateStatus    workflow.Status        `json:"stateStatus,omitempty"`
	StateStart     time.Time              `json:"stateStart,omitempty"`
	StateEnd       time.Time              `json:"stateEnd,omitempty"`
	SubmitTime     time.Time              `json:"submitTime,omitempty"`
	Reason         workflow.FailureReason `json:"reason,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type blocksEntry struct {
	PK                string          `json:"pk,omitempty"`
	Type              Type            `json:"type,omitempty"`
	ID                uuid.UUID       `json:"id,omitempty"`
	Key               uuid.UUID       `json:"key,omitempty"`
	PlanID            uuid.UUID       `json:"planID,omitempty"`
	Name              string          `json:"name,omitempty"`
	Descr             string          `json:"descr,omitempty"`
	Pos               int             `json:"pos,omitempty"`
	EntranceDelay     time.Duration   `json:"entranceDelay,omitempty"`
	ExitDelay         time.Duration   `json:"exitDelay,omitempty"`
	BypassChecks      uuid.UUID       `json:"bypassChecks,omitempty"`
	PreChecks         uuid.UUID       `json:"preChecks,omitempty"`
	PostChecks        uuid.UUID       `json:"postChecks,omitempty"`
	ContChecks        uuid.UUID       `json:"contChecks,omitempty"`
	DeferredChecks    uuid.UUID       `json:"deferredChecks,omitempty"`
	Sequences         []uuid.UUID     `json:"sequences,omitempty"`
	Concurrency       int             `json:"concurrency,omitempty"`
	ToleratedFailures int             `json:"toleratedFailures,omitempty"`
	StateStatus       workflow.Status `json:"stateStatus,omitempty"`
	StateStart        time.Time       `json:"stateStart,omitempty"`
	StateEnd          time.Time       `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type checksEntry struct {
	PK          string          `json:"pk,omitempty"`
	Type        Type            `json:"type,omitempty"`
	ID          uuid.UUID       `json:"id,omitempty"`
	Key         uuid.UUID       `json:"key,omitempty"`
	PlanID      uuid.UUID       `json:"planID,omitempty"`
	Actions     []uuid.UUID     `json:"actions,omitempty"`
	Delay       time.Duration   `json:"delay,omitempty"`
	StateStatus workflow.Status `json:"stateStatus,omitempty"`
	StateStart  time.Time       `json:"stateStart,omitempty"`
	StateEnd    time.Time       `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type sequencesEntry struct {
	PK          string          `json:"pk,omitempty"`
	Type        Type            `json:"type,omitempty"`
	ID          uuid.UUID       `json:"id,omitempty"`
	Key         uuid.UUID       `json:"key,omitempty"`
	PlanID      uuid.UUID       `json:"planID,omitempty"`
	Name        string          `json:"name,omitempty"`
	Descr       string          `json:"descr,omitempty"`
	Pos         int             `json:"pos,omitempty"`
	Actions     []uuid.UUID     `json:"actions,omitempty"`
	StateStatus workflow.Status `json:"stateStatus,omitempty"`
	StateStart  time.Time       `json:"stateStart,omitempty"`
	StateEnd    time.Time       `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type actionsEntry struct {
	PK          string          `json:"pk,omitempty"`
	Type        Type            `json:"type,omitempty"`
	ID          uuid.UUID       `json:"id,omitempty"`
	Key         uuid.UUID       `json:"key,omitempty"`
	PlanID      uuid.UUID       `json:"planID,omitempty"`
	Name        string          `json:"name,omitempty"`
	Descr       string          `json:"descr,omitempty"`
	Pos         int             `json:"pos,omitempty"`
	Plugin      string          `json:"plugin,omitempty"`
	Timeout     time.Duration   `json:"timeout,omitempty"`
	Retries     int             `json:"retries,omitempty"`
	Req         []byte          `json:"req,omitempty"`
	Attempts    []byte          `json:"attempts,omitempty"`
	StateStatus workflow.Status `json:"stateStatus,omitempty"`
	StateStart  time.Time       `json:"stateStart,omitempty"`
	StateEnd    time.Time       `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}
