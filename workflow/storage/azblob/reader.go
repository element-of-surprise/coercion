package azblob

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/go-json-experiment/json"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.Reader = reader{}

// reader implements the storage.Reader interface.
type reader struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string
	reg      *registry.Register

	private.Storage
}

// Exists implements storage.Reader.Exists(). It returns true if the plan exists.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get the container for this plan based on its ID
	containerName := containerForPlan(r.prefix, id)
	blobName := planEntryBlobName(id)
	blobClient := r.client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)

	_, err := blobClient.GetProperties(ctx, nil)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to check plan existence: %w", err))
}

// Read implements storage.Reader.Read().
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.fetchPlan(ctx, id)
}

// Search implements storage.Reader.Search().
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if err := filters.Validate(); err != nil {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	ch := make(chan storage.Stream[storage.ListResult], 1)

	go func() {
		defer close(ch)
		r.search(ctx, filters, ch)
	}()

	return ch, nil
}

// List implements storage.Reader.List(). It lists recent plans, most recent first.
func (r reader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ch := make(chan storage.Stream[storage.ListResult], 1)

	go func() {
		defer close(ch)
		r.list(ctx, limit, ch)
	}()

	return ch, nil
}

// search performs the actual search using blob index tags.
func (r reader) search(ctx context.Context, filters storage.Filters, ch chan storage.Stream[storage.ListResult]) {
	// For now, implement search by listing all plans and filtering
	// TODO: When Azure Blob Index Tags search API is available in the SDK, use it for better performance

	// Get all containers to search
	containers := containerNames(r.prefix)

	// Collect all results first for filtering
	var results []storage.ListResult

	for _, containerName := range containers {
		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !isNotFound(err) {
				select {
				case ch <- storage.Stream[storage.ListResult]{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			continue
		}

		results = append(results, containerResults...)
	}

	// Filter results based on filters
	for _, result := range results {
		if r.matchesFilters(result, filters) {
			select {
			case ch <- storage.Stream[storage.ListResult]{Result: result}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// matchesFilters checks if a result matches the given filters.
func (r reader) matchesFilters(result storage.ListResult, filters storage.Filters) bool {
	// Check ID filter
	if len(filters.ByIDs) > 0 {
		if !slices.Contains(filters.ByIDs, result.ID) {
			return false
		}
	}

	// Check GroupID filter
	if len(filters.ByGroupIDs) > 0 {
		if !slices.Contains(filters.ByGroupIDs, result.GroupID) {
			return false
		}
	}

	// Check Status filter
	if len(filters.ByStatus) > 0 {
		if result.State == nil {
			return false
		}

		if !slices.Contains(filters.ByStatus, result.State.Status) {
			return false
		}
	}

	return true
}

// list lists all plans, most recent first.
func (r reader) list(ctx context.Context, limit int, ch chan storage.Stream[storage.ListResult]) {
	// Get containers to search (most recent first)
	containers := containerNames(r.prefix)

	var results []storage.ListResult
	count := 0

	// Search containers in order
	for _, containerName := range containers {
		if limit > 0 && count >= limit {
			break
		}

		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !isNotFound(err) {
				select {
				case ch <- storage.Stream[storage.ListResult]{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			continue
		}

		results = append(results, containerResults...)
		count += len(containerResults)
	}

	// Sort by submit time (most recent first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].SubmitTime.After(results[j].SubmitTime)
	})

	// Apply limit if specified
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Send results
	for _, result := range results {
		select {
		case ch <- storage.Stream[storage.ListResult]{Result: result}:
		case <-ctx.Done():
			return
		}
	}
}

// listPlansInContainer lists all plans in a specific container.
func (r reader) listPlansInContainer(ctx context.Context, containerName string) ([]storage.ListResult, error) {
	var results []storage.ListResult

	// List blobs with the plans prefix
	pager := r.client.NewListBlobsFlatPager(containerName, &azblob.ListBlobsFlatOptions{
		Prefix:  toPtr(planBlobPrefix()),
		Include: container.ListBlobsInclude{Metadata: true},
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, err)
		}

		for _, blob := range page.Segment.BlobItems {
			if blob.Name == nil {
				continue
			}

			p := blob.Metadata[mdKeyPlanID]
			if p == nil {
				context.Log(ctx).Error(fmt.Sprintf("could not read planID metatadata for entry(%s)", *blob.Name))
				continue
			}
			lr, err := r.parsePlanMetadata(blob.Metadata)
			if err != nil {
				context.Log(ctx).Error(fmt.Sprintf("could not parse plan metadata for entry(%s): %v", *blob.Name, err))
				continue
			}

			results = append(results, lr)
		}
	}

	return results, nil
}

// parsePlanMetadata parses plan metadata from blob metadata.
func (r reader) parsePlanMetadata(md map[string]*string) (storage.ListResult, error) {
	sr := storage.ListResult{}

	planIDStr, ok := md[mdKeyPlanID]
	if !ok || planIDStr == nil {
		return storage.ListResult{}, fmt.Errorf("missing plan ID metadata")
	}
	planID, err := uuid.Parse(*planIDStr)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("invalid plan ID metadata: %w", err)
	}
	sr.ID = planID

	groupIDStr, ok := md[mdKeyGroupID]
	if ok && groupIDStr != nil {
		groupID, err := uuid.Parse(*groupIDStr)
		if err != nil {
			return storage.ListResult{}, fmt.Errorf("invalid group ID metadata: %w", err)
		}
		sr.GroupID = groupID
	}

	nameStr, ok := md[mdKeyName]
	if !ok || nameStr == nil {
		return storage.ListResult{}, fmt.Errorf("missing name metadata")
	}
	sr.Name = *nameStr

	descrStr, ok := md[mdKeyDescr]
	if !ok || descrStr == nil {
		return storage.ListResult{}, fmt.Errorf("missing description metadata")
	}
	sr.Descr = *descrStr

	submitTimeStr, ok := md[mdKeySubmitTime]
	if !ok || submitTimeStr == nil {
		return storage.ListResult{}, fmt.Errorf("missing submit time metadata")
	}
	submitTime, err := time.Parse(time.RFC3339Nano, *submitTimeStr)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("invalid submit time metadata: %w", err)
	}
	sr.SubmitTime = submitTime

	stateJSON, ok := md[mdKeyState]
	if !ok || stateJSON == nil {
		return storage.ListResult{}, fmt.Errorf("missing state metadata")
	}
	var state workflow.State
	if err := json.Unmarshal(strToBytes(*stateJSON), &state); err != nil {
		return storage.ListResult{}, fmt.Errorf("invalid state metadata: %w", err)
	}
	sr.State = &state
	return sr, nil
}
