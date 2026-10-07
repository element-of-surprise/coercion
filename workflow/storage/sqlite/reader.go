package sqlite

import (
	"fmt"
	"maps"
	"strings"
	"time"
	"unsafe"

	"github.com/gostdlib/base/concurrency/sync"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// reader implements the storage.PlanReader interface.
type reader struct {
	// mu is the Vault's lock. Reads hold it for reading. The pool shares its cache between connections, which locks
	// tables one at a time, so a read holding some tables could otherwise deadlock with a write transaction holding
	// others (SQLITE_LOCKED, "database is deadlocked").
	mu   *sync.RWMutex
	pool *sqlitex.Pool
	reg  *registry.Register
	// sendTimeout is how long a List or Search stream waits for its caller to take a result before it gives up.
	// Zero means defaultSendTimeout.
	sendTimeout time.Duration
}

const listPageSize = 100

// defaultSendTimeout is how long a List or Search stream waits for its caller to take a result before it gives up and
// ends with an error. It bounds the life of a stream whose caller stops reading without cancelling ctx.
const defaultSendTimeout = time.Minute

type listCursor struct {
	submitTime int64
	id         string
}

// listItem is one item of a List or Search stream, and listStream is the stream.
type (
	listItem   = storage.Stream[storage.ListResult]
	listStream = chan listItem
)

// pageQuery is a List or Search query, built once per stream. Only its cursor and limit change between pages.
type pageQuery struct {
	// first reads the first page, and next reads each later page, after the cursor.
	first, next string
	args        []any
	// named holds the filters' named arguments. page copies it, so it never holds a page's limit or cursor.
	named map[string]any
}

// page returns the query and arguments that read at most limit rows after cursor, or the first page if cursor is nil.
// Each call returns its own named arguments, holding only the ones its query uses.
func (q pageQuery) page(cursor *listCursor, limit int) (string, []any, map[string]any) {
	named := make(map[string]any, len(q.named)+3)
	maps.Copy(named, q.named)
	named["$page_limit"] = limit
	if cursor == nil {
		return q.first, q.args, named
	}
	named["$cursor_submit_time"] = cursor.submitTime
	named["$cursor_id"] = cursor.id
	return q.next, q.args, named
}

// Exists returns true if the Plan ID exists in the storage.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, err := r.pool.Take(ctx)
	if err != nil {
		return false, errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer r.pool.Put(conn)

	return planStored(ctx, conn, id)
}

// planStored reports whether conn has a row for the Plan with id.
func planStored(ctx context.Context, conn *sqlite.Conn, id uuid.UUID) (bool, error) {
	const q = "SELECT COUNT(*) FROM plans WHERE id = $id;"

	count := -1
	err := sqlitex.Execute(
		conn,
		q,
		&sqlitex.ExecOptions{
			Named: map[string]any{
				"$id": id.String(),
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		},
	)
	if err != nil {
		return false, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("couldn't do a lookup in table plans: %w", err))
	}
	if count < 0 {
		return false, errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("bug: unexpected count value: %d", count))
	}
	return count > 0, nil
}

// errMissingRow returns the error for a stored Plan that names an object with no row: its storage is damaged.
func errMissingRow(ctx context.Context, kind string, id uuid.UUID) error {
	return errors.E(ctx, errors.CatInternal, errors.TypeStorageInconsistent, fmt.Errorf("stored plan names %s(%s), which has no row", kind, id))
}

// Read returns a Plan from the storage.
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.fetchPlan(ctx, id)
}

// SearchPlans returns a list of Plan IDs that match the filter.
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("invalid filter: %w", err))
	}

	return r.streamList(ctx, r.buildSearchQuery(filters), 0, "search")
}

// buildSearchQuery returns the paged query for the plans that match filters, newest first.
func (r reader) buildSearchQuery(filters storage.Filters) pageQuery {
	const (
		sel    = `SELECT id, group_id, name, descr, submit_time, state_status, state_start, state_end FROM plans`
		after  = `(submit_time < $cursor_submit_time OR (submit_time = $cursor_submit_time AND id < $cursor_id))`
		order  = ` ORDER BY submit_time DESC, id DESC LIMIT $page_limit;`
		andStr = " AND "
	)

	named := map[string]any{}
	var args []any
	var filtersSQL []string

	if len(filters.ByIDs) > 0 {
		filtersSQL = append(filtersSQL, "id IN $ids")
	}
	if len(filters.ByGroupIDs) > 0 {
		filtersSQL = append(filtersSQL, "group_id IN $group_ids")
	}
	if len(filters.ByStatus) > 0 {
		var statuses []string
		for i, s := range filters.ByStatus {
			name := fmt.Sprintf("$status%d", i)
			named[name] = int64(s)
			statuses = append(statuses, fmt.Sprintf("state_status = %s", name))
		}
		filtersSQL = append(filtersSQL, "("+strings.Join(statuses, " OR ")+")")
	}

	filter := strings.Join(filtersSQL, andStr)
	if len(filters.ByIDs) > 0 {
		var idArgs []any
		filter, idArgs = replaceWithIDs(filter, "$ids", filters.ByIDs)
		args = append(args, idArgs...)
	}
	if len(filters.ByGroupIDs) > 0 {
		var groupArgs []any
		filter, groupArgs = replaceWithIDs(filter, "$group_ids", filters.ByGroupIDs)
		args = append(args, groupArgs...)
	}

	q := pageQuery{args: args, named: named}
	if filter == "" {
		q.first = sel + order
		q.next = sel + " WHERE " + after + order
		return q
	}
	q.first = sel + " WHERE " + filter + order
	q.next = sel + " WHERE " + filter + andStr + after + order
	return q
}

