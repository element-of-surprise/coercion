package cosmosdb

import (
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

type plansEntry struct {
	PartitionKey string              `json:"partitionKey"`
	Collection   string              `json:"collection"`
	Type         workflow.ObjectType `json:"type,omitempty"`
	ID           uuid.UUID           `json:"id,omitempty"`
	// PlanID is the unique identifier for the plan. This is a duplicate of ID. All other items have ID and PlanID.
	// While a plan technically doesn't need both, this fits well into the model. While it can be worked around,
	// it causes all kinds of subtle bugs. By having both it makes everything easier.
	PlanID         uuid.UUID              `json:"planID,omitempty"`
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
	PartitionKey      string              `json:"partitionKey"`
	Collection        string              `json:"collection"`
	Type              workflow.ObjectType `json:"type,omitempty"`
	ID                uuid.UUID           `json:"id,omitempty"`
	Key               uuid.UUID           `json:"key,omitempty"`
	PlanID            uuid.UUID           `json:"planID,omitempty"`
	Name              string              `json:"name,omitempty"`
	Descr             string              `json:"descr,omitempty"`
	Pos               int                 `json:"pos,omitempty"`
	EntranceDelay     time.Duration       `json:"entranceDelay,omitempty"`
	ExitDelay         time.Duration       `json:"exitDelay,omitempty"`
	BypassChecks      uuid.UUID           `json:"bypassChecks,omitempty"`
	PreChecks         uuid.UUID           `json:"preChecks,omitempty"`
	PostChecks        uuid.UUID           `json:"postChecks,omitempty"`
	ContChecks        uuid.UUID           `json:"contChecks,omitempty"`
	DeferredChecks    uuid.UUID           `json:"deferredChecks,omitempty"`
	Sequences         []uuid.UUID         `json:"sequences,omitempty"`
	Concurrency       int                 `json:"concurrency,omitempty"`
	ToleratedFailures int                 `json:"toleratedFailures,omitempty"`
	StateStatus       workflow.Status     `json:"stateStatus,omitempty"`
	StateStart        time.Time           `json:"stateStart,omitempty"`
	StateEnd          time.Time           `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type checksEntry struct {
	PartitionKey string              `json:"partitionKey"`
	Collection   string              `json:"collection"`
	Type         workflow.ObjectType `json:"type,omitempty"`
	ID           uuid.UUID           `json:"id,omitempty"`
	Key          uuid.UUID           `json:"key,omitempty"`
	PlanID       uuid.UUID           `json:"planID,omitempty"`
	Actions      []uuid.UUID         `json:"actions,omitempty"`
	Delay        time.Duration       `json:"delay,omitempty"`
	StateStatus  workflow.Status     `json:"stateStatus,omitempty"`
	StateStart   time.Time           `json:"stateStart,omitempty"`
	StateEnd     time.Time           `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type sequencesEntry struct {
	PartitionKey string              `json:"partitionKey"`
	Collection   string              `json:"collection"`
	Type         workflow.ObjectType `json:"type,omitempty"`
	ID           uuid.UUID           `json:"id,omitempty"`
	Key          uuid.UUID           `json:"key,omitempty"`
	PlanID       uuid.UUID           `json:"planID,omitempty"`
	Name         string              `json:"name,omitempty"`
	Descr        string              `json:"descr,omitempty"`
	Pos          int                 `json:"pos,omitempty"`
	Actions      []uuid.UUID         `json:"actions,omitempty"`
	StateStatus  workflow.Status     `json:"stateStatus,omitempty"`
	StateStart   time.Time           `json:"stateStart,omitempty"`
	StateEnd     time.Time           `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

type actionsEntry struct {
	PartitionKey string              `json:"partitionKey"`
	Collection   string              `json:"collection"`
	Type         workflow.ObjectType `json:"type,omitempty"`
	ID           uuid.UUID           `json:"id,omitempty"`
	Key          uuid.UUID           `json:"key,omitempty"`
	PlanID       uuid.UUID           `json:"planID,omitempty"`
	Name         string              `json:"name,omitempty"`
	Descr        string              `json:"descr,omitempty"`
	Pos          int                 `json:"pos,omitempty"`
	Plugin       string              `json:"plugin,omitempty"`
	Timeout      time.Duration       `json:"timeout,omitempty"`
	Retries      int                 `json:"retries,omitempty"`
	Req          []byte              `json:"req,omitempty"`
	Attempts     []byte              `json:"attempts,omitempty"`
	StateStatus  workflow.Status     `json:"stateStatus,omitempty"`
	StateStart   time.Time           `json:"stateStart,omitempty"`
	StateEnd     time.Time           `json:"stateEnd,omitempty"`

	ETag azcore.ETag `json:"_etag,omitempty"`
}

// ok, to deal with the fact that we can't do multiple partition keys in the go sdk, we are going to put all an index
// for searching into a single partition key. This will be the partition key will contain only the search index.
// Without any compression, about 500K entries make about 109MiB. So this should hold us for a while, especially if we are
// going to do 30/60/90 day retention.
type searchEntry struct {
	PartitionKey string          `json:"partitionKey"`
	Collection   string          `json:"collection"`
	Name         string          `json:"name,omitempty"`
	Descr        string          `json:"descr,omitempty"`
	ID           uuid.UUID       `json:"id,omitempty"`
	GroupID      uuid.UUID       `json:"groupID,omitempty"`
	SubmitTime   time.Time       `json:"submitTime,omitempty"`
	StateStatus  workflow.Status `json:"stateStatus,omitempty"`
	StateStart   time.Time       `json:"stateStart,omitempty"`
	StateEnd     time.Time       `json:"stateEnd,omitempty"`
}
