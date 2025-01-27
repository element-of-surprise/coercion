/*
Package cosmosdb provides a cosmosdb-based storage implementation for workflow.Plan data. This is used
to implement the storage.Vault interface.

This package is for use only by the coercion.Workstream and any use outside of that is not
supported.
*/
package cosmosdb

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/gostdlib/ops/retry/exponential"

	_ "embed"
)

// This validates that the Vault type implements the storage.Vault interface.
var _ storage.Vault = &Vault{}

// Vault implements the storage.Vault interface.
type Vault struct {
	// dbName is the cosmosdb database name for the storage.
	dbName string
	// cName is the cosmosdb container name for the storage.
	cName string
	// endpoint is the cosmosdb account endpoint
	endpoint string
	// partitionKey is the partition key for the storage.
	// This assumes the service will use a single partition.
	partitionKey string

	// todo: make this specify operation type (on update, on delete)
	enforceEtag    bool
	clientOpts     *azcosmos.ClientOptions
	containerProps azcosmos.ContainerProperties
	throughput     int32
	itemOpts       azcosmos.ItemOptions

	reader
	creator
	updater
	closer
	deleter

	private.Storage
}

var backoff *exponential.Backoff

func init() {
	var err error
	// This ensures that a custom retry policy is going to work. backoff is reusable.
	// should this be different for read and write?
	backoff, err = exponential.New(exponential.WithPolicy(plugins.SecondsRetryPolicy()))
	if err != nil {
		fatalErr(slog.Default(), "failed to create backoff policy: %v", err)
	}
}

// Option is an option for configuring a Vault.
type Option func(*Vault) error

// WithClientOptions sets client options.
// It is up to the user to make sure these don't conflict with any required options.
func WithClientOptions(opts *azcosmos.ClientOptions) Option {
	return func(r *Vault) error {
		r.clientOpts = opts
		return nil
	}
}

// WithThroughput sets container throughtput in RUs in manual mode.
func WithThroughput(throughput int32) Option {
	return func(r *Vault) error {
		r.throughput = throughput
		return nil
	}
}

// WithContainerProperties sets container properties.
// It is up to the user to make sure these don't conflict with the required properties like indexing policy.
// Any changes to container name, partition key, and indexing policy here will be overriden.
func WithContainerProperties(props azcosmos.ContainerProperties) Option {
	return func(r *Vault) error {
		r.containerProps = props
		return nil
	}
}

// WithItemOptions sets item options.
// IfMatchEtag (along with EnableContentResponseOnWrite) will be set during an operation, if appropriate.
func WithItemOptions(opts azcosmos.ItemOptions) Option {
	return func(r *Vault) error {
		r.itemOpts = opts
		return nil
	}
}

// WithEnforceETag enables enforcing etag match on update.
// We need to make sure to retry on conflict so we don't lose updates to attempts and such.
func WithEnforceETag() Option {
	return func(r *Vault) error {
		r.enforceEtag = true
		return nil
	}
}