// List returns a list of Plan IDs in the storage in order from newest to oldest. This should
// return with most recent submiited first. Limit sets the maximum number of
// entrie to return
func (r reader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	return r.streamList(ctx, r.buildSearchQuery(storage.Filters{}), limit, "list")
}

func (r reader) streamList(ctx context.Context, q pageQuery, limit int, operation string) (listStream, error) {
	results := make(listStream, 1)
	// The producer parks until the caller reads or cancels, so it runs on the default pool rather than taking a slot
	// from a Limited pool the caller's ctx may carry.
	ok := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			defer close(results)
			sender := r.newListSender(results, operation)
			var cursor *listCursor
			remaining := limit
			for {
				pageLimit := listPageSize
				if remaining > 0 && remaining < pageLimit {
					pageLimit = remaining
				}
				query, args, named := q.page(cursor, pageLimit)
				page, err := r.readListPage(ctx, query, args, named)
				if err != nil {
					if ctx.Err() != nil {
						// Cancelled while reading the page: classify it as the cancellation, not a storage failure.
						err = errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("%s was cancelled: %w", operation, err))
					}
					sender.sendErr(ctx, err)
					return
				}
				for _, result := range page {
					if err := sender.send(ctx, listItem{Result: result}); err != nil {
						sender.sendErr(ctx, err)
						return
					}
				}
				if remaining > 0 {
					remaining -= len(page)
				}
				if len(page) < pageLimit || (limit > 0 && remaining == 0) {
					return
				}
				last := page[len(page)-1]
				cursor = &listCursor{submitTime: last.SubmitTime.UnixNano(), id: last.ID.String()}
			}
		},
	)
	if !ok {
		close(results)
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("could not start %s: %w", operation, context.Cause(ctx)))
	}
	return results, nil
}

// readListPage materializes one bounded page while holding the Vault's read lock. The lock and connection are released
// before the page is sent to the caller.
func (r reader) readListPage(ctx context.Context, query string, args []any, named map[string]any) ([]storage.ListResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, err := r.pool.Take(ctx)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer r.pool.Put(conn)

	results := make([]storage.ListResult, 0, listPageSize)
	err = sqlitex.Execute(conn, query, &sqlitex.ExecOptions{
		Args:  args,
		Named: named,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result, err := r.listResultsFunc(stmt)
			if err != nil {
				return err
			}
			results = append(results, result)
			return nil
		},
	})
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageList, fmt.Errorf("couldn't read a page of plans: %w", err))
	}
	return results, nil
}

// listSender sends the items of one List or Search stream. It reuses one timer for every send.
type listSender struct {
	results   listStream
	operation string
	timeout   time.Duration
	timer     *time.Timer
	// failed is set once a send fails. The caller is gone or ctx is done, so no later send waits.
	failed bool
}

// newListSender returns a listSender for results.
func (r reader) newListSender(results listStream, operation string) *listSender {
	timeout := r.sendTimeout
	if timeout == 0 {
		timeout = defaultSendTimeout
	}
	timer := time.NewTimer(timeout)
	timer.Stop()
	return &listSender{results: results, operation: operation, timeout: timeout, timer: timer}
}

// send sends item. It returns a TypeTimeout error, without sending, if ctx is done or the caller does not take the
// item within the send timeout.
func (s *listSender) send(ctx context.Context, item listItem) error {
	s.timer.Reset(s.timeout)
	defer s.timer.Stop()

	select {
	case s.results <- item:
		return nil
	case <-ctx.Done():
		s.failed = true
		return errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("%s was cancelled: %w", s.operation, context.Cause(ctx)))
	case <-s.timer.C:
		s.failed = true
		return errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("%s caller did not take a result within %v", s.operation, s.timeout))
	}
}

// sendErr sends err as the last item, so the caller can tell a stream that failed or was cancelled from one that
// finished. Unless a send already failed, it first waits up to the send timeout for the caller to take it. Once a send
// has failed, ctx is done or the caller stopped reading, so it drops the buffered result to make room instead of
// waiting. The producer is the only sender, so once the buffer has room the final send cannot block.
func (s *listSender) sendErr(ctx context.Context, err error) {
	item := listItem{Err: err}
	if !s.failed && s.send(ctx, item) == nil {
		return
	}
	select {
	case <-s.results:
	default:
	}
	s.results <- item
}

// listResultsFunc is a helper function to convert a SQLite statement into a ListResult.
func (r reader) listResultsFunc(stmt *sqlite.Stmt) (storage.ListResult, error) {
	result := storage.ListResult{}
	var err error
	result.ID, err = fieldToID("id", stmt)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("couldn't get ID: %w", err)
	}
	result.GroupID, err = fieldToID("group_id", stmt)
	if err != nil {
		return storage.ListResult{}, fmt.Errorf("couldn't get group ID: %w", err)
	}
	result.Name = stmt.GetText("name")
	result.Descr = stmt.GetText("descr")
	result.SubmitTime = time.Unix(0, stmt.GetInt64("submit_time"))
	result.State = workflow.State{
		Status: workflow.Status(stmt.GetInt64("state_status")),
		Start:  time.Unix(0, stmt.GetInt64("state_start")),
		End:    time.Unix(0, stmt.GetInt64("state_end")),
	}
	return result, nil
}

func (r reader) private() {
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
