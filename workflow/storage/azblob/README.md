# Azure Blob Storage Backend for Workflow Plans

This package provides an Azure Blob Storage implementation of the `storage.Vault` interface for persisting workflow plans.

## Overview

The azblob storage backend organizes workflow data in Azure Blob Storage using:
- **Daily containers** for time-based organization (`<prefix>-YYYY-MM-DD`)
- **Hierarchical blob structure** for different object types
- **Blob metadata** for efficient listing and searching
- **Two-blob plan storage** (entry + object) for optimized updates
- **Recovery support** for handling incomplete plans

## Architecture

### Container Strategy

Containers are created based on Plan ID timestamps (UUID v7):
```
<prefix>-2025-10-21
<prefix>-2025-10-22
...
```

The container name is derived from the Plan's UUID timestamp, ensuring:
- All plan data and sub-objects are in the same container
- Time-based data organization
- Easy retention policy implementation
- Reduced blast radius for issues
- No multi-container searches needed for operations

### Blob Organization

```
<container>/
  plans/<plan-id>-entry.json              # Lightweight plan entry (IDs only) with metadata
  plans/<plan-id>-object.json             # Full plan with embedded hierarchy
  blocks/<plan-id>/<block-id>.json        # Individual block data
  sequences/<plan-id>/<sequence-id>.json  # Individual sequence data
  checks/<plan-id>/<checks-id>.json       # Individual checks data
  actions/<plan-id>/<action-id>.json      # Individual action data
```

#### Plan Storage Strategy

Plans are stored using a two-blob approach:

1. **planEntry blob** (`-entry.json`):
   - Lightweight structure containing only IDs of sub-objects
   - Written first before everything else
   - Contains metadata for efficient listing
   - Updated during plan execution

2. **Plan object blob** (`-object.json`):
   - Full workflow.Plan with complete embedded hierarchy
   - Written last after all sub-objects
   - Used for non-running plans
   - Updated when the plan completes (but not during execution)

**Write Order**: `planEntry → sub-objects → Plan object`

**Recovery Rule**: If planEntry exists but Plan object doesn't, the planEntry is deleted (incomplete write).

### Blob Metadata

PlanEntry blobs have metadata for efficient listing:
- `PlanID`: Plan ID UUID
- `GroupID`: Group ID UUID (if present)
- `Name`: Plan name
- `Descr`: Plan description
- `SubmitTime`: Plan submission time (RFC3339Nano)
- `State`: JSON-encoded plan state

## Usage

### Basic Setup

```go
package main

import (
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/element-of-surprise/coercion/plugins/registry"
    "github.com/element-of-surprise/coercion/workflow/storage/azblob"
    "github.com/gostdlib/base/context"
)

func main() {
    ctx := context.Background()

    // Create Azure credential
    cred, err := azidentity.NewDefaultAzureCredential(nil)
    if err != nil {
        panic(err)
    }

    // Create plugin registry
    reg := registry.New()

    // Create vault
    vault, err := azblob.New(
        ctx,
        "my-cluster",                                    // prefix
        "https://mystorageaccount.blob.core.windows.net", // endpoint
        cred,                                             // credential
        reg,                                              // registry
    )
    if err != nil {
        panic(err)
    }
    defer vault.Close(ctx)

    // Use vault...
}
```

### Creating a Plan

```go
plan := &workflow.Plan{
    Name:  "My Workflow",
    Descr: "Example workflow",
    Blocks: []*workflow.Block{
        // ... blocks
    },
}

// Create plan in storage
if err := vault.Create(ctx, plan); err != nil {
    log.Fatalf("Failed to create plan: %v", err)
}
```

### Reading a Plan

```go
planID := uuid.MustParse("...")

plan, err := vault.Read(ctx, planID)
if err != nil {
    log.Fatalf("Failed to read plan: %v", err)
}
```

### Searching Plans

```go
filters := storage.Filters{
    ByStatus: []workflow.Status{workflow.Running},
}

resultsCh, err := vault.Search(ctx, filters)
if err != nil {
    log.Fatalf("Failed to search: %v", err)
}

for result := range resultsCh {
    if result.Err != nil {
        log.Printf("Error: %v", result.Err)
        continue
    }
    log.Printf("Found plan: %s", result.Result.Name)
}
```

### Listing Plans

```go
limit := 10
resultsCh, err := vault.List(ctx, limit)
if err != nil {
    log.Fatalf("Failed to list: %v", err)
}

for result := range resultsCh {
    if result.Err != nil {
        log.Printf("Error: %v", result.Err)
        continue
    }
    log.Printf("Plan: %s (submitted: %v)", result.Result.Name, result.Result.SubmitTime)
}
```

### Updating a Plan

```go
plan.State.Status = workflow.Running

if err := vault.UpdatePlan(ctx, plan); err != nil {
    log.Fatalf("Failed to update plan: %v", err)
}
```

