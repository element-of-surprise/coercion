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

// FakeCosmosDBClient has the methods for all of Create/Update/Delete/Query operations
// on coercion data.
type FakeCosmosDBClient struct {
	partitionKey string
	enforceETag  bool

	client *FakeContainerClient

	batch *FakeTransactionalBatch

	// create count per document type during ExecuteTransactionalBatch
	createCallCount map[Type]int
	createErr       error

	// delete count per document type during ExecuteTransactionalBatch
	deleteCallCount map[Type]int
	deleteErr       error
}

// NewFakeCosmosDBClient returns a new FakeCosmosDBClient.
func NewFakeCosmosDBClient(enforceETag bool) *FakeCosmosDBClient {
	documents := make(map[string][]byte)

	partitionKey := "fakePartitionKey"

	fakeContainerClient := FakeContainerClient{
		documents: documents,

		patchCallCount: map[Type]int{},
	}
	return &FakeCosmosDBClient{
		partitionKey: partitionKey,
		enforceETag:  enforceETag,
		client:       &fakeContainerClient,

		createCallCount: map[Type]int{},
		deleteCallCount: map[Type]int{},
	}
}

// GetContainerClient returns the container client.
func (c *FakeCosmosDBClient) GetContainerClient() ContainerClient {
	return c.client
}

// GetPK returns the partition key.
func (c *FakeCosmosDBClient) GetPK() azcosmos.PartitionKey {
	return partitionKey(c.partitionKey)
}

// GetPKString returns the partition key as a string.
func (c *FakeCosmosDBClient) GetPKString() string {
	return c.partitionKey
}

// NewTransactionalBatch returns a new fake TransactionalBatch.
func (c *FakeCosmosDBClient) NewTransactionalBatch() TransactionalBatch {
	// initialize maps
	return &FakeTransactionalBatch{
		createItems: map[string][]byte{},
		deleteItems: []string{},
	}
}

// SetBatch sets the batch.
func (b *FakeCosmosDBClient) SetBatch(batch TransactionalBatch) {
	b.batch = batch.(*FakeTransactionalBatch)
}

// ItemOptions returns the item options.
func (b *FakeCosmosDBClient) ItemOptions() *azcosmos.ItemOptions {
	return &azcosmos.ItemOptions{}
}

// EnforceETag returns whether enforcing etag match is required.
func (c *FakeCosmosDBClient) EnforceETag() bool {
	return c.enforceETag
}

// ExecuteTransactionalBatch executes the fake transactional batch by adding to or deleting from the documents map.
func (c *FakeCosmosDBClient) ExecuteTransactionalBatch(ctx context.Context, b TransactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
	if c.createErr != nil {
		return azcosmos.TransactionalBatchResponse{}, c.createErr
	}
	for id, item := range c.batch.createItems {
		c.client.documents[id] = item

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
		item, ok := c.client.documents[id]
		delete(c.client.documents, id)

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

// FakeContainerClient has the methods for operations on single container items.
type FakeContainerClient struct {
	mu sync.Mutex

	patchCallCount map[Type]int
	patchErr       error
	patchErrs      []error

	documents map[string][]byte

	readErr error
	listErr error
}

// NewQueryItemsPager returns a new QueryItemsPager with items from the fake cosmosdb documents.
func (cc *FakeContainerClient) NewQueryItemsPager(query string, partitionKey azcosmos.PartitionKey, o *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse] {
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
func (cc *FakeContainerClient) PatchItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, ops azcosmos.PatchOperations, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
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
func (cc *FakeContainerClient) ReadItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
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

// FakeTransactionalBatch is a fake implementation of TransactionalBatch.
type FakeTransactionalBatch struct {
	// map of id to object
	createItems map[string][]byte
	// ids to remove from documents
	deleteItems []string
}

// CreateItem adds an item to the createItems map.
func (b *FakeTransactionalBatch) CreateItem(item []byte, o *azcosmos.TransactionalBatchItemOptions) {
	c, err := getCommonFields(item)
	if err != nil {
		fatalErr(slog.Default(), "failed to get type and id from item: %v", err)
	}
	b.createItems[c.ID.String()] = item
}

// DeleteItem adds an item to the deleteItems map.
func (b *FakeTransactionalBatch) DeleteItem(itemId string, o *azcosmos.TransactionalBatchItemOptions) {
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

func dbSetup(enforceETag bool) (*Vault, *FakeCosmosDBClient) {
	container := "test-container"

	reg := registry.New()
	reg.MustRegister(&plugins.CheckPlugin{})
	reg.MustRegister(&plugins.HelloPlugin{})

	cc := NewFakeCosmosDBClient(enforceETag)
	mu := &sync.Mutex{}
	r := &Vault{
		db:           "test-db",
		container:    container,
		partitionKey: "test-partition",
	}
	r.reader = reader{container: container, Client: cc, reg: reg}
	r.creator = creator{mu: mu, Client: cc, reader: r.reader}
	r.updater = newUpdater(mu, cc, r.reader)
	r.closer = closer{Client: cc}
	r.deleter = deleter{mu: mu, Client: cc, reader: r.reader}

	return r, cc
}
