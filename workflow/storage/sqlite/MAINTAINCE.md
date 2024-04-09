# SQLITE STRUCTURE

## File structure

### Reading

- `reader.go` contains the `reader` struct.
- `stmts.go` contains all the SQL statements used to query the database.
- `schema.go` contains the schema for the database.
- `reader_actions.go` contains the methods to convert the `$actions` field to `Action` objects.
- `reader_blocks.go` contains the methods to convert the `$blocks` field to `Block` objects.
- `reader_checks.go` contains the methods to convert the `$pre_checks`, `$post_checks`, and `$cont_checks` fields to `Checks` objects.
- `reader_plans.go` contains the methods to convert to locate a Plan in SQLITE by its ID and convert it to a `Plan` objects.
- `reader_sequences.go` contains the methods to convert the `$sequences` field to `Sequence` objects.

### Writing

- `creator.go` contains the `creator` struct.
- `creator_plan.go` contains a function for committing an entire `*workflow.Plan` object to the database.

### Updating

- `updater.go` contains the `updater` struct.
- `updater_actions.go` contains the `actionUpdater` struct and methods to update the `Action` object in the database.
- `updater_blocks.go` contains the `blockUpdater` struct and methods to update the `Block` object in the database.
- `updater_checks.go` contains the `checkUpdater` struct and methods to update the `Checks` object in the database.
- `updater_sequences.go` contains the `sequenceUpdater` struct and methods to update the `Sequence` object in the database.
- `updater_stmts.go` contains the SQL statements used to update the database.

## Reader

`*storage.Reader` is implemented by `reader`.

## Read

`Read` is a fairly complicated process that involves reading the plan from the database and then creating the plan object. The plan object is a `*workflow.Plan` object. The plan object is then returned to the caller.

This works by calling `fetchPlan` which reads the plan from the database. This grabs all the plan fields, however any field that would contain another `struct` either stores the object's ID as string uuid.UUIDv7 or a JSON encoded list of the uuid.UUIDv7 objects. This is because SQLITE does not support arrays.

There is a generic `fieldToCheck` method that will convert a field name to a check object. This is used to convert the `PreCheck`, `PostCheck`, and `ContCheck` fields to their respective check objects.

All sub-objects are converted in a similar way with a `fieldTo` method. This is used to convert the `Blocks`, `Sequences` and `Actions` fields to their respective objects.

Each of these methods are contained in their own sub files:

- `reader_actions.go`
- `reader_blocks.go`
- `reader_checks.go`
- `reader_plans.go`
- `reader_sequences.go`

## Encoding Notes

### Attempts

`Action.Attempts` are stored as a JSON `[][]byte`. This is because SQLITE does not support arrays. Because we don't know the `Resp` type, we can't use a `json.Marshaler` to encode the `Attempts` field. This would lead to a `map[string]any` instead of the specific type the user expected.

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
