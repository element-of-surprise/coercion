package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// planReader implements the storage.PlanReader interface.
type planReader struct {
	conn *sqlite.Conn
	reg  *registry.Register
}

// Exists returns true if the Plan ID exists in the storage.
func (p *planReader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	const q = "SELECT COUNT(*) FROM 'plans' WHERE 'id' = ?;"

	count := -1
	err := sqlitex.ExecuteTransient(
		p.conn,
		q,
		&sqlitex.ExecOptions{
			Args: []any{
				id[:],
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		},
	)
	if err != nil {
		return false, fmt.Errorf("couldn't do a lookup in table plans: %w", err)
	}
	if count < 0 {
		return false, fmt.Errorf("bug: unexpected count value: %d", count)
	}
	return count > 0, nil
}

// ReadPlan returns a Plan from the storage.
func (p *planReader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	return p.fetchPlan(ctx, id)
}

// SearchPlans returns a list of Plan IDs that match the filter.
func (p *planReader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	q, named := p.buildSearchQuery(filters)

	results := make(chan storage.Stream[storage.ListResult], 1)

	go func() {
		err := sqlitex.Execute(
			p.conn,
			q,
			&sqlitex.ExecOptions{
				Named: named,
				ResultFunc: func(stmt *sqlite.Stmt) error {
					r, err := p.listResultsFunc(stmt)
					if err != nil {
						return fmt.Errorf("problem searching plans: %w", err)
					}
					select {
					case <-ctx.Done():
						results <- storage.Stream[storage.ListResult]{
							Err: ctx.Err(),
						}
						return ctx.Err()
					case results <- storage.Stream[storage.ListResult]{Result: r}:
						return nil
					}
				},
			},
		)

		if err != nil {
			results <- storage.Stream[storage.ListResult]{Err: fmt.Errorf("couldn't complete list plans: %w", err)}
		}
	}()
	return results, nil
}

func (p *planReader) buildSearchQuery(filters storage.Filters) (string, map[string]any) {
	const sel = `SELECT id, group_id, name, descr, submit_time, state_status, state_start, state_end FROM plans WHERE`

	named := map[string]any{}

	build := strings.Builder{}
	build.WriteString(sel)

	numFilters := 0

	if len(filters.ByIDs) > 0 {
		numFilters++
		named["$ids"] = idSearchFromUUID(filters.ByIDs)
		build.WriteString(" id IN ($ids)")
	}
	if len(filters.ByGroupIDs) > 0 {
		if numFilters > 0 {
			build.WriteString(" AND")
		}
		numFilters++
		named["$group_ids"] = idSearchFromUUID(filters.ByGroupIDs)
		build.WriteString(" group_id IN ($group_ids)")
	}
	if len(filters.ByStatus) > 0 {
		if numFilters > 0 {
			build.WriteString(" AND")
		}
		numFilters++
		for i, s := range filters.ByStatus {
			name := fmt.Sprintf("$status%d", i)
			named[name] = int64(s)
			if i == 0 {
				build.WriteString(fmt.Sprintf(" state_status = %s", name))
			} else {
				build.WriteString(fmt.Sprintf(" AND state_status = %s", name))
			}
		}
	}
	build.WriteString(" ORDER BY submit_time DESC;")
	return build.String(), named
}

// List returns a list of Plan IDs in the storage in order from newest to oldest. This should
// return with most recent submiited first. Limit sets the maximum number of
// entrie to return
func (p *planReader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	const listPlans = `SELECT id, group_id, name, descr, submit_time, state_status, state_start, state_end FROM plans ORDER BY submit_time DESC`

	named := map[string]any{}

	q := listPlans
	if limit > 0 {
		q += " LIMIT $limit;"
		named["$limit"] = limit
	}

	results := make(chan storage.Stream[storage.ListResult], 1)

	go func() {
		err := sqlitex.Execute(
			p.conn,
			q,
			&sqlitex.ExecOptions{
				Named: named,
				ResultFunc: func(stmt *sqlite.Stmt) error {
					r, err := p.listResultsFunc(stmt)
					if err != nil {
						return fmt.Errorf("problem listing plans: %w", err)
					}
					select {
					case <-ctx.Done():
						results <- storage.Stream[storage.ListResult]{
							Err: ctx.Err(),
						}
						return ctx.Err()
					case results <- storage.Stream[storage.ListResult]{Result: r}:
						return nil
					}
				},
			},
		)

		if err != nil {
			results <- storage.Stream[storage.ListResult]{Err: fmt.Errorf("couldn't complete list plans: %w", err)}
		}
	}()
	return results, nil
}

// listResultsFunc is a helper function to convert a SQLite statement into a ListResult.
func (p *planReader) listResultsFunc(stmt *sqlite.Stmt) (storage.ListResult, error) {
	r := storage.ListResult{}
	var err error
	r.ID, err = fieldToID("id", stmt)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("couldn't get ID: %w", err)
	}
	r.GroupID, err = fieldToID("group_id", stmt)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("couldn't get group ID: %w", err)
	}
	r.Name = stmt.GetText("name")
	r.Descr = stmt.GetText("descr")
	r.SubmitTime = time.Unix(0, stmt.GetInt64("submit_time"))
	r.State = &workflow.State{
		Status: workflow.Status(stmt.GetInt64("state_status")),
		Start:  time.Unix(0, stmt.GetInt64("state_start")),
		End:    time.Unix(0, stmt.GetInt64("state_end")),
	}
	return r, nil
}

