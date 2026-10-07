package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/concurrency/worker"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"

	"github.com/google/uuid"
	"github.com/kylelemons/godebug/pretty"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestReaderList(t *testing.T) {
	t.Parallel()

	// Create a new database for this test
	tmpDir := os.TempDir()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestReaderList: couldn't generate UUID: %s", err)
	}
	dbPath := filepath.Join(tmpDir, id.String())
	defer os.RemoveAll(dbPath)

	pool, err := sqlitex.NewPool(
		dbPath,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 1,
		},
	)
	if err != nil {
		t.Fatalf("TestReaderList: couldn't create pool: %s", err)
	}
	defer pool.Close()

	// Create tables
	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("TestReaderList: couldn't get connection: %s", err)
	}

	if err := createTables(t.Context(), conn); err != nil {
		pool.Put(conn)
		t.Fatalf("TestReaderList: couldn't create tables: %s", err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	reader := reader{
		mu:   &sync.RWMutex{},
		pool: pool,
		reg:  reg,
	}

	ctx := t.Context()

	// Create three plans with different submit times
	plans := make([]*workflow.Plan, 3)
	now := time.Now()

	for i := 0; i < 3; i++ {
		p := createTestPlan(t, now.Add(time.Duration(-2+i)*time.Hour))
		plans[i] = p

		// Insert plan into database
		conn, err := pool.Take(ctx)
		if err != nil {
			t.Fatalf("TestReaderList: couldn't get connection: %s", err)
		}

		if err := commitPlan(ctx, conn, p, nil); err != nil {
			pool.Put(conn)
			t.Fatalf("TestReaderList: couldn't commit plan %d: %s", i, err)
		}
		pool.Put(conn)
	}

	tests := []struct {
		name      string
		limit     int
		wantCount int
		wantOrder []int // indices into plans array showing expected order
		wantErr   bool
	}{
		{
			name:      "Success: List all plans without limit",
			limit:     0,
			wantCount: 3,
			wantOrder: []int{2, 1, 0}, // newest to oldest
			wantErr:   false,
		},
		{
			name:      "Success: List with limit 2",
			limit:     2,
			wantCount: 2,
			wantOrder: []int{2, 1}, // newest two
			wantErr:   false,
		},
		{
			name:      "Success: List with limit 1",
			limit:     1,
			wantCount: 1,
			wantOrder: []int{2}, // only newest
			wantErr:   false,
		},
		{
			name:      "Success: List with limit larger than plan count",
			limit:     10,
			wantCount: 3,
			wantOrder: []int{2, 1, 0}, // all plans
			wantErr:   false,
		},
	}

	for _, test := range tests {
		ch, err := reader.List(ctx, test.limit)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestReaderList(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestReaderList(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}

		var results []storage.ListResult
		for item := range ch {
			if item.Err != nil {
				t.Errorf("TestReaderList(%s): got error in stream: %s", test.name, item.Err)
				continue
			}
			results = append(results, item.Result)
		}

		if len(results) != test.wantCount {
			t.Errorf("TestReaderList(%s): got %d results, want %d", test.name, len(results), test.wantCount)
			continue
		}

		// Verify order and contents
		for i, wantIdx := range test.wantOrder {
			wantPlan := plans[wantIdx]
			gotResult := results[i]

			if gotResult.ID != wantPlan.ID {
				t.Errorf("TestReaderList(%s): result[%d].ID = %s, want %s", test.name, i, gotResult.ID, wantPlan.ID)
			}
			if gotResult.GroupID != wantPlan.GroupID {
				t.Errorf("TestReaderList(%s): result[%d].GroupID = %s, want %s", test.name, i, gotResult.GroupID, wantPlan.GroupID)
			}
			if gotResult.Name != wantPlan.Name {
				t.Errorf("TestReaderList(%s): result[%d].Name = %s, want %s", test.name, i, gotResult.Name, wantPlan.Name)
			}
			if gotResult.Descr != wantPlan.Descr {
				t.Errorf("TestReaderList(%s): result[%d].Descr = %s, want %s", test.name, i, gotResult.Descr, wantPlan.Descr)
			}
			if !gotResult.SubmitTime.Equal(wantPlan.SubmitTime) {
				t.Errorf("TestReaderList(%s): result[%d].SubmitTime = %s, want %s", test.name, i, gotResult.SubmitTime, wantPlan.SubmitTime)
			}
			if gotResult.State.Status != wantPlan.State.Get().Status {
				t.Errorf("TestReaderList(%s): result[%d].State.Status = %v, want %v", test.name, i, gotResult.State.Status, wantPlan.State.Get().Status)
			}
		}

		// Additional verification: ensure results are in descending order by submit time
		for i := 1; i < len(results); i++ {
			if results[i].SubmitTime.After(results[i-1].SubmitTime) {
				t.Errorf("TestReaderList(%s): results not in descending order: result[%d].SubmitTime (%s) is after result[%d].SubmitTime (%s)",
					test.name, i, results[i].SubmitTime, i-1, results[i-1].SubmitTime)
			}
		}
	}
}

func TestReaderListPages(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestReaderListPages: New: %s", err)
	}
	defer v.Close(t.Context())

	submitTime := time.Now().UTC()
	const planCount = listPageSize + 1
	want := make(map[uuid.UUID]struct{}, planCount)
	for range planCount {
		plan := createTestPlan(t, submitTime)
		if err := v.Create(t.Context(), plan); err != nil {
			t.Fatalf("TestReaderListPages: Create: %s", err)
		}
		want[plan.ID] = struct{}{}
	}

	results, err := v.List(t.Context(), 0)
	if err != nil {
		t.Fatalf("TestReaderListPages: List: %s", err)
	}
	var got []storage.ListResult
	for result := range results {
		if result.Err != nil {
			t.Fatalf("TestReaderListPages: stream: %s", result.Err)
		}
		got = append(got, result.Result)
	}
	if len(got) != planCount {
		t.Fatalf("TestReaderListPages: got %d results, want %d", len(got), planCount)
	}
	for i, result := range got {
		if _, ok := want[result.ID]; !ok {
			t.Errorf("TestReaderListPages: result[%d] has unexpected or duplicate ID %s", i, result.ID)
			continue
		}
		delete(want, result.ID)
		if i > 0 && got[i-1].ID.String() < result.ID.String() {
			t.Errorf("TestReaderListPages: IDs are not descending at result[%d]: %s before %s", i, got[i-1].ID, result.ID)
		}
	}
}

func TestReaderListDoesNotBlockUpdate(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestReaderListDoesNotBlockUpdate: New: %s", err)
	}
	defer v.Close(t.Context())

	plans := []*workflow.Plan{
		createTestPlan(t, time.Now().Add(-2*time.Minute)),
		createTestPlan(t, time.Now().Add(-time.Minute)),
		createTestPlan(t, time.Now()),
	}
	for _, plan := range plans {
		if err := v.Create(t.Context(), plan); err != nil {
			t.Fatalf("TestReaderListDoesNotBlockUpdate: Create: %s", err)
		}
	}

	listCtx, cancelList := context.WithCancel(t.Context())
	defer cancelList()
	results, err := v.List(listCtx, 0)
	if err != nil {
		t.Fatalf("TestReaderListDoesNotBlockUpdate: List: %s", err)
	}
	// A result is only sent after its page is read, so once one arrives the page's read lock is released. The
	// producer then buffers the second result and parks sending the third, an open stream nobody is reading.
	if first := <-results; first.Err != nil {
		t.Fatalf("TestReaderListDoesNotBlockUpdate: first result: %s", first.Err)
	}

	// If the open stream held the lock, this would deadlock and the test would hit the go test timeout.
	plans[0].State.Set(workflow.State{Status: workflow.Completed})
	if err := v.UpdatePlan(t.Context(), plans[0]); err != nil {
		t.Errorf("TestReaderListDoesNotBlockUpdate: UpdatePlan: %s", err)
	}

	cancelList()
	for range results {
	}
}

