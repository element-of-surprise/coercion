# COSMOSDB STRUCTURE

## File structure

### Reading

- `reader.go` contains the `reader` struct.
- `schema.go` contains the schema for the database.
- `reader_actions.go` contains the methods to convert the `actions` document type to `Action` objects.
- `reader_blocks.go` contains the methods to convert the `blocks` document type to `Block` objects.
- `reader_checks.go` contains the methods to convert the `checks` document types to `Checks` objects.
- `reader_plans.go` contains the methods to convert to locate a Plan in a CosmosDB container by its ID and convert it to a `Plan` object.
- `reader_sequences.go` contains the methods to convert the `sequences` document type to `Sequence` objects.

### Writing

- `creator.go` contains the `creator` struct.
- `creator_plan.go` contains a function for committing an entire `*workflow.Plan` object to the database.

### Updating

- `updater.go` contains the `updater` struct.
- `updater_actions.go` contains the `actionUpdater` struct and methods to update the `Action` object in the database.
- `updater_blocks.go` contains the `blockUpdater` struct and methods to update the `Block` object in the database.
- `updater_checks.go` contains the `checkUpdater` struct and methods to update the `Checks` object in the database.
- `updater_sequences.go` contains the `sequenceUpdater` struct and methods to update the `Sequence` object in the database.

## Reader

`*storage.Reader` is implemented by `reader`.

## Read

`Read` is a fairly complicated process that involves reading the plan from the database and then creating the plan object. The plan object is a `*workflow.Plan` object. The plan object is then returned to the caller.

This works by calling `fetchPlan` which reads the plan from the database. This grabs all the plan fields, however any field that would contain another `struct` either stores the object's ID as string uuid.UUIDv7 or a list of the uuid.UUIDv7 objects. This is because these arrays are technically unbounded so normalization is used here, even though CosmosDB is a NoSQL database.

There is a generic `fieldToCheck` method that will convert a field name to a check object. This is used to convert the `BypassCheck`, `PreCheck`, `PostCheck`, `ContCheck`, and `DeferredCheck` fields to their respective check objects.

All sub-objects are converted in a similar way with a `fieldTo` method. This is used to convert the `Blocks`, `Sequences` and `Actions` fields to their respective objects.

Each of these methods are contained in their own sub files:

- `reader_actions.go`
- `reader_blocks.go`
- `reader_checks.go`
- `reader_plans.go`
- `reader_sequences.go`

## Encoding Notes

### Attempts

`Action.Attempts` are stored as a JSON `[][]byte`. Because we don't know the `Resp` type, we can't use a `json.Marshaler` to encode the `Attempts` field. This would lead to a `map[string]any` instead of the specific type the user expected.

To fix this, because we decode initially into a `[][]byte`, we can create the slice of `Attempts` like so: `attempts := make([]*Attempts, len(rawAttempts))`. Then we can do the following:

```go
for _, a := range attempts {
	a.Resp = plugin.Resp()
}

for _, r := range rawAttempts {
	if err := json.Unmarshal(r, &attempts); err != nil {
		return nil, err
	}
}
```

This populates our `Resp` field with the correct type. The normal `encoding/json` package would replace the `Resp` field with a `map[string]any` type. But we use the `github.com/go-json-experiment` package, which handles this correctly. This package is likely to become `encoding/json/v2`.

# Integration testing 

```
export AZURE_COSMOSDB_DBNAME="dbname"
export AZURE_COSMOSDB_CNAME="underlaycx1"
export AZURE_COSMOSDB_PK="resourceid"
```

go run testing/integration.go --teardown=<false/true>

To allow authentication via az login, you need the following comosdb sql roles:
https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/security/how-to-grant-data-plane-role-based-access?tabs=built-in-definition%2Ccsharp&pivots=azure-interface-cli

In production, use an Azure identity.