func (p *planReader) private() {
	return
}

// fieldToID returns a uuid.UUID from a field "field" in the Stmt that must be a TEXT field.
func fieldToID(field string, stmt *sqlite.Stmt) (uuid.UUID, error) {
	return uuid.Parse(stmt.GetText(field))
}

// fieldToIDs returns the IDs from the statement field. Field must the a blob
// encoded as a JSON array that has string UUIDs in v7 format.
func fieldToIDs(field string, stmt *sqlite.Stmt) ([]uuid.UUID, error) {
	contents := fieldToBytes(field, stmt)
	if contents == nil {
		return nil, fmt.Errorf("actions IDs are nil")
	}
	strIDs := []string{}
	if err := json.Unmarshal(contents, &strIDs); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal action ids: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(strIDs))
	for _, id := range strIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse id(%s): %w", id, err)
		}
		ids = append(ids, u)
	}

	return ids, nil
}

func strToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// fieldToBytes returns the bytes of the field from the statement.
func fieldToBytes(field string, stmt *sqlite.Stmt) []byte {
	l := stmt.GetLen(field)
	if l == 0 {
		return nil
	}
	b := make([]byte, l)
	stmt.GetBytes(field, b)
	return b
}

func timeFromField(field string, stmt *sqlite.Stmt) (time.Time, error) {
	unixTime := stmt.GetInt64(field)
	if unixTime == 0 {
		return time.Time{}, nil
	}
	t := time.Unix(0, unixTime)
	if t.Before(zeroTime) {
		return time.Time{}, nil
	}
	return t, nil
}

// fieldToState pulls the state_start, state_end and state_status from a stmt
// and turns them into a *workflow.State.
func fieldToState(stmt *sqlite.Stmt) (*workflow.State, error) {
	start, err := timeFromField("state_start", stmt)
	if err != nil {
		return nil, err
	}
	end, err := timeFromField("state_end", stmt)
	if err != nil {
		return nil, err
	}
	return &workflow.State{
		Status: workflow.Status(stmt.GetInt64("state_status")),
		Start:  start,
		End:    end,
	}, nil
}

// idSearchFromUUID returns a string that can be used in a query to search for the given UUIDs.
// The returned byte slice is a comma separated list of UUIDs
func idSearchFromUUID(ids []uuid.UUID) string {
	if len(ids) == 0 {
		return ""
	}

	build := strings.Builder{}
	for i, id := range ids {
		build.WriteString(id.String())
		if i < len(ids)-1 {
			build.WriteString(",")
		}
	}
	return build.String()
}
