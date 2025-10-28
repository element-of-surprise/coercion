# End to end tests

## Running

### Sqlite

`go test .`

This will also run recovery tests. These tests validate that our recovery of objects in inconsistent states work. This test is valid no matter what the storage is.

### CosmosDB

go test . -vault=cosmosdb -collection-name="[a collection name]" -db-name="[your db name]" -container-name="[choose one]" -cosmos_url="[the cosmos url]"

### Azblob

go test . -vault=azblob -azblob_url=[https://the usl]
