package cosmosdb

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"

	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

//+gocover:ignore:file No need to test fake store.

var (
	plan *workflow.Plan

	// test with multiple plans in storage
	plan1 *workflow.Plan
	plan2 *workflow.Plan
)

func init() {
	plan = NewTestPlan()

	// test with multiple plans in storage
	plan1 = NewTestPlan()
	plan2 = NewTestPlan()
}

type setters interface {
	// SetID is a setter for the ID field.
	SetID(uuid.UUID)
	// SetState is a setter for the State settings.
	SetState(*workflow.State)
}

func NewTestPlan() *workflow.Plan {
	var plan *workflow.Plan
	ctx := context.Background()

	build, err := builder.New("test", "test", builder.WithGroupID(mustUUID()))
	if err != nil {
		panic(err)
	}

	checkAction1 := &workflow.Action{Name: "action1", Descr: "preCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction2 := &workflow.Action{Name: "action2", Descr: "contCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction3 := &workflow.Action{Name: "action3", Descr: "postCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction4 := &workflow.Action{Name: "action4", Descr: "deferredCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	checkAction5 := &workflow.Action{Name: "action5", Descr: "bypassCheckAction", Plugin: plugins.CheckPluginName, Req: nil}
	seqAction1 := &workflow.Action{
		Name:   "action",
		Descr:  "action",
		Plugin: plugins.HelloPluginName,
		Req:    plugins.HelloReq{Say: "hello"},
		Attempts: []*workflow.Attempt{
			{
				Err:   &pluglib.Error{Message: "internal error"},
				Start: time.Now().Add(-1 * time.Minute).UTC(),
				End:   time.Now().UTC(),
			},
			{
				Resp:  plugins.HelloResp{Said: "hello"},
				Start: time.Now().Add(-1 * time.Second).UTC(),
				End:   time.Now().UTC(),
			},
		},
	}

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction1))
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 32 * time.Second})
	build.AddAction(clone.Action(ctx, checkAction2))
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction3))
	build.Up()

	build.AddChecks(builder.DeferredChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction4))
	build.Up()

	build.AddChecks(builder.BypassChecks, &workflow.Checks{})
	build.AddAction(clone.Action(ctx, checkAction5))
	build.Up()

	build.AddBlock(builder.BlockArgs{
		Name:              "block",
		Descr:             "block",
		EntranceDelay:     1 * time.Second,
		ExitDelay:         1 * time.Second,
		ToleratedFailures: 1,
		Concurrency:       1,
	})

	build.AddChecks(builder.PreChecks, &workflow.Checks{})
	build.AddAction(checkAction1)
	build.Up()

	build.AddChecks(builder.ContChecks, &workflow.Checks{Delay: 1 * time.Minute})
	build.AddAction(checkAction2)
	build.Up()

	build.AddChecks(builder.PostChecks, &workflow.Checks{})
	build.AddAction(checkAction3)
	build.Up()

	build.AddChecks(builder.DeferredChecks, &workflow.Checks{})
	build.AddAction(checkAction4)
	build.Up()

	build.AddChecks(builder.BypassChecks, &workflow.Checks{})
	build.AddAction(checkAction5)
	build.Up()

	build.AddSequence(&workflow.Sequence{Name: "sequence", Descr: "sequence"})
	build.AddAction(seqAction1)
	build.Up()

	plan, err = build.Plan()
	if err != nil {
		panic(err)
	}

	for item := range walk.Plan(context.Background(), plan) {
		setter := item.Value.(setters)
		setter.SetID(mustUUID())
		setter.SetState(
			&workflow.State{
				Status: workflow.Running,
				Start:  time.Now().UTC(),
				End:    time.Now().UTC(),
			},
		)
	}
	return plan
}

// needs to implement cosmosdb.go client interface

// fakeContainerClient has the methods for all of Create/Update/Delete/Query operations
// on coercion data.
type fakeContainerClient struct {
	partitionVal string

	reader  *fakeContainerReader
	updater *fakeContainerUpdater

	batch *fakeTransactionalBatch

	// create count per document type during ExecuteTransactionalBatch
	createCallCount map[Type]int
	createErr       error

	// delete count per document type during ExecuteTransactionalBatch
	deleteCallCount map[Type]int
	deleteErr       error
}