// ContainerClient is the interface for the cosmosdb container client.
// This allows for faking the azcosmos container client.
type ContainerClient interface {
	NewQueryItemsPager(string, azcosmos.PartitionKey, *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse]
	Read(context.Context, *azcosmos.ReadContainerOptions) (azcosmos.ContainerResponse, error)
	ReadItem(context.Context, azcosmos.PartitionKey, string, *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
	PatchItem(context.Context, azcosmos.PartitionKey, string, azcosmos.PatchOperations, *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
}

// TransactionalBatch is the interface for the cosmosdb transactional batch.
type TransactionalBatch interface {
	CreateItem(item []byte, o *azcosmos.TransactionalBatchItemOptions)
	DeleteItem(itemID string, o *azcosmos.TransactionalBatchItemOptions)
}

type setters interface {
	// SetID is a setter for the ID field.
	SetID(uuid.UUID)
	// SetState is a setter for the State settings.
	SetState(*workflow.State)
}

func partitionKey(val string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(val)
}

// Client is the interface for the cosmosdb client.
type Client interface {
	// GetContainerClient returns the container client.
	GetContainerClient() ContainerClient
	// GetPK returns the partition key.
	GetPK() azcosmos.PartitionKey
	// GetPKString returns the partition key as a string.
	GetPKString() string
	// NewTransactionalBatch returns a TransactionalBatch. This allows using a fake TransactionalBatch.
	NewTransactionalBatch() TransactionalBatch
	// ExecuteTransactionalBatch executes a transactional batch.
	ExecuteTransactionalBatch(context.Context, TransactionalBatch, *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error)
	// SetBatch allows for setting the fake batch in tests.
	SetBatch(TransactionalBatch)
	// ItemOptions returns the item options.
	ItemOptions() *azcosmos.ItemOptions
	// EnforceETag returns whether to enforce etag match on update.
	EnforceETag() bool
}

// CosmosDBClient has the methods for all of Create/Update/Delete/Query operation
// on data model.
type CosmosDBClient struct {
	partitionKey string

	client *azcosmos.ContainerClient

	itemOpts    azcosmos.ItemOptions
	enforceETag bool
}

// GetContainerClient returns the container client.
func (c *CosmosDBClient) GetContainerClient() ContainerClient {
	return c.client
}

// GetPK returns the partition key.
func (c *CosmosDBClient) GetPK() azcosmos.PartitionKey {
	return partitionKey(c.partitionKey)
}

// GetPKString returns the partition key as a string.
func (c *CosmosDBClient) GetPKString() string {
	return c.partitionKey
}

// NewTransactionalBatch returns a TransactionalBatch. This allows using a fake TransactionalBatch.
func (c *CosmosDBClient) NewTransactionalBatch() TransactionalBatch {
	batch := c.client.NewTransactionalBatch(c.GetPK())
	return &batch
}

// SetBatch sets the batch for testing with the fake client, so nothing is
// needed to be done for the real client.
func (c *CosmosDBClient) SetBatch(batch TransactionalBatch) {
}

// ItemOptions returns the item options.
func (c *CosmosDBClient) ItemOptions() *azcosmos.ItemOptions {
	return &c.itemOpts
}

// EnforceETag returns whether enforcing etag match is required.
func (c *CosmosDBClient) EnforceETag() bool {
	return c.enforceETag
}

// ExecuteTransactionalBatch executes a transactional batch. This allows for faking.
// can I use generics here instead of type assertion? something would have to be pretty wrong for someone to end up with a transactional batch of the wrong type
// in production code, so it's probably fine.
func (c *CosmosDBClient) ExecuteTransactionalBatch(ctx context.Context, b TransactionalBatch, opts *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
	if b == nil {
		return azcosmos.TransactionalBatchResponse{}, fmt.Errorf("nil transactional batch")
	}
	batch := b.(*azcosmos.TransactionalBatch)
	return c.client.ExecuteTransactionalBatch(ctx, *batch, nil)
}

func pathToScalar(path string) azcosmos.IncludedPath {
	return azcosmos.IncludedPath{
		Path: fmt.Sprintf("/%s/?", path),
	}
}

// indexPaths are the included paths for the container.
var indexPaths = []azcosmos.IncludedPath{
	pathToScalar("type"),       // plans, checks, sequences, actions
	pathToScalar("groupID"),    // plans
	pathToScalar("submitTime"), // plans
	pathToScalar("key"),        // blocks, checks, sequences, actions
	pathToScalar("planID"),     // blocks, checks, sequences, actions
	// do I need a composite index? perhaps everything with planID
	pathToScalar("pos"), // actions
}

// New is the constructor for *Vault. dbName, cName, and pk are used to identify the storage container.
// If the container does not exist, it will be created.
func New(ctx context.Context, dbName, cName, pk string, cred azcore.TokenCredential, reg *registry.Register, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	r := &Vault{
		dbName:       dbName,
		cName:        cName,
		endpoint:     fmt.Sprintf("https://%s.documents.azure.com:443/", dbName),
		partitionKey: pk,
	}
	for _, o := range options {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	if r.enforceEtag {
		r.itemOpts.EnableContentResponseOnWrite = true
	}

	client, err := azcosmos.NewClient(r.endpoint, cred, r.clientOpts)
	if err != nil {
		return nil, err
	}

	cc, err := r.createContainerClient(ctx, client)
	if err != nil {
		return nil, err
	}

	mu := &sync.Mutex{}

	r.reader = reader{cName: cName, Client: cc, reg: reg}
	r.creator = creator{mu: mu, Client: cc, reader: r.reader}
	r.updater = newUpdater(mu, cc, r.reader)
	r.closer = closer{Client: cc}
	r.deleter = deleter{mu: mu, Client: cc, reader: r.reader}
	return r, nil
}

// Use this function to create a new CosmosDBClient struct.
// dbEndpoint - the Cosmos DB's https endpoint
func (v *Vault) createContainerClient(
	ctx context.Context,
	azCosmosClient *azcosmos.Client) (*CosmosDBClient, error) {

	client := &CosmosDBClient{
		partitionKey: v.partitionKey,
		itemOpts:     v.itemOpts,
		enforceETag:  v.enforceEtag,
	}

	dc, err := azCosmosClient.NewDatabase(v.dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB database client: %w", err)
	}

	exists, err := v.containerExists(ctx, azCosmosClient)
	if err != nil {
		return nil, err
	}
	if !exists {
		activityID, err := v.createContainer(ctx, dc, indexPaths)
		if err != nil {
			switch {
			case IsConflict(err):
				slog.Default().Warn(fmt.Sprintf("Container %s already exists: %s", v.cName, err))
			default:
				return nil, fmt.Errorf("failed to create Cosmos DB container: container=%s. %w", v.cName, err)
			}
		} else {
			slog.Default().Info(activityID)
		}
	}

	cc, err := azCosmosClient.NewContainer(v.dbName, v.cName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB container client: container=%s. %w", v.cName, err)
	}

	client.client = cc

	if _, err = cc.Read(ctx, nil); err != nil {
		return nil, fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			v.endpoint,
			v.cName,
			err,
		)
	}

	return client, nil
}

// createContainer creates a container. Check for existence first.
func (v *Vault) createContainer(ctx context.Context, database *azcosmos.DatabaseClient, indexPaths []azcosmos.IncludedPath) (string, error) {
	v.containerProps.ID = v.cName
	v.containerProps.PartitionKeyDefinition = azcosmos.PartitionKeyDefinition{
		Paths: []string{"/partitionKey"},
	}
	v.containerProps.IndexingPolicy = &azcosmos.IndexingPolicy{
		IncludedPaths: indexPaths,
		// exclude by default
		ExcludedPaths: []azcosmos.ExcludedPath{
			{
				Path: "/*",
			},
		},
		Automatic:    true,
		IndexingMode: azcosmos.IndexingModeConsistent,
	}

	if v.throughput == 0 {
		v.throughput = 400
	}
	throughput := azcosmos.NewManualThroughputProperties(v.throughput)
	response, err := database.CreateContainer(ctx, v.containerProps, &azcosmos.CreateContainerOptions{ThroughputProperties: &throughput})
	if err != nil {
		return "", err
	}
	return response.ActivityID, nil
}

// containerExists checks if the container exists.
func (v *Vault) containerExists(ctx context.Context, client *azcosmos.Client) (bool, error) {
	cc, err := client.NewContainer(v.dbName, v.cName)
	if err != nil {
		return false, fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			v.endpoint,
			v.cName,
			err,
		)
	}
	if _, err = cc.Read(ctx, nil); err != nil {
		if !IsNotFound(err) {
			return false, fmt.Errorf(
				"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
				v.endpoint,
				v.cName,
				err,
			)
		}
		return false, nil
	}
	return true, nil
}

// deleteContainer deletes a container. This is for testing only.
func deleteContainer(ctx context.Context, cc *azcosmos.ContainerClient) (string, error) {
	response, err := cc.Delete(ctx, &azcosmos.DeleteContainerOptions{})
	if err != nil {
		return "", err
	}
	return response.ActivityID, nil
}

// Teardown deletes a container. This is for testing only.
func Teardown(ctx context.Context, dbName, cName string, cred azcore.TokenCredential, clientOpts *azcosmos.ClientOptions) error {
	endpoint := fmt.Sprintf("https://%s.documents.azure.com:443/", dbName)

	client, err := azcosmos.NewClient(endpoint, cred, clientOpts)
	if err != nil {
		return err
	}

	cc, err := client.NewContainer(dbName, cName)
	if err != nil {
		return fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			endpoint,
			cName,
			err,
		)
	}

	if _, err := deleteContainer(ctx, cc); err != nil {
		return fmt.Errorf("failed to delete Cosmos DB container: container=%s. %w", cName, err)
	}
	return nil
}
