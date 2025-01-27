package cosmosdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/ops/retry/exponential"
)

const (
	// beginning of query to list plans with a filter
	searchPlans = `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM %s p WHERE p.type=1 AND`
	// list all plans without parameters
	listPlans = `SELECT p.id, p.groupID, p.name, p.descr, p.submitTime, p.stateStatus, p.stateStart, p.stateEnd FROM %s p WHERE p.type=1 ORDER BY p.submitTime DESC`
)

// reader implements the storage.PlanReader interface.
type reader struct {
	cName string
	Client
	reg *registry.Register
}

// Exists returns true if the Plan ID exists in the storage.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := r.GetContainerClient().ReadItem(ctx, r.GetPK(), id.String(), r.ItemOptions())
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("couldn't fetch plan by id: %w", err)
	}
	return true, nil
}

// Read returns a Plan from the storage.
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	var plan *workflow.Plan
	var err error
	fetchPlan := func(ctx context.Context, rec exponential.Record) error {
		plan, err = r.fetchPlan(ctx, id)
		if err != nil {
			if !isRetriableError(err) || rec.Attempt >= 5 {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}
	if err := backoff.Retry(ctx, fetchPlan); err != nil {
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

	pager := r.GetContainerClient().NewQueryItemsPager(q, r.GetPK(), &azcosmos.QueryOptions{QueryParameters: parameters})
	results := make(chan storage.Stream[storage.ListResult])
	go func() {
		defer close(results)
		// need to check for ctx timeout
		for pager.More() {
			res, err := pager.NextPage(ctx)
			if err != nil {
				results <- storage.Stream[storage.ListResult]{
					Err: fmt.Errorf("problem listing plans: %w", err),
				}
				return
			}
			for _, item := range res.Items {
				result, err := r.listResultsFunc(item)
				if err != nil {
					results <- storage.Stream[storage.ListResult]{
						Err: fmt.Errorf("problem listing items in plans: %w", err),
					}
					return
				}
				results <- storage.Stream[storage.ListResult]{Result: result}
			}
		}
		return
	}()
	return results, nil
}

func (r reader) buildSearchQuery(filters storage.Filters) (string, []azcosmos.QueryParameter) {
	parameters := []azcosmos.QueryParameter{}

	build := strings.Builder{}
	build.WriteString(fmt.Sprintf(searchPlans, r.cName))

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
		numFilters++ // I know this says inEffectual assignment and it is, but it is here for completeness.
		for i, s := range filters.ByStatus {
			name := fmt.Sprintf("@status%d", i)
			if i == 0 {
				build.WriteString(fmt.Sprintf(" p.stateStatus = %s", name))
			} else {
				build.WriteString(fmt.Sprintf(" AND p.stateStatus = %s", name))
			}
			parameters = append(parameters, azcosmos.QueryParameter{
				Name:  name,
				Value: int64(s),
			})
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
// return with most recent submiited first. Limit sets the maximum number of
// entries to return
func (r reader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	q := fmt.Sprintf(listPlans, r.cName)
	if limit > 0 {
		q += fmt.Sprintf(" OFFSET 0 LIMIT %d", limit)
	}

	pager := r.GetContainerClient().NewQueryItemsPager(q, r.GetPK(), &azcosmos.QueryOptions{QueryParameters: []azcosmos.QueryParameter{}})
	results := make(chan storage.Stream[storage.ListResult])
	go func() {
		defer close(results)
		for pager.More() {
			res, err := pager.NextPage(ctx)
			if err != nil {
				results <- storage.Stream[storage.ListResult]{
					Err: fmt.Errorf("problem listing plans: %w", err),
				}
				return
			}
			for _, item := range res.Items {
				result, err := r.listResultsFunc(item)
				if err != nil {
					results <- storage.Stream[storage.ListResult]{
						Err: fmt.Errorf("problem listing items in plans: %w", err),
					}
					return
				}
				results <- storage.Stream[storage.ListResult]{Result: result}
			}
		}
	}()
	return results, nil
}

// listResultsFunc is a helper function to convert a CosmosDB document into a ListResult.
func (r reader) listResultsFunc(item []byte) (storage.ListResult, error) {
	var err error
	var resp plansEntry
	err = json.Unmarshal(item, &resp)
	if err != nil {
		return storage.ListResult{}, err
	}

	result := storage.ListResult{
		ID:         resp.ID,
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

func (r reader) private() {
	return
}