// createTestPlan creates a test plan with all required fields properly initialized.
func createTestPlan(t *testing.T, submitTime time.Time) *workflow.Plan {
	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		t.Fatalf("createTestPlan: couldn't create builder: %s", err)
	}

	checkAction := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.CheckPluginName,
		Req:    nil,
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction)
	build.Up()

	p, err := build.Plan()
	if err != nil {
		t.Fatalf("createTestPlan: couldn't build plan: %s", err)
	}

	// Set IDs and states for all objects
	p.SetID(mustUUID())
	p.SubmitTime = submitTime
	p.SetState(workflow.State{
		Status: workflow.Running,
		Start:  submitTime,
		End:    time.Time{},
	})

	// Set IDs and states for checks
	if p.PreChecks != nil {
		p.PreChecks.SetID(mustUUID())
		p.PreChecks.Key = mustUUID()
		p.PreChecks.SetState(workflow.State{
			Status: workflow.Running,
			Start:  submitTime,
			End:    time.Time{},
		})
		for _, action := range p.PreChecks.Actions {
			action.SetID(mustUUID())
			action.SetPlanID(p.ID)
			action.SetState(workflow.State{
				Status: workflow.Running,
				Start:  submitTime,
				End:    time.Time{},
			})
		}
	}

	return p
}