**Note**: During execution (when running), `UpdatePlan` only updates the planEntry blob (lightweight, IDs only) and its metadata. When the plan completes, the full Plan object blob is updated with the final state.

### Deleting a Plan

```go
if err := vault.Delete(ctx, planID); err != nil {
    log.Fatalf("Failed to delete plan: %v", err)
}
```

### Recovery

Recovery can be triggered to repair incomplete plans (e.g., after a crash):

```go
if err := vault.Recovery(ctx); err != nil {
    log.Fatalf("Recovery failed: %v", err)
}
```

Recovery handles incomplete writes:
1. Scans for planEntry blobs without corresponding Plan object blobs
2. Deletes orphaned planEntry blobs (incomplete writes)
3. For complete plans, verifies consistency
4. Skips running plans (as they're actively being updated)

**Note**: If a planEntry exists but the Plan object blob doesn't, this indicates an incomplete write. The recovery process will delete the planEntry to maintain consistency.

## Configuration

### Authentication

The package supports all Azure authentication methods via `azcore.TokenCredential`:

```go
// Default credential chain
cred, _ := azidentity.NewDefaultAzureCredential(nil)

// Managed identity
cred, _ := azidentity.NewManagedIdentityCredential(nil)

// Service principal
cred, _ := azidentity.NewClientSecretCredential(tenantID, clientID, secret, nil)
```

### Storage Account

Endpoint format:
```
https://<storage-account-name>.blob.core.windows.net
```

### Prefix

The prefix is used in container names and should identify your cluster/instance:
```
my-cluster-2025-10-21
production-2025-10-21
staging-abc123-2025-10-21
```

## Performance Considerations

### Search and Listing

The implementation uses blob metadata for listing and filtering:
- Metadata is retrieved with blob properties (no extra request)
- Filtering happens client-side after listing
- Consider using specific time ranges when querying historical data

### Update Performance

The two-blob approach optimizes update performance:
- **During execution**: Only planEntry (lightweight) is updated
- **After completion**: Plan object blob is updated with final state
- Reduces write overhead for frequent state updates during execution

### Concurrency

The implementation uses local mutex-based locking. For distributed deployments, consider:
- Using separate prefixes for each node
- Implementing distributed locking if needed

### Container Locality

Plans and their sub-objects are co-located in the same container (based on Plan ID timestamp):
- No multi-container searches for reads/updates/deletes
- Better cache locality
- Simpler cleanup operations

### Retention

Old containers can be cleaned up using Azure Blob lifecycle management policies or custom cleanup jobs.

## Error Handling

The package uses exponential backoff for transient errors:
- HTTP 408, 429, 500, 502, 503, 504
- Azure Blob errors: ServerBusy, InternalError, OperationTimedOut

Permanent errors (e.g., not found, authentication) fail immediately.

## Testing

### Unit Tests

Run unit tests:
```bash
go test -v
```

With coverage:
```bash
go test -cover
```

### Integration Tests

Integration tests require a real Azure Storage account or emulator.


Run base storage integration tests:
```bash
cd storage/azblob/integration
go run . -endpoint https://[storage account].blob.core.windows.net/
```

Run service based integration tests:
```bash
cd internal/etoe
go run . --azblobURL https://[storage account].blob.core.windows.net/
```

## Limitations

1. **No true transactions**: Azure Blob doesn't support ACID transactions. The implementation uses write ordering (planEntry first, Plan object last) and recovery to handle incomplete writes.

2. **Search performance**: Currently uses list+filter with metadata. For large datasets, consider time-based filtering or separate indexing.

3. **Local locking only**: Uses local mutex, not distributed locks.

4. **Manual container cleanup**: Old containers must be cleaned up externally.

## Development

### Project Structure

```
azblob/
├── azblob.go           # Main Vault struct and constructor
├── key.go              # Blob naming helpers
├── schema.go           # Entry types and conversions
├── errors.go           # Error handling utilities
├── creator.go          # Create operations
├── creator_plan.go     # Plan creation logic
├── reader.go           # Read, Search, List operations
├── reader_plan.go      # Plan reconstruction
├── updater.go          # Update operations
├── deleter.go          # Delete operations
├── closer.go           # Close operations
├── recovery.go         # Recovery operations
└── *_test.go           # Unit tests
```

### Adding New Features

When adding new object types or fields:

1. Update `schema.go` entry types
2. Update conversion functions
3. Update blob naming in `key.go`
4. Update creator/reader/updater logic
5. Add tests

## Troubleshooting

### Plan not found

Check:
- Plan exists in expected container (today or yesterday)
- Blob name format is correct: `plans/<uuid>.json`
- Authentication is working

### Slow searches

- Search currently uses list+filter
- Consider filtering on fewer plans or using more specific filters
- Future: optimize with native blob index tag queries when SDK supports it

### Recovery not working

Check:
- Plan is not currently running (recovery skips running plans)
- Plan blob exists and is readable
- Permissions allow creating blobs

## License

See repository root for license information.

## Contributing

See repository CONTRIBUTING.md for guidelines.
