package azblob

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
	"golang.org/x/sync/singleflight"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

var _ storage.Reader = reader{}

// reader implements the storage.Reader interface.
type reader struct {
	mu            *planlocks.Group
	readFlight    *singleflight.Group
	existsFlight  *singleflight.Group
	prefix        string
	client        blobops.Ops
	reg           *registry.Register
	retentionDays int
	nowf          func() time.Time

	testListPlansInContainer func(ctx context.Context, containerName string) ([]storage.ListResult, error)

	private.Storage
}

func (r reader) now() time.Time {
	if r.nowf == nil {
		return time.Now()
	}
	return r.nowf()
}

// Exists implements storage.Reader.Exists(). It returns true if the plan exists.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	r.mu.RLock(id)
	defer r.mu.RUnlock(id)

	v, err, _ := r.existsFlight.Do(
		id.String(),
		func() (any, error) {
			return r.exists(ctx, id)
		},
	)
	if err != nil {
		return false, err
	}
	return v.(bool), nil
}

func (r reader) exists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := r.fetchPlanEntryMeta(ctx, id)
	if err == nil {
		return true, nil
	}

	if blobops.IsNotFound(err) {
		return false, nil
	}
	return false, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to check plan existence: %w", err))
}

// Read implements storage.Reader.Read().
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	// We have a retention time in blob storage because of unique(read pain in the ass) way of
	// doing storage. So even if something is in our storage still, if it is past retention we just
	// say it is not.
	if time.Unix(id.Time().UnixTime()).Before(r.now().AddDate(0, 0, -r.retentionDays)) {
		return nil, &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound), RawResponse: &http.Response{}}
	}

	r.mu.RLock(id)
	defer r.mu.RUnlock(id)

	v, err, _ := r.readFlight.Do(
		id.String(),
		func() (any, error) {
			return r.fetchPlan(ctx, id)
		},
	)
	if err != nil {
		return nil, err
	}

	return v.(*workflow.Plan), nil
}

// ReadDirect reads a plan from storage bypassing the retention check.
// This is intended for testing purposes only.
func (r reader) ReadDirect(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	if !testing.Testing() {
		panic("ReadDirect is only for testing")
	}

	r.mu.RLock(id)
	defer r.mu.RUnlock(id)

	return r.fetchPlan(ctx, id)
}

// Search implements storage.Reader.Search().
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
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

	containers := searchContainerNames(r.prefix, r.retentionDays)

	var results []storage.ListResult

	for _, containerName := range containers {
		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !blobops.IsNotFound(err) {
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
		if !slices.Contains(filters.ByStatus, result.State.Status) {
			return false
		}
	}

	return true
}

// list lists all plans, most recent first.
func (r reader) list(ctx context.Context, limit int, ch chan storage.Stream[storage.ListResult]) {
	containers := searchContainerNames(r.prefix, r.retentionDays)

	var results []storage.ListResult
	count := 0

	for _, containerName := range containers {
		if limit > 0 && count >= limit {
			break
		}

		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !blobops.IsNotFound(err) {
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
	if r.testListPlansInContainer != nil && testing.Testing() {
		return r.testListPlansInContainer(ctx, containerName)
	}

	var results []storage.ListResult

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
			if v := blob.Metadata[mdPlanType]; v == nil || *v != ptEntry {
				continue
			}

			p := blob.Metadata[mdKeyPlanID]
			if p == nil {
				context.Log(ctx).Error(fmt.Sprintf("could not read planID metatadata for entry(%s)", *blob.Name))
				continue
			}
			pm, err := mapToPlanMeta(blob.Metadata)
			if err != nil {
				context.Log(ctx).Error(fmt.Sprintf("could not parse plan metadata for entry(%s): %v", *blob.Name, err))
				continue
			}

			results = append(results, pm.ListResult)
		}
	}

	return results, nil
}