// TestSearchMultipleStatuses tests that searching with multiple statuses returns
// plans matching ANY of the statuses (OR logic), not ALL of them (AND logic).
// This tests the fix for a bug where the SQL query used AND instead of OR,
// which would always return zero results when multiple statuses were provided.
func TestSearchMultipleStatuses(t *testing.T) {
	t.Parallel()

	tmpDir := os.TempDir()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("TestSearchMultipleStatuses: couldn't generate UUID: %s", err)
	}
	dbPath := filepath.Join(tmpDir, id.String())
	defer os.RemoveAll(dbPath)

	pool, err := sqlitex.NewPool(
		dbPath,
		sqlitex.PoolOptions{
			Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
			PoolSize: 1,
		},
	)
	if err != nil {
		t.Fatalf("TestSearchMultipleStatuses: couldn't create pool: %s", err)
	}
	defer pool.Close()

	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("TestSearchMultipleStatuses: couldn't get connection: %s", err)
	}

	if err := createTables(t.Context(), conn); err != nil {
		pool.Put(conn)
		t.Fatalf("TestSearchMultipleStatuses: couldn't create tables: %s", err)
	}
	pool.Put(conn)

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	r := reader{
		mu:   &sync.RWMutex{},
		pool: pool,
		reg:  reg,
	}

	ctx := t.Context()
	now := time.Now()

	// Create plans with different statuses
	statusPlans := map[workflow.Status]*workflow.Plan{
		workflow.Running:   createTestPlanWithStatus(t, now.Add(-3*time.Hour), workflow.Running),
		workflow.Completed: createTestPlanWithStatus(t, now.Add(-2*time.Hour), workflow.Completed),
		workflow.Failed:    createTestPlanWithStatus(t, now.Add(-1*time.Hour), workflow.Failed),
	}

	for status, plan := range statusPlans {
		conn, err := pool.Take(ctx)
		if err != nil {
			t.Fatalf("TestSearchMultipleStatuses: couldn't get connection: %s", err)
		}
		if err := commitPlan(ctx, conn, plan, nil); err != nil {
			pool.Put(conn)
			t.Fatalf("TestSearchMultipleStatuses: couldn't commit plan with status %v: %s", status, err)
		}
		pool.Put(conn)
	}

	tests := []struct {
		name         string
		statuses     []workflow.Status
		wantCount    int
		wantStatuses []workflow.Status
	}{
		{
			name:         "Success: Search with single status returns matching plan",
			statuses:     []workflow.Status{workflow.Running},
			wantCount:    1,
			wantStatuses: []workflow.Status{workflow.Running},
		},
		{
			name:         "Success: Search with two statuses returns both matching plans",
			statuses:     []workflow.Status{workflow.Running, workflow.Completed},
			wantCount:    2,
			wantStatuses: []workflow.Status{workflow.Completed, workflow.Running},
		},
		{
			name:         "Success: Search with all three statuses returns all plans",
			statuses:     []workflow.Status{workflow.Running, workflow.Completed, workflow.Failed},
			wantCount:    3,
			wantStatuses: []workflow.Status{workflow.Failed, workflow.Completed, workflow.Running},
		},
	}

	for _, test := range tests {
		ch, err := r.Search(ctx, storage.Filters{ByStatus: test.statuses})
		if err != nil {
			t.Errorf("TestSearchMultipleStatuses(%s): Search returned error: %s", test.name, err)
			continue
		}

		var results []storage.ListResult
		for item := range ch {
			if item.Err != nil {
				t.Errorf("TestSearchMultipleStatuses(%s): got error in stream: %s", test.name, item.Err)
				continue
			}
			results = append(results, item.Result)
		}

		if len(results) != test.wantCount {
			t.Errorf("TestSearchMultipleStatuses(%s): got %d results, want %d", test.name, len(results), test.wantCount)
			continue
		}

		// Verify that all returned results have one of the expected statuses
		for i, result := range results {
			found := false
			for _, wantStatus := range test.wantStatuses {
				if result.State.Status == wantStatus {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("TestSearchMultipleStatuses(%s): result[%d] has status %v, which is not in expected statuses %v",
					test.name, i, result.State.Status, test.wantStatuses)
			}
		}
	}
}

