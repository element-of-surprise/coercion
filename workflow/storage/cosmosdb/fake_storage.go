package cosmosdb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// TODO: Improve this so that we take into account the partition key so if that doesn't match it doesn't return.

// fakeStorage fakes the storage methods needed to communicate with azcosmos used in this package.
// We have to use some unsafe methods to avoid writing unneccesary wrappers to get at data used for mocks.
// This is sad and cost us a lot of time.
// This is not 100% a fake, in that we are relying on error here to know if something worked. But Cosmos,
// unfortunately, can screw us returning errors in azcosmos.TransactionalBatchResponse{} and we aren't simulating that.
// I'm not even sure if that happens.
type fakeStorage struct {
	mu   sync.Mutex
	pool *sqlitex.Pool
	reg  *registry.Register

	readItemErr   error
	queryItemsErr bool
	createItemErr bool
	deleteItemErr bool
}

func newFakeStorage(reg *registry.Register) *fakeStorage {
	const table = `
CREATE Table If Not Exists pages (
	id TEXT PRIMARY KEY,
	plan_id TEXT NOT NULL,
	data BLOB NOT NULL
);`
	var flags sqlite.OpenFlags
	flags |= sqlite.OpenReadWrite
	flags |= sqlite.OpenCreate
	flags |= sqlite.OpenWAL
	flags |= sqlite.OpenMemory

	// NOTE: Pool is set to 1. I'm having a problem with multiple conns seeing the commits of each other.
	// Such as even Pool creation. Not sure what is wrong. PoolSize 1 is a workaround for the moment.
	pool, err := sqlitex.NewPool(uuid.NewString(), sqlitex.PoolOptions{Flags: flags, PoolSize: 1})
	if err != nil {
		panic(err)
	}

	conn, err := pool.Take(context.Background())
	if err != nil {
		panic(err)
	}
	defer pool.Put(conn)

	err = sqlitex.Execute(
		conn,
		"PRAGMA journal_mode=WAL;",
		nil,
	)
	if err != nil {
		log.Fatalf("Could not set WAL mode: %v", err)
	}

	if err := sqlitex.ExecuteTransient(
		conn,
		table,
		&sqlitex.ExecOptions{},
	); err != nil {
		panic(fmt.Sprintf("couldn't create table: %s", err))
	}
	return &fakeStorage{pool: pool, reg: reg}
}

func (f *fakeStorage) writeData(ctx context.Context, id, planID string, data []byte) (err error) {
	const q = `INSERT OR REPLACE INTO pages (id, plan_id, data) VALUES ($id, $plan_id, $data);`

	conn, err := f.pool.Take(ctx)
	if err != nil {
		panic(fmt.Sprintf("couldn't get a connection from the pool: %s", err))
	}
	defer f.pool.Put(conn)
	defer sqlitex.Transaction(conn)(&err)

	err = sqlitex.Execute(conn, q, &sqlitex.ExecOptions{
		Named: map[string]any{
			"$id":      id,
			"$plan_id": planID,
			"$data":    data,
		},
	})

	if err != nil {
		panic(err)
	}
	return nil
}

func (f *fakeStorage) deleteItem(ctx context.Context, id string) (err error) {
	const q = `DELETE FROM pages WHERE id = $id;`

	if f.deleteItemErr {
		return errors.New("error")
	}

	conn, err := f.pool.Take(ctx)
	if err != nil {
		panic(fmt.Sprintf("couldn't get a connection from the pool: %s", err))
	}
	defer f.pool.Put(conn)
	defer sqlitex.Transaction(conn)(&err)

	err = sqlitex.Execute(conn, q, &sqlitex.ExecOptions{
		Named: map[string]any{
			"$id": id,
		},
	})

	if err != nil {
		panic(err)
	}
	return nil
}

type data struct {
	id     string
	planID string
	data   []byte
}

func (f *fakeStorage) allIDs(conn *sqlite.Conn) ([]data, error) {
	const fetchAll = `SELECT id, plan_id, data FROM pages`

	d := []data{}
	err := sqlitex.Execute(
		conn,
		fetchAll,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				b := make([]byte, stmt.GetLen("data"))
				stmt.GetBytes("data", b)
				d = append(
					d,
					data{
						id:     stmt.GetText("id"),
						planID: stmt.GetText("plan_id"),
						data:   b,
					},
				)
				return nil
			},
		},
	)
	if err != nil {
		panic(err)
	}
	return d, nil
}

// ExecuteTransactionalBatch creates a transactional batch.
func (f *fakeStorage) NewTransactionalBatch(partitionKey azcosmos.PartitionKey) azcosmos.TransactionalBatch {
	return azcosmos.TransactionalBatch{}
}

