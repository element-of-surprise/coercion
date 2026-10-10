package cosmosdb

import (
	"fmt"
	"strings"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/values/chans"

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

// Read returns a Plan from the storage. Retries are bounded by ctx: a read changes nothing, so cutting one short is
// safe.
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	var plan *workflow.Plan
	fetchPlan := func(ctx context.Context, rec exponential.Record) error {
		var err error
		plan, err = r.fetchPlanLocked(ctx, id)
		switch {
		case err == nil:
			return nil
		// Damaged storage will read the same way on every attempt.
		case errors.IsStorageInconsistent(err), !isRetriableError(err):
			return fmt.Errorf("%w: %w", err, errors.ErrPermanent)
		}
		return err
	}
	if err := backoff.Retry(ctx, fetchPlan); err != nil {
		switch {
		case isNotFound(err):
			return nil, errors.ErrNotFound(ctx, fmt.Errorf("plan(%s) not found: %w", id, err))
		case ctx.Err() != nil:
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("read of plan(%s) was cancelled: %w", id, err))
		}
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to fetch plan: %w", err))
	}
	return plan, nil
}

// fetchPlanLocked is fetchPlan under the read lock, so a writer cannot change the plan's documents during one attempt.
// Read takes the lock per attempt, not across its backoff waits, so a slow or failing read does not stall writers
// (and, behind a waiting writer, every other reader) for the whole retry window.
func (r reader) fetchPlanLocked(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.fetchPlan(ctx, id)
}

const searchKeyStr = "planSearch"

var searchKey = azcosmos.NewPartitionKeyString(searchKeyStr)

// Search returns a list of Plan IDs that match the filter.
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("invalid filter: %w: %w", err, errors.ErrPermanent))
	}

	q, parameters := r.buildSearchQuery(filters)

	r.mu.RLock()
	defer r.mu.RUnlock()

	pager := r.client.NewQueryItemsPager(q, searchKey, &azcosmos.QueryOptions{QueryParameters: parameters})
	return r.stream(ctx, pager, "search"), nil
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
	return r.stream(ctx, pager, "list"), nil
}

type listStream = chan storage.Stream[storage.ListResult]

// stream returns a channel that receives every ListResult from pager and is then closed. If ctx ends before the
// stream is complete, or a page or item cannot be read, the last item carries an error, so a caller can tell a
// truncated stream from a finished one. This holds even if ctx is done before the stream starts.
func (r reader) stream(ctx context.Context, pager *runtime.Pager[azcosmos.QueryItemsResponse], operation string) listStream {
	results := make(listStream, 1)
	// The producer parks until the caller reads or ctx ends, so it runs on the default pool rather than taking a
	// slot from a Limited pool the caller's ctx may carry.
	ok := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			defer close(results)
			for pager.More() {
				res, err := pager.NextPage(ctx)
				if err != nil {
					switch {
					case ctx.Err() != nil:
						// Cancelled while reading the page: classify it as the cancellation, not a storage failure.
						err = errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("%s was cancelled: %w", operation, err))
					default:
						err = errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("problem listing plans: %w", err))
					}
					sendErr(ctx, results, err)
					return
				}
				for _, item := range res.Items {
					result, err := r.listResultsFunc(item)
					if err != nil {
						sendErr(ctx, results, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("problem listing items in plans: %w", err)))
						return
					}
					if !chans.Put(ctx, results, storage.Stream[storage.ListResult]{Result: result}) {
						forceErr(results, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("%s was cancelled: %w", operation, context.Cause(ctx))))
						return
					}
				}
			}
		},
	)
	if !ok {
		// The producer never ran, so the buffer is empty and this send cannot block.
		results <- storage.Stream[storage.ListResult]{Err: errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("could not start %s: %w", operation, context.Cause(ctx)))}
		close(results)
	}
	return results
}

// sendErr sends err as the last item of results. If ctx ends before the caller takes it, the error is forced in.
func sendErr(ctx context.Context, results listStream, err error) {
	if chans.Put(ctx, results, storage.Stream[storage.ListResult]{Err: err}) {
		return
	}
	forceErr(results, err)
}

// forceErr puts err into results without waiting for the caller, dropping a buffered result to make room. The
// producer is the only sender, so once the buffer has room the send cannot block. Dropping a result is fine because
// the stream is already incomplete and the error tells the caller so.
func forceErr(results listStream, err error) {
	chans.TryGet(results)
	results <- storage.Stream[storage.ListResult]{Err: err}
}

// listResultsFunc is a helper function to convert a CosmosDB document into a ListResult.
func (r reader) listResultsFunc(item []byte) (storage.ListResult, error) {
	var err error
	var resp searchEntry
	if err = unmarshalDoc(item, &resp); err != nil {
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
