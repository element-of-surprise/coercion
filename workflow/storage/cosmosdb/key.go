package cosmosdb

import (
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/google/uuid"
)

// key returns the partition key given the key value as either a uuid.UUID, *workflow.Plan, or string.
func key(v any) azcosmos.PartitionKey {
	switch x := v.(type) {
	case uuid.UUID:
		return azcosmos.NewPartitionKeyString(x.String())
	case *workflow.Plan:
		return azcosmos.NewPartitionKeyString(x.ID.String())
	case string:
		return azcosmos.NewPartitionKeyString(x)
	}
	panic("unknown type")
}

// key returns the partition key as a string given the key value as either a uuid.UUID, *workflow.Plan, or string.
func keyStr(v any) string {
	switch x := v.(type) {
	case uuid.UUID:
		return x.String()
	case *workflow.Plan:
		return x.GetID().String()
	case string:
		return x
	}
	panic("unknown type")
}
