package cosmosdb

import (
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// key returns the partition key given the key value as either a uuid.UUID, *workflow.Plan, or string.
func key(v any) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString("cosmosdbIsBad")

	// CosmosDB in its infinite wisdom, thinks that high availability is more important
	// being able to actually use the database. So, it doesn't support not using partition keys.
	// This means I have to use cosmos as a key value store, which is not what I want.
	// Because there weren't a bunch of way better products to do key/value with...........
	// Anyways, so until then we have to return a staic key.
	//
	// When we enable this one day in 2030, r.client.NewQueryItemsPager() in reader.Search() will need to
	// be updated to use the nil parition key and remove key().
	/*
		switch x := v.(type) {
		case uuid.UUID:
			return azcosmos.NewPartitionKeyString(x.String())
		case *workflow.Plan:
			return azcosmos.NewPartitionKeyString(x.ID.String())
		case string:
			return azcosmos.NewPartitionKeyString(x)
		}
		panic("unknown type")
	*/
}

// key returns the partition key as a string given the key value as either a uuid.UUID, *workflow.Plan, or string.
func keyStr(v any) string {
	return "cosmosdbIsBad"

	// CosmosDB in its infinite wisdom, thinks that high availability is more important
	// being able to actually use the database. So, it doesn't support not using partition keys.
	// This means I have to use cosmos as a key value store, which is not what I want.
	// Because there weren't a bunch of way better products to do key/value with...........
	// Anyways, so until then we have to return a staic key.
	//
	// When we enable this one day in 2030, r.client.NewQueryItemsPager() in reader.Search() will need to
	// be updated to use the nil parition key and remove key().
	/*
		switch x := v.(type) {
		case uuid.UUID:
			return azcosmos.NewPartitionKeyString(x.String())
		case *workflow.Plan:
			return azcosmos.NewPartitionKeyString(x.ID.String())
		case string:
			return azcosmos.NewPartitionKeyString(x)
		}
		panic("unknown type")
	*/
}