// newFakeCosmosDBClient returns a new FakeCosmosDBClient.
func newFakeCosmosDBClient() *fakeContainerClient {
	documents := make(map[string][]byte)

	partitionVal := "fakePartitionVal"

	fakeContainerReader := fakeContainerReader{
		documents: documents,
	}
	fakeContainerUpdater := fakeContainerUpdater{
		// need to somehow share the documents map
		documents: documents,

		patchCallCount: map[Type]int{},
	}
	return &fakeContainerClient{
		partitionVal: partitionVal,
		reader:       &fakeContainerReader,
		updater:      &fakeContainerUpdater,

		createCallCount: map[Type]int{},
		deleteCallCount: map[Type]int{},
	}
}

// getReader returns the container client.
func (c *fakeContainerClient) getReader() containerReader {
	return c.reader
}

// getUpdater returns the container client.
func (c *fakeContainerClient) getUpdater() containerUpdater {
	return c.updater
}

// getPK returns the partition key.
func (c *fakeContainerClient) getPK() azcosmos.PartitionKey {
	return pk(c.partitionVal)
}

// getPKString returns the partition key as a string.
func (c *fakeContainerClient) getPKString() string {
	return c.partitionVal
}

// newTransactionalBatch returns a new fake TransactionalBatch.
func (c *fakeContainerClient) newTransactionalBatch() transactionalBatch {
	// initialize maps
	return &fakeTransactionalBatch{
		createItems: map[string][]byte{},
		deleteItems: []string{},
	}
}

// SetBatch sets the batch.
func (b *fakeContainerClient) setBatch(batch transactionalBatch) {
	b.batch = batch.(*fakeTransactionalBatch)
}

// itemOptions returns the item options.
func (b *fakeContainerClient) itemOptions() *azcosmos.ItemOptions {
	return &azcosmos.ItemOptions{}
}

// ExecuteTransactionalBatch executes the fake transactional batch by adding to or deleting from the documents map.
func (c *fakeContainerClient) executeTransactionalBatch(ctx context.Context, b transactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
	if c.createErr != nil {
		return azcosmos.TransactionalBatchResponse{}, c.createErr
	}
	for id, item := range c.batch.createItems {
		c.reader.documents[id] = item
		c.updater.documents[id] = item

		fields, err := getCommonFields(item)
		if err != nil {
			return azcosmos.TransactionalBatchResponse{}, err
		}
		c.createCallCount[fields.Type]++
	}
	// clear create items
	c.batch.createItems = map[string][]byte{}

	if c.deleteErr != nil {
		return azcosmos.TransactionalBatchResponse{}, c.deleteErr
	}
	for _, id := range c.batch.deleteItems {
		item, ok := c.reader.documents[id]
		delete(c.reader.documents, id)
		delete(c.updater.documents, id)

		if ok {
			fields, err := getCommonFields(item)
			if err != nil {
				return azcosmos.TransactionalBatchResponse{}, err
			}
			c.deleteCallCount[fields.Type]++
		}
	}
	// clear delete items
	c.batch.deleteItems = []string{}

	return azcosmos.TransactionalBatchResponse{}, nil
}

// fakeContainerReader has the methods for read operations on single container items.
type fakeContainerReader struct {
	mu sync.RWMutex

	documents map[string][]byte

	readErr error
	listErr error
}

// fakeContainerUpdater has the methods for update operations on single container items.
type fakeContainerUpdater struct {
	mu sync.RWMutex

	documents map[string][]byte

	patchCallCount map[Type]int
	patchErr       error
}

// NewQueryItemsPager returns a new QueryItemsPager with items from the fake cosmosdb documents.
func (cc *fakeContainerReader) NewQueryItemsPager(query string, pk azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
	// convert map of documents to slice
	queryItems := [][]byte{}
	queryType := Plan
	if query == fetchActionsByID {
		queryType = Action
	}
	ids := map[uuid.UUID]struct{}{}
	if o != nil && len(o.QueryParameters) > 0 {
		ids = getIDsFromQueryParameters(o.QueryParameters)
	}
	for _, item := range cc.documents {
		c, err := getCommonFields(item)
		if err != nil {
			fatalErr(slog.Default(), "failed to get type and id from item: %v", err)
		}
		if c.Type == queryType {
			if _, ok := ids[c.ID]; len(ids) > 0 && !ok {
				continue
			}
			queryItems = append(queryItems, item)
		}
	}

	return runtime.NewPager(runtime.PagingHandler[azcosmos.QueryItemsResponse]{
		More: func(page azcosmos.QueryItemsResponse) bool {
			return page.ContinuationToken != nil
		},
		Fetcher: func(ctx context.Context, page *azcosmos.QueryItemsResponse) (azcosmos.QueryItemsResponse, error) {
			if cc.listErr != nil {
				return azcosmos.QueryItemsResponse{}, errors.New("Bad query result for testing")
			}
			return azcosmos.QueryItemsResponse{Items: queryItems}, nil
		},
	})
}