// createTestPlanWithStatus creates a test plan with the specified status.
func createTestPlanWithStatus(t *testing.T, submitTime time.Time, status workflow.Status) *workflow.Plan {
	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		t.Fatalf("createTestPlanWithStatus: couldn't create builder: %s", err)
	}

	checkAction := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.CheckPluginName,
		Req:    nil,
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction)
	build.Up()

	p, err := build.Plan()
	if err != nil {
		t.Fatalf("createTestPlanWithStatus: couldn't build plan: %s", err)
	}

	p.SetID(mustUUID())
	p.SubmitTime = submitTime
	p.SetState(workflow.State{
		Status: status,
		Start:  submitTime,
		End:    time.Time{},
	})

	if p.PreChecks != nil {
		p.PreChecks.SetID(mustUUID())
		p.PreChecks.Key = mustUUID()
		p.PreChecks.SetState(workflow.State{
			Status: workflow.Running,
			Start:  submitTime,
			End:    time.Time{},
		})
		for _, action := range p.PreChecks.Actions {
			action.SetID(mustUUID())
			action.SetPlanID(p.ID)
			action.SetState(workflow.State{
				Status: workflow.Running,
				Start:  submitTime,
				End:    time.Time{},
			})
		}
	}

	return p
}

func TestExists(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestExists: New: %s", err)
	}
	defer v.Close(t.Context())

	plan := createTestPlan(t, time.Now().UTC())
	if err := v.Create(t.Context(), plan); err != nil {
		t.Fatalf("TestExists: Create: %s", err)
	}

	tests := []struct {
		name string
		id   uuid.UUID
		want bool
	}{
		{
			name: "Success: a stored plan ID exists.",
			id:   plan.ID,
			want: true,
		},
		{
			name: "Success: an unknown plan ID does not exist.",
			id:   mustUUID(),
			want: false,
		},
	}

	for _, test := range tests {
		got, err := v.Exists(t.Context(), test.id)
		if err != nil {
			t.Errorf("TestExists(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		if got != test.want {
			t.Errorf("TestExists(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

// TestQueriesUseIndex checks that the paged List and Search queries are ordered by an index, and that Delete finds a
// plan's rows by an index. Without one, every List page is a full scan of plans plus a sort, and every Delete scans
// every table.
func TestQueriesUseIndex(t *testing.T) {
	t.Parallel()

	v, err := New(t.Context(), "", registry.New(), WithInMemory())
	if err != nil {
		t.Fatalf("TestQueriesUseIndex: New: %s", err)
	}
	defer v.Close(t.Context())

	cursor := &listCursor{submitTime: time.Now().UnixNano(), id: mustUUID().String()}
	byStatus := storage.Filters{ByStatus: []workflow.Status{workflow.Running, workflow.Completed}}
	planID := map[string]any{"$plan_id": mustUUID().String()}

	type test struct {
		name  string
		build func() (string, []any, map[string]any)
	}
	tests := []test{
		{
			name: "Success: the first List page is ordered by an index.",
			build: func() (string, []any, map[string]any) {
				return v.reader.buildSearchQuery(storage.Filters{}).page(nil, listPageSize)
			},
		},
		{
			name: "Success: a later List page is ordered by an index.",
			build: func() (string, []any, map[string]any) {
				return v.reader.buildSearchQuery(storage.Filters{}).page(cursor, listPageSize)
			},
		},
		{
			name: "Success: a later Search page by status is ordered by an index.",
			build: func() (string, []any, map[string]any) {
				return v.reader.buildSearchQuery(byStatus).page(cursor, listPageSize)
			},
		},
	}
	for _, q := range deleteByPlanID {
		tests = append(tests, test{
			name:  fmt.Sprintf("Success: %q finds its rows by an index.", q),
			build: func() (string, []any, map[string]any) { return q, nil, planID },
		})
	}

	conn, err := v.pool.Take(t.Context())
	if err != nil {
		t.Fatalf("TestQueriesUseIndex: Take: %s", err)
	}
	defer v.pool.Put(conn)

	for _, test := range tests {
		query, args, named := test.build()
		var plan []string
		err := sqlitex.Execute(conn, "EXPLAIN QUERY PLAN "+query, &sqlitex.ExecOptions{
			Args:  args,
			Named: named,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				plan = append(plan, stmt.ColumnText(3))
				return nil
			},
		})
		if err != nil {
			t.Errorf("TestQueriesUseIndex(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		for _, step := range plan {
			fullScan := strings.HasPrefix(step, "SCAN ") && !strings.Contains(step, " USING ")
			if fullScan || step == "USE TEMP B-TREE FOR ORDER BY" {
				t.Errorf("TestQueriesUseIndex(%s): query plan has %q, want every row found or ordered by an index: %v", test.name, step, plan)
				break
			}
		}
	}
}

func TestListDoesNotHoldLimitedPoolSlot(t *testing.T) {
	// Not parallel: asserts that a Submit on a full pool does not time out.

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestListDoesNotHoldLimitedPoolSlot: New: %s", err)
	}
	defer v.Close(t.Context())

	// Two plans: the first fills the stream's buffer, so the producer parks sending the second.
	for range 2 {
		if err := v.Create(t.Context(), createTestPlan(t, time.Now().UTC())); err != nil {
			t.Fatalf("TestListDoesNotHoldLimitedPoolSlot: Create: %s", err)
		}
	}

	limited := worker.Default().Limited(t.Context(), "TestListDoesNotHoldLimitedPoolSlot", 1)
	listCtx, cancelList := context.WithCancel(context.SetPool(t.Context(), limited))
	defer cancelList()

	// The stream is never read, so its producer parks until listCtx is cancelled.
	if _, err := v.List(listCtx, 0); err != nil {
		t.Fatalf("TestListDoesNotHoldLimitedPoolSlot: List: %s", err)
	}

	submitCtx, cancelSubmit := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelSubmit()
	if !limited.Submit(submitCtx, func() {}) {
		t.Errorf("TestListDoesNotHoldLimitedPoolSlot: Submit on the caller's limited pool timed out, want the List producer outside the limit")
	}
}

func TestReaderListCancel(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestReaderListCancel: New: %s", err)
	}
	defer v.Close(t.Context())

	const planCount = 3
	for range planCount {
		if err := v.Create(t.Context(), createTestPlan(t, time.Now().UTC())); err != nil {
			t.Fatalf("TestReaderListCancel: Create: %s", err)
		}
	}

	tests := []struct {
		name string
		// cancel cancels the List before any result is read.
		cancel  bool
		wantErr bool
	}{
		{
			name: "Success: a List that is read to the end returns every plan.",
		},
		{
			name:    "Error: a List cancelled before it is read ends with an error.",
			cancel:  true,
			wantErr: true,
		},
	}

	for _, test := range tests {
		ctx, cancel := context.WithCancel(t.Context())
		results, err := v.List(ctx, 0)
		if err != nil {
			cancel()
			t.Fatalf("TestReaderListCancel(%s): List: %s", test.name, err)
		}
		if test.cancel {
			cancel()
		}

		var got []storage.Stream[storage.ListResult]
		for result := range results {
			got = append(got, result)
		}
		cancel()

		err = nil
		if len(got) > 0 {
			err = got[len(got)-1].Err
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestReaderListCancel(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestReaderListCancel(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if len(got) != planCount {
			t.Errorf("TestReaderListCancel(%s): got %d results, want %d", test.name, len(got), planCount)
		}
	}
}

func TestSearchByIDs(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestSearchByIDs: New: %s", err)
	}
	defer v.Close(t.Context())

	// group spans more than one page, so its searches read a page with the cursor clause.
	group := mustUUID()
	var inGroup, other []uuid.UUID
	submitTime := time.Now().UTC()
	for i := 0; i < listPageSize+1; i++ {
		plan := createTestPlan(t, submitTime)
		plan.GroupID = group
		if err := v.Create(t.Context(), plan); err != nil {
			t.Fatalf("TestSearchByIDs: Create: %s", err)
		}
		inGroup = append(inGroup, plan.ID)
	}
	for i := 0; i < 2; i++ {
		plan := createTestPlan(t, submitTime)
		if err := v.Create(t.Context(), plan); err != nil {
			t.Fatalf("TestSearchByIDs: Create: %s", err)
		}
		other = append(other, plan.ID)
	}

	tests := []struct {
		name    string
		filters storage.Filters
		want    []uuid.UUID
	}{
		{
			name:    "Success: search by plan IDs returns only those plans.",
			filters: storage.Filters{ByIDs: []uuid.UUID{inGroup[0], other[0]}},
			want:    []uuid.UUID{inGroup[0], other[0]},
		},
		{
			name:    "Success: search by group ID returns every plan in the group across pages.",
			filters: storage.Filters{ByGroupIDs: []uuid.UUID{group}},
			want:    inGroup,
		},
		{
			name:    "Success: search by plan IDs and group ID returns the plans that match both.",
			filters: storage.Filters{ByIDs: []uuid.UUID{inGroup[1], other[1]}, ByGroupIDs: []uuid.UUID{group}},
			want:    []uuid.UUID{inGroup[1]},
		},
	}

	for _, test := range tests {
		results, err := v.Search(t.Context(), test.filters)
		if err != nil {
			t.Errorf("TestSearchByIDs(%s): got err == %s, want err == nil", test.name, err)
			continue
		}
		var got []uuid.UUID
		for result := range results {
			if result.Err != nil {
				t.Errorf("TestSearchByIDs(%s): got stream err == %s, want err == nil", test.name, result.Err)
				continue
			}
			got = append(got, result.Result.ID)
		}
		slices.SortFunc(got, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		want := slices.Clone(test.want)
		slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		if diff := pretty.Compare(want, got); diff != "" {
			t.Errorf("TestSearchByIDs(%s): -want +got:\n%s", test.name, diff)
		}
	}
}

func TestListAbandoned(t *testing.T) {
	// Not parallel: waits for the stream's send timeout to pass.

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	v, err := New(t.Context(), "", reg, WithInMemory())
	if err != nil {
		t.Fatalf("TestListAbandoned: New: %s", err)
	}
	defer v.Close(t.Context())
	v.reader.sendTimeout = 10 * time.Millisecond

	for range 3 {
		if err := v.Create(t.Context(), createTestPlan(t, time.Now().UTC())); err != nil {
			t.Fatalf("TestListAbandoned: Create: %s", err)
		}
	}

	// Never cancelled and not read until long after the send timeout, so the producer gives up instead of waiting.
	results, err := v.List(t.Context(), 0)
	if err != nil {
		t.Fatalf("TestListAbandoned: List: %s", err)
	}
	time.Sleep(50 * v.reader.sendTimeout)

	var last storage.Stream[storage.ListResult]
	for result := range results {
		last = result
	}
	if last.Err == nil {
		t.Errorf("TestListAbandoned: got last result err == nil, want err != nil")
	}
}

func TestSendErr(t *testing.T) {
	// Not parallel: asserts that a send returns without waiting out its timeout.

	r := reader{sendTimeout: 200 * time.Millisecond}
	results := make(listStream, 1)
	results <- listItem{} // A caller that stopped reading: the buffer is full and nothing will take from it.
	sender := r.newListSender(results, "list")

	err := sender.send(t.Context(), listItem{})
	if err == nil {
		t.Fatalf("TestSendErr: got first send err == nil, want err != nil")
	}

	start := time.Now()
	sender.sendErr(t.Context(), err)
	if elapsed := time.Since(start); elapsed >= r.sendTimeout {
		t.Errorf("TestSendErr: sendErr took %v after a send already timed out, want less than %v", elapsed, r.sendTimeout)
	}
	if last := <-results; last.Err == nil {
		t.Errorf("TestSendErr: got last item err == nil, want err != nil")
	}
}

func TestRead(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	tests := []struct {
		name string
		// missing returns an object of the stored Plan whose row is deleted before the Read, or nil.
		missing func(p *workflow.Plan) workflow.Object
		wantErr bool
	}{
		{name: "Success: a stored Plan is read."},
		{
			name:    "Error: a Plan whose Checks row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.PreChecks },
			wantErr: true,
		},
		{
			name:    "Error: a Plan whose Block row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.Blocks[0] },
			wantErr: true,
		},
		{
			name:    "Error: a Plan whose Sequence row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.Blocks[0].Sequences[0] },
			wantErr: true,
		},
		{
			name:    "Error: a Plan whose Action row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.Blocks[0].Sequences[0].Actions[0] },
			wantErr: true,
		},
		{
			name:    "Error: a Plan whose DeferredActions row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.DeferredActions },
			wantErr: true,
		},
		{
			name:    "Error: a Plan whose DeferBatch row is missing is inconsistent.",
			missing: func(p *workflow.Plan) workflow.Object { return p.DeferredActions.DeferredBatches[0] },
			wantErr: true,
		},
	}

	for _, test := range tests {
		pool, err := freshInMemoryPool(t)
		if err != nil {
			t.Fatalf("TestRead(%s): freshInMemoryPool: %s", test.name, err)
		}
		t.Cleanup(func() { pool.Close() })

		conn, err := pool.Take(t.Context())
		if err != nil {
			t.Fatalf("TestRead(%s): Take: %s", test.name, err)
		}
		err = commitPlan(t.Context(), conn, plan, nil)
		pool.Put(conn)
		if err != nil {
			t.Fatalf("TestRead(%s): commitPlan: %s", test.name, err)
		}
		if test.missing != nil {
			deleteRow(t, pool, test.missing(plan))
		}

		r := reader{mu: &sync.RWMutex{}, pool: pool, reg: reg}
		got, err := r.Read(t.Context(), plan.ID)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestRead(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestRead(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			if !errors.IsStorageInconsistent(err) {
				t.Errorf("TestRead(%s): got err == %s, want it classified by errors.IsStorageInconsistent", test.name, err)
			}
			continue
		}
		if got.ID != plan.ID {
			t.Errorf("TestRead(%s): got Plan %s, want %s", test.name, got.ID, plan.ID)
		}
	}
}

func TestPageQuery(t *testing.T) {
	t.Parallel()

	pool, err := freshInMemoryPool(t)
	if err != nil {
		t.Fatalf("TestPageQuery: freshInMemoryPool: %s", err)
	}
	t.Cleanup(func() { pool.Close() })
	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("TestPageQuery: Take: %s", err)
	}
	defer pool.Put(conn)

	r := reader{}
	cursor := &listCursor{submitTime: time.Now().UnixNano(), id: mustUUID().String()}
	byStatus := storage.Filters{ByStatus: []workflow.Status{workflow.Running, workflow.Completed}}

	tests := []struct {
		name    string
		filters storage.Filters
		// cursors are the pages asked for, in order, from the same pageQuery. nil is the first page.
		cursors []*listCursor
	}{
		{
			name:    "Success: the first page and then a later page run.",
			cursors: []*listCursor{nil, cursor},
		},
		{
			name:    "Success: the first page runs again after a later page.",
			cursors: []*listCursor{nil, cursor, nil},
		},
		{
			name:    "Success: a filtered first page runs again after a later page.",
			filters: byStatus,
			cursors: []*listCursor{nil, cursor, nil},
		},
	}

	for _, test := range tests {
		q := r.buildSearchQuery(test.filters)
		for i, c := range test.cursors {
			query, args, named := q.page(c, listPageSize)
			if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args, Named: named}); err != nil {
				t.Errorf("TestPageQuery(%s): page %d: got err == %s, want err == nil", test.name, i, err)
				break
			}
		}
	}
}

func TestFetchActionsByIDs(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	reg.Register(&plugins.CheckPlugin{})
	reg.Register(&plugins.HelloPlugin{})

	pool, err := freshInMemoryPool(t)
	if err != nil {
		t.Fatalf("TestFetchActionsByIDs: freshInMemoryPool: %s", err)
	}
	t.Cleanup(func() { pool.Close() })
	conn, err := pool.Take(t.Context())
	if err != nil {
		t.Fatalf("TestFetchActionsByIDs: Take: %s", err)
	}
	defer pool.Put(conn)
	if err := commitPlan(t.Context(), conn, plan, nil); err != nil {
		t.Fatalf("TestFetchActionsByIDs: commitPlan: %s", err)
	}

	first := plan.Blocks[0].Sequences[0].Actions[0].ID
	second := plan.PreChecks.Actions[0].ID

	tests := []struct {
		name    string
		ids     []uuid.UUID
		wantErr bool
	}{
		{
			name: "Success: every listed Action is returned.",
			ids:  []uuid.UUID{first, second},
		},
		{
			name:    "Error: a listed Action with no row is inconsistent.",
			ids:     []uuid.UUID{first, mustUUID()},
			wantErr: true,
		},
		{
			name:    "Error: an Action listed twice is inconsistent.",
			ids:     []uuid.UUID{first, first},
			wantErr: true,
		},
	}

	r := reader{mu: &sync.RWMutex{}, pool: pool, reg: reg}
	for _, test := range tests {
		got, err := r.fetchActionsByIDs(t.Context(), conn, test.ids)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestFetchActionsByIDs(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestFetchActionsByIDs(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			if !errors.IsStorageInconsistent(err) {
				t.Errorf("TestFetchActionsByIDs(%s): got err == %s, want it classified by errors.IsStorageInconsistent", test.name, err)
			}
			continue
		}
		if len(got) != len(test.ids) {
			t.Errorf("TestFetchActionsByIDs(%s): got %d Actions, want %d", test.name, len(got), len(test.ids))
		}
	}
}
