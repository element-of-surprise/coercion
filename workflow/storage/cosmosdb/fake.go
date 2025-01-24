package cosmosdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	pluglib "github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/builder"
	"github.com/element-of-surprise/coercion/workflow/storage/sqlite/testing/plugins"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// https://dev.azure.com/msazure/CloudNativeCompute/_git/aks-rp?path=/fleet/pkg/cosmosdb/testing/pager.go

//+gocover:ignore:file No need to test mock store.

var (
	ErrCosmosDBNotFound error = fmt.Errorf("cosmosdb error: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound})

	testPlanID uuid.UUID
)

var plan *workflow.Plan

func init() {
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
	// need to set to get this to match perfectly
	// plan.SubmitTime = time.Now().UTC()
	testPlanID = plan.ID

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
}

// needs to implement cosmosdb.go client interface

// FakeCosmosDBClient has the methods for all of Create/Update/Delete/Query operation
// on data model.
type FakeCosmosDBClient struct {
	partitionKey string
	enforceETag  bool

	// Need the following methods to be implemented:
	// ExecuteTransactionalBatch, NewQueryItemsPager, NewTransactionalBatch, PatchItem, Read, ReadItem
	client *FakeContainerClient

	batch *FakeTransactionalBatch

	// create count per document type during ExecuteTransactionalBatch
	createCallCount map[Type]int
	createErr       error

	// delete count per document type during ExecuteTransactionalBatch
	deleteCallCount map[Type]int
	deleteErr       error
}

type FakeContainerClient struct {
	mu sync.Mutex

	patchCallCount map[Type]int
	patchErr       error
	patchErrs      []error

	documents map[string][]byte

	listErr error
}

func NewFakeCosmosDBClient() (*FakeCosmosDBClient, error) {
	documents := make(map[string][]byte)

	partitionKey := "fakePartitionKey"

	fakeContainerClient := FakeContainerClient{
		documents: documents,

		patchCallCount: map[Type]int{},
	}
	return &FakeCosmosDBClient{
		partitionKey: partitionKey,
		enforceETag:  true,
		client:       &fakeContainerClient,

		createCallCount: map[Type]int{},
		deleteCallCount: map[Type]int{},
	}, nil
}

func (cc *FakeContainerClient) ExecuteTransactionalBatch(ctx context.Context, b azcosmos.TransactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	// Use FakeCosmosDBClient ExecuteTransactionalBatch instead.
	return azcosmos.TransactionalBatchResponse{}, nil
}

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
			// else set etag?
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

func (cc *FakeContainerClient) NewTransactionalBatch(partitionKey azcosmos.PartitionKey) azcosmos.TransactionalBatch {
	// Use FakeCosmosDBClient NewTransactionalBatch instead.
	return azcosmos.TransactionalBatch{}
}

func (cc *FakeContainerClient) PatchItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, ops azcosmos.PatchOperations, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	if cc.patchErr != nil {
		return azcosmos.ItemResponse{}, cc.patchErr
	}
	item, ok := cc.documents[itemId]
	if !ok {
		return azcosmos.ItemResponse{}, runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound})
	}
	// just get type from documents map and validate it was called?
	c, err := getCommonFields(item)
	if err != nil {
		return azcosmos.ItemResponse{}, err
	}
	cc.patchCallCount[c.Type]++
	return azcosmos.ItemResponse{}, nil
}

func (cc *FakeContainerClient) Read(ctx context.Context, o *azcosmos.ReadContainerOptions) (azcosmos.ContainerResponse, error) {
	return azcosmos.ContainerResponse{}, nil
}

func (cc *FakeContainerClient) ReadItem(ctx context.Context, partitionKey azcosmos.PartitionKey, itemId string, o *azcosmos.ItemOptions) (azcosmos.ItemResponse, error) {
	item, ok := cc.documents[itemId]
	if !ok {
		return azcosmos.ItemResponse{}, runtime.NewResponseError(&http.Response{StatusCode: http.StatusNotFound})
	}
	return azcosmos.ItemResponse{
		Value: item,
	}, nil
}

type FakeTransactionalBatch struct {
	// map of id to object
	createItems map[string][]byte
	// ids to remove from documents
	deleteItems []string
}

func (b *FakeTransactionalBatch) CreateItem(item []byte, o *azcosmos.TransactionalBatchItemOptions) {
	c, err := getCommonFields(item)
	if err != nil {
		fatalErr(slog.Default(), "failed to get type and id from item: %v", err)
	}
	b.createItems[c.ID.String()] = item
}

func (b *FakeTransactionalBatch) DeleteItem(itemId string, o *azcosmos.TransactionalBatchItemOptions) {
	b.deleteItems = append(b.deleteItems, itemId)
}

func (c *FakeCosmosDBClient) GetContainerClient() ContainerClient {
	return c.client
}

func (c *FakeCosmosDBClient) GetPK() azcosmos.PartitionKey {
	return partitionKey(c.partitionKey)
}

func (c *FakeCosmosDBClient) GetPKString() string {
	return c.partitionKey
}

func (c *FakeCosmosDBClient) NewTransactionalBatch() TransactionalBatch {
	// initialize maps
	return &FakeTransactionalBatch{
		createItems: map[string][]byte{},
		deleteItems: []string{},
	}
}

func (b *FakeCosmosDBClient) SetBatch(batch TransactionalBatch) {
	b.batch = batch.(*FakeTransactionalBatch)
}

func (b *FakeCosmosDBClient) ItemOptions() *azcosmos.ItemOptions {
	return &azcosmos.ItemOptions{}
}

// EnforceETag returns whether enforcing etag match is required.
func (c *FakeCosmosDBClient) EnforceETag() bool {
	return c.enforceETag
}

func (c *FakeCosmosDBClient) ExecuteTransactionalBatch(ctx context.Context, b TransactionalBatch, o *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
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

func mustUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id
}

func fatalErr(logger *slog.Logger, msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	logger.Error(s, "fatal", "true")
	os.Exit(1)
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