func getIDsFromQueryParameters(params []azcosmos.QueryParameter) map[uuid.UUID]struct{} {
	for _, param := range params {
		if param.Name != "@ids" {
			continue
		}
		ids := map[uuid.UUID]struct{}{}
		for _, id := range param.Value.([]uuid.UUID) {
			ids[id] = struct{}{}
		}
		return ids
	}
	return nil
}

// PatchItem increments the patchCallCount for the item type.
func (cc *fakeContainerUpdater) PatchItem(ctx context.Context, pk azcosmos.PartitionKey, itemId string, ops azcosmos.PatchOperations, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	if cc.patchErr != nil {
		return azcosmos.ItemResponse{}, cc.patchErr
	}
	item, ok := cc.documents[itemId]
	if !ok {
		return azcosmos.ItemResponse{}, runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound})
	}
	// Don't bother to actually patch the item. Just get type from documents map and validate it was called.
	c, err := getCommonFields(item)
	if err != nil {
		return azcosmos.ItemResponse{}, err
	}
	cc.patchCallCount[c.Type]++
	return azcosmos.ItemResponse{}, nil
}

// ReadItem returns the item from the documents map.
func (cc *fakeContainerReader) ReadItem(ctx context.Context, pk azcosmos.PartitionKey, itemId string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	if cc.readErr != nil {
		return azcosmos.ItemResponse{}, cc.readErr
	}
	item, ok := cc.documents[itemId]
	if !ok {
		return azcosmos.ItemResponse{}, runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound})
	}
	return azcosmos.ItemResponse{
		Value: item,
	}, nil
}

// fakeTransactionalBatch is a fake implementation of TransactionalBatch.
type fakeTransactionalBatch struct {
	// map of id to object
	createItems map[string][]byte
	// ids to remove from documents
	deleteItems []string
}

// CreateItem adds an item to the createItems map.
func (b *fakeTransactionalBatch) CreateItem(item []byte, o *azcosmos.TransactionalBatchItemOptions) {
	c, err := getCommonFields(item)
	if err != nil {
		fatalErr(slog.Default(), "failed to get type and id from item: %v", err)
	}
	b.createItems[c.ID.String()] = item
}

// DeleteItem adds an item to the deleteItems map.
func (b *fakeTransactionalBatch) DeleteItem(itemId string, o *azcosmos.TransactionalBatchItemOptions) {
	b.deleteItems = append(b.deleteItems, itemId)
}

func mustUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id
}

type commonFields struct {
	ID          uuid.UUID       `json:"id"`
	Type        Type            `json:"type"`
	StateStatus workflow.Status `json:"stateStatus"`
	StateStart  time.Time       `json:"stateStart"`
	StateEnd    time.Time       `json:"stateEnd"`
}

func getCommonFields(data []byte) (commonFields, error) {
	var c commonFields
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

func dbSetup() (*Vault, *fakeContainerClient) {
	container := "test-container"

	reg := registry.New()
	reg.MustRegister(&plugins.CheckPlugin{})
	reg.MustRegister(&plugins.HelloPlugin{})

	cc := newFakeCosmosDBClient()
	mu := &sync.RWMutex{}
	r := &Vault{
		db:        "test-db",
		container: container,
		pkVal:     "test-partition",
	}
	r.reader = reader{container: container, client: cc, reg: reg}
	r.creator = creator{mu: mu, client: cc, reader: r.reader}
	r.updater = newUpdater(mu, cc, r.reader)
	r.closer = closer{client: cc}
	r.deleter = deleter{mu: mu, client: cc, reader: r.reader}

	return r, cc
}