// ExecuteTransactionalBatch implements the ExecuteBatcher interface.
func (f *fakeStorage) ExecuteTransactionalBatch(ctx context.Context, b azcosmos.TransactionalBatch, opts *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
	if reflect.ValueOf(b).IsZero() {
		return azcosmos.TransactionalBatchResponse{}, fmt.Errorf("empty transactional batch")
	}
	ops := unsafeBatchOps(&b)

	for _, op := range ops {
		switch op.op {
		case "Create":
			if f.createItemErr {
				return azcosmos.TransactionalBatchResponse{}, errors.New("error")
			}
			fields, err := getCommonFields(op.resourceBody)
			if err != nil {
				panic(err)
			}

			var id = fields.ID

			if fields.Type == workflow.OTPlan {
				id = fields.PlanID
			}

			if err := f.writeData(ctx, id.String(), fields.PlanID.String(), op.resourceBody); err != nil {
				panic(err)
			}
		case "Delete":
			if f.deleteItemErr {
				return azcosmos.TransactionalBatchResponse{}, errors.New("error")
			}
			if err := f.deleteItem(ctx, op.itemID); err != nil {
				return azcosmos.TransactionalBatchResponse{}, err
			}
		default:
			panic("do not support the TransactionBatch op: " + op.op)
		}
	}
	return azcosmos.TransactionalBatchResponse{}, nil
}

func (f *fakeStorage) WritePlan(ctx context.Context, plan *workflow.Plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	ictx, err := planToItems(plan)
	if err != nil {
		panic(err)
	}

	for id, item := range ictx.m {
		if err := f.writeData(ctx, id, plan.ID.String(), item); err != nil {
			panic(err)
		}
	}
	return nil
}

func (f *fakeStorage) ReadItem(ctx context.Context, pk azcosmos.PartitionKey, itemID string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	if f.readItemErr != nil {
		return azcosmos.ItemResponse{}, f.readItemErr
	}

	d, _, err := f.readItem(ctx, itemID)
	if err != nil {
		return azcosmos.ItemResponse{}, err
	}
	return azcosmos.ItemResponse{Value: d}, nil
}

func (f *fakeStorage) readItem(ctx context.Context, itemID string) (data []byte, planID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	const fetchByID = `SELECT id, plan_id, data FROM pages WHERE id = $id`

	conn, err := f.pool.Take(ctx)
	if err != nil {
		panic(err)
	}
	defer f.pool.Put(conn)

	// Leave for debugging.
	/*
		all, err := f.allIDs(conn)
		if err != nil {
			panic(err)
		}

		for _, d := range all {
			common, err := getCommonFields(d.data)
			if err != nil {
				panic(err)
			}
		}
	*/

	var item []byte
	err = sqlitex.Execute(
		conn,
		fetchByID,
		&sqlitex.ExecOptions{
			Named: map[string]any{
				"$id": strings.TrimSpace(itemID),
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				b := make([]byte, stmt.GetLen("data"))
				stmt.GetBytes("data", b)
				item = b
				planID = stmt.GetText("plan_id")
				return nil
			},
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("couldn't fetch plan: %w", err)
	}

	if len(item) == 0 {
		return nil, "", &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}

	return item, planID, nil
}

func (f *fakeStorage) NewQueryItemsPager(query string, pk azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
	const q = `SELECT id, data FROM pages`

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.queryItemsErr {
		return runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
			More: func(page azcosmos.QueryItemsResponse) bool {
				return page.ContinuationToken != nil
			},
			Fetcher: func(ctx context.Context, page *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
				return azcosmos.QueryItemsResponse{}, errors.New("Bad query result for testing")
			},
		})
	}

	if o.QueryParameters == nil {
		panic("NewQueryItemsPager: query parameters must exist")
	}
	var queryType workflow.ObjectType
	for _, p := range o.QueryParameters {
		if p.Name == "@objectType" {
			switch p.Value.(int64) {
			case int64(workflow.OTAction):
				queryType = workflow.OTAction
			case int64(workflow.OTPlan):
				queryType = workflow.OTPlan
			}
		}
	}
	if queryType == workflow.OTUnknown {
		panic(fmt.Sprintf("NewQueryItemsPager: called on query(%s) we don't support)", query))
	}

	ids := map[uuid.UUID]struct{}{}
	if o != nil && len(o.QueryParameters) > 0 {
		ids = getIDsFromQueryParameters(o.QueryParameters)
	}

	conn, err := f.pool.Take(context.Background())
	if err != nil {
		panic("can't get conn object")
	}
	defer f.pool.Put(conn)

	items := [][]byte{}
	err = sqlitex.Execute(
		conn,
		q,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var err error

				b := make([]byte, stmt.GetLen("data"))
				stmt.GetBytes("data", b)

				c, err := getCommonFields(b)
				if err != nil {
					log.Fatalf("failed to get type and id from item: %v", err)
				}
				if c.Type == queryType {
					id := c.ID
					if c.Type == workflow.OTPlan {
						id = c.PlanID
					}
					if _, ok := ids[id]; len(ids) > 0 && !ok {
						return nil
					}
					items = append(items, b)
				}
				return nil
			},
		},
	)
	if err != nil {
		panic("some type of sqlite error: " + err.Error())
	}

	return runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
		More: func(page azcosmos.QueryItemsResponse) bool {
			return page.ContinuationToken != nil
		},
		Fetcher: func(ctx context.Context, page *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
			return azcosmos.QueryItemsResponse{Items: items}, nil
		},
	})
}

