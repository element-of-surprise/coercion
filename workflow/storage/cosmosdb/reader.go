package cosmosdb

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

const (
	// beginning of query to list plans with a filter
	searchPlans = `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM %s p WHERE p.type=1 AND`
	// list all plans without parameters
	listPlans = `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM %s p WHERE p.type=1 ORDER BY p.submitTime DESC`
)

// readerClient provides abstraction for testing reader. This is implmented by *azcosmos.ContainerClient.
type readerClient interface {
	ReadItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
	NewQueryItemsPager(query string, partitionKey azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse]
}

// reader implements the storage.PlanReader interface.
type reader struct {
	mu           *sync.RWMutex
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
		return context.Cause(ctx)
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
		return false, fmt.Errorf("couldn't fetch plan by id: %w", err)
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
		return nil, fmt.Errorf("failed to fetch plan: %w", err)
	}
	return plan, nil
}

// Search returns a list of Plan IDs that match the filter.
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	q, parameters := r.buildSearchQuery(filters)

	r.mu.RLock()
	defer r.mu.RUnlock()

	pager := r.client.NewQueryItemsPager(q, key("doesntmatterwhatiput"), &azcosmos.QueryOptions{QueryParameters: parameters})
	results := make(chan storage.Stream[storage.ListResult], 1)
	go func() {
		defer close(results)
		for pager.More() {
			res, err := pager.NextPage(ctx)
			if err != nil {
				sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing plans: %w", err)})
				if sendErr != nil {
					results <- storage.Stream[storage.ListResult]{Err: sendErr}
				}
				return
			}
			for _, item := range res.Items {
				result, err := r.listResultsFunc(item)
				if err != nil {
					sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing items in plans: %w", err)})
					if sendErr != nil {
						results <- storage.Stream[storage.ListResult]{Err: sendErr}
					}
					return
				}
				if sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Result: result}); sendErr != nil {
					results <- storage.Stream[storage.ListResult]{Err: sendErr}
					return
				}
			}
		}
		return
	}()
	return results, nil
}

func (r reader) buildSearchQuery(filters storage.Filters) (string, []azcosmos.QueryParameter) {
	parameters := []azcosmos.QueryParameter{}

	build := strings.Builder{}
	build.WriteString(fmt.Sprintf(searchPlans, r.container))

	numFilters := 0

	if len(filters.ByIDs) > 0 {
		numFilters++
		build.WriteString(" ARRAY_CONTAINS(@ids, p.id)")
	}
	if len(filters.ByGroupIDs) > 0 {
		if numFilters > 0 {
			build.WriteString(" AND")
		}
		numFilters++
		build.WriteString(" ARRAY_CONTAINS(@group_ids, p.groupID)")
	}
	if len(filters.ByStatus) > 0 {
		if numFilters > 0 {
			build.WriteString(" AND")
		}
		build.WriteString(" ")
		if len(filters.ByStatus) > 1 {
			build.WriteString("(")
		}
		numFilters++ // I know this says inEffectual assignment and it is, but it is here for completeness.
		for i, s := range filters.ByStatus {
			name := fmt.Sprintf("@status%d", i)
			if i == 0 {
				build.WriteString(fmt.Sprintf("p.stateStatus = %s", name))
			} else {
				build.WriteString(fmt.Sprintf(" OR p.stateStatus = %s", name))
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
	build.WriteString(" ORDER BY p.submitTime DESC")
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
	q := fmt.Sprintf(listPlans, r.container)
	if limit > 0 {
		q += fmt.Sprintf(" OFFSET 0 LIMIT %d", limit)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	pager := r.client.NewQueryItemsPager(q, azcosmos.NullPartitionKey, &azcosmos.QueryOptions{QueryParameters: []azcosmos.QueryParameter{}})
	results := make(chan storage.Stream[storage.ListResult], 1)
	go func() {
		defer close(results)
		for pager.More() {
			res, err := pager.NextPage(ctx)
			if err != nil {
				sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing plans: %w", err)})
				if sendErr != nil {
					results <- storage.Stream[storage.ListResult]{Err: sendErr}
				}
				return
			}
			for _, item := range res.Items {
				result, err := r.listResultsFunc(item)
				if err != nil {
					sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Err: fmt.Errorf("problem listing items in plans: %w", err)})
					if sendErr != nil {
						results <- storage.Stream[storage.ListResult]{Err: sendErr}
					}
					return
				}
				if sendErr := sender[storage.Stream[storage.ListResult]](ctx, results, storage.Stream[storage.ListResult]{Result: result}); sendErr != nil {
					results <- storage.Stream[storage.ListResult]{Err: sendErr}
					return
				}
			}
		}
	}()
	return results, nil
}

// listResultsFunc is a helper function to convert a CosmosDB document into a ListResult.
func (r reader) listResultsFunc(item []byte) (storage.ListResult, error) {
	var err error
	var resp plansEntry
	if err = json.Unmarshal(item, &resp); err != nil {
		return storage.ListResult{}, err
	}

	result := storage.ListResult{
		ID:         resp.PlanID,
		GroupID:    resp.GroupID,
		Name:       resp.Name,
		Descr:      resp.Descr,
		SubmitTime: resp.SubmitTime,
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	return result, nil
}
