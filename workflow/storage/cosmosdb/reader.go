package cosmosdb

import (
	"fmt"
	"strings"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

const (
	// beginning of query to list plans with a filter
	searchPlans = `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm`
	// list all plans without parameters
	listPlans = `SELECT c.id, c.groupID, c.name, c.descr, c.submitTime, c.stateStatus, c.stateStart, c.stateEnd FROM c WHERE c.swarm=@swarm ORDER BY c.submitTime DESC`
)

// readerClient provides abstraction for testing reader. This is implmented by *azcosmos.ContainerClient.
type readerClient interface {
	ReadItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
	NewQueryItemsPager(query string, partitionKey azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse]
}

// reader implements the storage.Reader interface.
type reader struct {
	mu           *sync.RWMutex
	swarm        string
	container    string
	client       readerClient // *azcosmos.ContainerClient
	defaultIOpts *azcosmos.ItemOptions

	reg *registry.Register

	private.Storage
}

// sender is a helper that sends value v on channel ch or returns an error because ctx.Done() fires.
func sender[T any](ctx context.Context, ch chan T, v T) error {
	select {
	case <-ctx.Done():
		return errors.E(ctx, errors.CatInternal, errors.TypeTimeout, context.Cause(ctx))
	case ch <- v:
		return nil
	}
}

// Exists returns true if the Plan ID exists in the storage.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	idStr := id.String()

	_, err := r.client.ReadItem(ctx, key(id), idStr, r.defaultIOpts)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("couldn't fetch plan by id: %w", err))
	}
	return true, nil
}

// Read returns a Plan from the storage.
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var plan *workflow.Plan
	var err error
	fetchPlan := func(ctx context.Context, rec exponential.Record) error {
		plan, err = r.fetchPlan(ctx, id)
		if err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), fetchPlan); err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to fetch plan: %w", err))
	}
	return plan, nil
}

const searchKeyStr = "planSearch"

var searchKey = azcosmos.NewPartitionKeyString(searchKeyStr)

// Search returns a list of Plan IDs that match the filter.
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	q, parameters := r.buildSearchQuery(filters)

	r.mu.RLock()
	defer r.mu.RUnlock()

	pager := r.client.NewQueryItemsPager(q, searchKey, &azcosmos.QueryOptions{QueryParameters: parameters})
	results := make(chan storage.Stream[storage.ListResult], 1)

	context.Pool(ctx).Submit(
		ctx,
		func() {
			defer close(results)
			for pager.More() {
				res, err := pager.NextPage(ctx)
				if err != nil {
					err := errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("problem listing plans: %w", err))
					sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: err})
					return
				}
				for _, item := range res.Items {
					result, err := r.listResultsFunc(item)
					if err != nil {
						err := errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("problem listing items in plans: %w", err))
						sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: err})
						return
					}
					if err := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Result: result}); err != nil {
						return
					}
				}
			}
		},
	)
	return results, nil
}

func (r reader) buildSearchQuery(filters storage.Filters) (string, []azcosmos.QueryParameter) {
	parameters := []azcosmos.QueryParameter{
		{Name: "@swarm", Value: r.swarm},
	}

	build := strings.Builder{}
	build.WriteString(searchPlans)

	numFilters := 0

	if len(filters.ByIDs) > 0 {
		numFilters++
		build.WriteString(" AND ARRAY_CONTAINS(@ids, c.id)")
	}
	if len(filters.ByGroupIDs) > 0 {
		numFilters++
		build.WriteString(" AND ARRAY_CONTAINS(@group_ids, c.groupID)")
	}
	if len(filters.ByStatus) > 0 {
		build.WriteString(" AND ")
		if len(filters.ByStatus) > 1 {
			build.WriteString("(")
		}
		numFilters++ // I know this says inEffectual assignment and it is, but it is here for completeness.
		for i, s := range filters.ByStatus {
			name := fmt.Sprintf("@status%d", i)
			if i == 0 {
				build.WriteString(fmt.Sprintf("c.stateStatus = %s", name))
			} else {
				build.WriteString(fmt.Sprintf(" OR c.stateStatus = %s", name))
			}
			parameters = append(parameters, azcosmos.QueryParameter{
				Name:  name,
				Value: int64(s),
			})
		}
		if len(filters.ByStatus) > 1 {
			build.WriteString(")")
		}
	}

	build.WriteString(" ORDER BY c.submitTime DESC")
	query := build.String()

	if len(filters.ByIDs) > 0 {
		parameters = append(parameters, azcosmos.QueryParameter{
			Name:  "@ids",
			Value: filters.ByIDs,
		})
	}
	if len(filters.ByGroupIDs) > 0 {
		parameters = append(parameters, azcosmos.QueryParameter{
			Name:  "@group_ids",
			Value: filters.ByGroupIDs,
		})
	}
	return query, parameters
}

// List returns a list of Plan IDs in the storage in order from newest to oldest. This should
// return with most recent submitted first. limit sets the maximum number of entries to return. If
// limit == 0, there is no limit.
func (r reader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	parameters := []azcosmos.QueryParameter{
		{Name: "@swarm", Value: r.swarm},
	}
	q := listPlans
	if limit > 0 {
		q += " OFFSET 0 LIMIT @limit"
		parameters = append(parameters, azcosmos.QueryParameter{Name: "@limit", Value: limit})
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	pager := r.client.NewQueryItemsPager(q, searchKey, &azcosmos.QueryOptions{QueryParameters: parameters})
	results := make(chan storage.Stream[storage.ListResult], 1)

	context.Pool(ctx).Submit(
		ctx,
		func() {
			defer close(results)
			for pager.More() {
				res, err := pager.NextPage(ctx)
				if err != nil {
					sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing plans: %w", err)})
					return
				}
				for _, item := range res.Items {
					result, err := r.listResultsFunc(item)
					if err != nil {
						sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing items in plans: %w", err)})
						return
					}
					if err := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Result: result}); err != nil {
						return
					}
				}
			}
		},
	)
	return results, nil
}

// listResultsFunc is a helper function to convert a CosmosDB document into a ListResult.
func (r reader) listResultsFunc(item []byte) (storage.ListResult, error) {
	var err error
	var resp searchEntry
	if err = json.Unmarshal(item, &resp); err != nil {
		return storage.ListResult{}, err
	}

	result := storage.ListResult{
		ID:         resp.ID,
		GroupID:    resp.GroupID,
		Name:       resp.Name,
		Descr:      resp.Descr,
		SubmitTime: resp.SubmitTime,
		State: workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
		},
	}
	return result, nil
}