type getIDer interface {
	GetID() uuid.UUID
}

func (f *fakeStorage) PatchItem(ctx context.Context, key azcosmos.PartitionKey, itemID string, po azcosmos.PatchOperations, opts *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	ops := unsafePathOps(&po)
	for _, op := range ops {
		switch op.Op {
		case "replace", "set":
			_, planID, err := f.readItem(ctx, itemID)
			if err != nil {
				return azcosmos.ItemResponse{}, err
			}

			b, _, err := f.readItem(ctx, planID)
			if err != nil {
				panic("sqlite doesn't have the plan object????")
			}
			// Leave for debugging.
			/*
				common, err := transactions.GetCommonFields(b)
				if err != nil {
					panic(err)
				}
				log.Println("plan ID was: ", common.ID.String())
			*/

			reader := reader{client: f, reg: f.reg}

			plan, err := reader.docToPlan(ctx, &azcosmos.ItemResponse{Value: b})
			if err != nil {
				panic("could not convert document to plan object: " + err.Error())
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			items := walk.Plan(ctx, plan)
			var item walk.Item
			for item = range items {
				id := item.Value.(getIDer).GetID()
				if id.String() == itemID {
					cancel()
					break
				}
			}
			if item.IsZero() {
				return azcosmos.ItemResponse{}, fmt.Errorf("%q could not be found", itemID)
			}
			f.patchObject(op, item.Value.(stateObject))

			ictx, err := planToItems(plan)
			if err != nil {
				panic(err)
			}

			for k, v := range ictx.m {
				if err := f.writeData(context.Background(), k, plan.GetID().String(), v); err != nil {
					panic(err)
				}
			}
		default:
			panic(fmt.Sprintf("we don't suport this operation yet: %s", op.Op))
		}
	}

	return azcosmos.ItemResponse{}, nil
}

type stateObject interface {
	GetState() *workflow.State
	SetState(*workflow.State)
}

func (f *fakeStorage) patchObject(op pathOps, o stateObject) {
	state := o.GetState()
	switch op.Op {
	case "set":
		switch op.Path {
		case "/attempts":
			action := o.(*workflow.Action)
			plug := f.reg.Plugin(action.Plugin)
			attempts, err := decodeAttempts(op.Value.([]byte), plug)
			if err != nil {
				panic(err)
			}
			action.Attempts = attempts
		default:
			panic(fmt.Sprintf("unsupported op Path(%s) on set op", op.Path))
		}
	case "replace":
		switch op.Path {
		case "/reason":
			plan := o.(*workflow.Plan)
			plan.Reason = op.Value.(workflow.FailureReason)
		case "/stateStatus":
			state.Status = op.Value.(workflow.Status)
		case "/stateStart":
			state.Start = op.Value.(time.Time)
		case "/stateEnd":
			state.End = op.Value.(time.Time)
		default:
			panic(fmt.Sprintf("unsupported op Path(%s) on replace op", op.Path))
		}
	default:
		panic(fmt.Sprintf("unsupported Op type(%s)", op.Op))
	}
}

type pathOps struct {
	Op    string // "add"/"replace"/"remove"/"set"/"incr"
	Path  string
	Value any
}

// unsafePathOps extracts unexported `operations` from PatchOperations because the azcosmos authors are sadists.
// Rant: come on, they have an internal mock implementation they don't expose, make it difficult to locally test with
// mock outs and love using pointers all over when they aren't necessary so my garbage collector can be full all the time.
// Blah.........
func unsafePathOps(po *azcosmos.PatchOperations) []pathOps {
	poType := reflect.TypeOf(*po)

	// Locate the `operations` field
	field, found := poType.FieldByName("operations")
	if !found {
		panic("Field 'operations' not found in PatchOperations")
	}

	// Get a pointer to `operations`
	uptr := unsafe.Pointer(po)
	operationsPtr := unsafe.Pointer(uintptr(uptr) + field.Offset)

	// Get the actual type of the `operations` field
	operationsValue := reflect.NewAt(field.Type, operationsPtr).Elem()

	// Check if it's a slice
	if operationsValue.Kind() != reflect.Slice {
		panic("Unexpected type for 'operations' field")
	}

	ops := []pathOps{}
	for i := 0; i < operationsValue.Len(); i++ {
		opValue := operationsValue.Index(i)

		// If op is a pointer, dereference it
		if opValue.Kind() == reflect.Ptr {
			opValue = opValue.Elem()
		}

		// Access fields using reflection
		opField := opValue.FieldByName("Op")
		pathField := opValue.FieldByName("Path")
		valueField := opValue.FieldByName("Value")

		// Validate extracted fields
		if opField.IsValid() && pathField.IsValid() && valueField.IsValid() {
			ops = append(
				ops,
				pathOps{
					Op:    opField.String(),
					Path:  pathField.String(),
					Value: valueField.Interface(),
				},
			)
		}
	}
	return ops
}

type batchOp struct {
	op           string
	resourceBody []byte // only on Create
	itemID       string // only on Delete
}

// unsafePathOps extracts unexported `operations` from PatchOperations because the azcosmos authors are sadists.
// Rant: come on, they have an internal mock implementation they don't expose, make it difficult to locally test with
// mock outs and love using pointers all over when they aren't necessary so my garbage collector can be full all the time.
// Blah.........
func unsafeBatchOps(t *azcosmos.TransactionalBatch) []batchOp {
	poType := reflect.TypeOf(*t)

	// Locate the `operations` field.
	field, found := poType.FieldByName("operations")
	if !found {
		panic("Field 'operations' not found in TransactionalBatch")
	}

	// Get a pointer to `operations`.
	uptr := unsafe.Pointer(t)
	operationsPtr := unsafe.Pointer(uintptr(uptr) + field.Offset)

	// Get the actual type of the `operations` field.
	operationsValue := reflect.NewAt(field.Type, operationsPtr).Elem()

	// Check if it's a slice.
	if operationsValue.Kind() != reflect.Slice {
		panic("Unexpected type for 'operations' field")
	}

	ops := []batchOp{}
	for i := 0; i < operationsValue.Len(); i++ {
		// Get the element (which is an interface)
		opValue := operationsValue.Index(i).Elem() // Extract value from interface

		// Ensure we have a valid value
		if !opValue.IsValid() {
			panic(fmt.Sprintf("Invalid value for batch operation at index %d", i))
		}

		// Ensure we are working with a pointer to the struct
		if opValue.Kind() != reflect.Ptr {
			opType := opValue.Type()
			opValuePtr := reflect.New(opType) // Allocate new struct pointer
			opValuePtr.Elem().Set(opValue)    // Copy the struct value into pointer
			opValue = opValuePtr
		}

		// Convert to struct type
		opValue = opValue.Elem() // Dereference pointer

		// Ensure the value is valid before calling Type()
		if !opValue.IsValid() {
			panic(fmt.Sprintf("Invalid opValue after dereferencing at index %d", i))
		}

		// Check type and get field
		name := opValue.Type().Name()
		switch {
		case strings.Contains(name, "batchOperationCreate"):
			field := getUnexportedField[[]byte](opValue, "resourceBody")
			ops = append(ops, batchOp{op: "Create", resourceBody: field})
		case strings.Contains(name, "batchOperationDelete"):
			field := getUnexportedField[string](opValue, "id")
			ops = append(ops, batchOp{op: "Delete", itemID: field})
		default:
			panic(fmt.Sprintf("TransactionalBatch contains an unknown operation type: %T", opValue.Interface()))
		}
	}
	return ops
}

// getUnexportedField retrieves an unexported field using unsafe.Pointer
func getUnexportedField[T any](v reflect.Value, fieldName string) T {
	// Get the field by name
	field := v.FieldByName(fieldName)

	// Ensure field exists before accessing
	if !field.IsValid() {
		panic(fmt.Sprintf("Field '%s' not found in struct %s", fieldName, v.Type()))
	}

	// Ensure the field is addressable
	if !field.CanAddr() {
		panic(fmt.Sprintf("Field '%s' is not addressable in struct %s", fieldName, v.Type()))
	}

	// Read the value using unsafe
	fieldPtr := unsafe.Pointer(field.UnsafeAddr())
	return *(*T)(fieldPtr)
}
