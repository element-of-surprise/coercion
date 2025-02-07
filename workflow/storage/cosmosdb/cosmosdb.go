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
	"os"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/retry/exponential"

	_ "embed"
)

// This validates that the Vault type implements the storage.Vault interface.
var _ storage.Vault = &Vault{}

// Vault implements the storage.Vault interface.
type Vault struct {
	// db is the CosmosDB database name for the storage.
	db string
	// container is the CosmosDB container name for the storage.
	container string
	// endpoint is the CosmosDB account endpoint
	endpoint string
	// pkVal is the partition key value for the storage.
	// This assumes the service will use a single partition.
	pkVal string

	clientOpts *azcosmos.ClientOptions
	props      azcosmos.ContainerProperties
	// maxRU is the maximum throughput in RU/s.
	// https://learn.microsoft.com/en-us/azure/cosmos-db/request-units
	maxRU    int32
	itemOpts azcosmos.ItemOptions

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
	policy := plugins.SecondsRetryPolicy()
	policy.MaxAttempts = 5
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

// WithMaxThroughput sets container throughtput in RU/s in manual mode. Default is 400.
func WithMaxThroughput(maxRU int32) Option {
	return func(r *Vault) error {
		r.maxRU = maxRU
		return nil
	}
}

// WithContainerProperties sets container properties.
// It is up to the user to make sure these don't conflict with the required properties like indexing policy.
// Any changes to container name, partition key, and indexing policy here will be overriden.
func WithContainerProperties(props azcosmos.ContainerProperties) Option {
	return func(r *Vault) error {
		r.props = props
		return nil
	}
}

// WithItemOptions sets item options. This is used for reads and updates.
// IfMatchEtag (along with EnableContentResponseOnWrite) will be set during an operation, if appropriate.
func WithItemOptions(opts azcosmos.ItemOptions) Option {
	return func(r *Vault) error {
		r.itemOpts = opts
		return nil
	}
}

type batcher interface {
	// newTransactionalBatch returns a TransactionalBatch. This allows using a fake TransactionalBatch.
	newTransactionalBatch() transactionalBatch
	// executeTransactionalBatch executes a transactional batch.
	executeTransactionalBatch(context.Context, transactionalBatch, *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error)
	// setBatch allows for setting the fake batch in tests.
	setBatch(transactionalBatch)
}

// ContainerClient is the interface for the CosmosDB container client.
// This allows for faking the azcosmos container client.
type containerReader interface {
	NewQueryItemsPager(string, azcosmos.PartitionKey, *azcosmos.QueryOptions) *runtime.Pager[azcosmos.QueryItemsResponse]
	ReadItem(context.Context, azcosmos.PartitionKey, string, *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
}

// ContainerClient is the interface for the CosmosDB container client.
// This allows for faking the azcosmos container client.
type containerUpdater interface {
	PatchItem(context.Context, azcosmos.PartitionKey, string, azcosmos.PatchOperations, *azcosmos.ItemOptions) (azcosmos.ItemResponse, error)
}

// transactionalBatch is the interface for the CosmosDB transactional batch.
// This is used for creating and deleting plans.
type transactionalBatch interface {
	CreateItem(item []byte, o *azcosmos.TransactionalBatchItemOptions)
	DeleteItem(itemID string, o *azcosmos.TransactionalBatchItemOptions)
}

func pk(val string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(val)
}

// Client is the interface for the CosmosDB client.
type client interface {
	// getReader returns the container client.
	getReader() containerReader
	// getUpdater returns the container client.
	getUpdater() containerUpdater
	// getPK returns the partition key.
	getPK() azcosmos.PartitionKey
	// getPKString returns the partition key as a string.
	getPKString() string
	// itemOptions returns the item options.
	itemOptions() *azcosmos.ItemOptions
	// The client must implement batcher. Some of the transactional batch methods
	// are difficult to fake otherwise.
	batcher
}

var _ client = &containerClient{}

// containerClient has the methods for all of Create/Update/Delete/Query operations
// on the CosmosDB data.
type containerClient struct {
	pkVal    string
	client   *azcosmos.ContainerClient
	itemOpts azcosmos.ItemOptions
}

// getReader returns the container client.
func (c *containerClient) getReader() containerReader {
	return c.client
}

// getUpdater returns the container client.
func (c *containerClient) getUpdater() containerUpdater {
	return c.client
}

// getPK returns the partition key.
func (c *containerClient) getPK() azcosmos.PartitionKey {
	return pk(c.pkVal)
}

// getPKString returns the partition key as a string.
func (c *containerClient) getPKString() string {
	return c.pkVal
}

// newTransactionalBatch returns a transactionalBatch. This allows using a fake TransactionalBatch.
func (c *containerClient) newTransactionalBatch() transactionalBatch {
	batch := c.client.NewTransactionalBatch(c.getPK())
	return &batch
}

// setBatch sets the batch for testing with the fake client, so nothing is
// needed to be done for the real client.
func (c *containerClient) setBatch(batch transactionalBatch) {
}

// itemOptions returns the item options.
func (c *containerClient) itemOptions() *azcosmos.ItemOptions {
	return &c.itemOpts
}

// executeTransactionalBatch executes a transactional batch. This allows for faking by accepting the transactionalBatch
// interface. This is only used internally, so asserting type here should be fine.
func (c *containerClient) executeTransactionalBatch(ctx context.Context, b transactionalBatch, opts *azcosmos.TransactionalBatchOptions) (azcosmos.TransactionalBatchResponse, error) {
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
	pathToScalar("pos"),        // actions
}

// New is the constructor for *Vault. db, container, and pval are used to identify the storage container.
// If the container does not exist, it will be created.
// The partition key has a set key name, so users should decide what partition key value means to them depending on
// their architecture.
func New(ctx context.Context, db, container, pval string, cred azcore.TokenCredential, reg *registry.Register, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	r := &Vault{
		db:        db,
		container: container,
		endpoint:  fmt.Sprintf("https://%s.documents.azure.com:443/", db),
		pkVal:     pval,
	}
	for _, o := range options {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	// This is necessary for enforcing ETag.
	r.itemOpts.EnableContentResponseOnWrite = true

	client, err := azcosmos.NewClient(r.endpoint, cred, r.clientOpts)
	if err != nil {
		return nil, err
	}

	cc, err := r.createContainerClient(ctx, client)
	if err != nil {
		return nil, err
	}

	mu := &sync.RWMutex{}

	r.reader = reader{container: container, client: cc, reg: reg}
	r.creator = creator{mu: mu, client: cc, reader: r.reader}
	r.updater = newUpdater(mu, cc, r.reader)
	r.closer = closer{client: cc}
	r.deleter = deleter{mu: mu, client: cc, reader: r.reader}
	return r, nil
}

// createContainerClient creates a new CosmosDB container client.
func (v *Vault) createContainerClient(
	ctx context.Context,
	azCosmosClient *azcosmos.Client) (*containerClient, error) {
	if azCosmosClient == nil {
		return nil, fmt.Errorf("azCosmosClient cannot be nil")
	}

	client := &containerClient{
		pkVal:    v.pkVal,
		itemOpts: v.itemOpts,
	}

	dc, err := azCosmosClient.NewDatabase(v.db)
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
			case isConflict(err):
				slog.Default().Warn(fmt.Sprintf("Container %s already exists: %s", v.container, err))
			default:
				return nil, fmt.Errorf("failed to create Cosmos DB container: container=%s. %w", v.container, err)
			}
		} else {
			slog.Default().Info(activityID)
		}
	}

	cc, err := azCosmosClient.NewContainer(v.db, v.container)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB container client: container=%s. %w", v.container, err)
	}

	client.client = cc

	if _, err = cc.Read(ctx, nil); err != nil {
		return nil, fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			v.endpoint,
			v.container,
			err,
		)
	}

	return client, nil
}

// createContainer creates a container. Check for existence first.
func (v *Vault) createContainer(ctx context.Context, database *azcosmos.DatabaseClient, indexPaths []azcosmos.IncludedPath) (string, error) {
	v.props.ID = v.container
	v.props.PartitionKeyDefinition = azcosmos.PartitionKeyDefinition{
		Paths: []string{"/pk"},
	}
	v.props.IndexingPolicy = &azcosmos.IndexingPolicy{
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

	if v.maxRU == 0 {
		v.maxRU = 400
	}
	throughput := azcosmos.NewAutoscaleThroughputProperties(v.maxRU)
	response, err := database.CreateContainer(ctx, v.props, &azcosmos.CreateContainerOptions{ThroughputProperties: &throughput})
	if err != nil {
		return "", err
	}
	return response.ActivityID, nil
}

// containerExists checks if the container exists.
func (v *Vault) containerExists(ctx context.Context, client *azcosmos.Client) (bool, error) {
	cc, err := client.NewContainer(v.db, v.container)
	if err != nil {
		return false, fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			v.endpoint,
			v.container,
			err,
		)
	}
	if _, err = cc.Read(ctx, nil); err != nil {
		if !isNotFound(err) {
			return false, fmt.Errorf(
				"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
				v.endpoint,
				v.container,
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

// Teardown deletes a container from a given CosmosDB database. This is for testing only.
func Teardown(ctx context.Context, db, container string, cred azcore.TokenCredential, clientOpts *azcosmos.ClientOptions) error {
	endpoint := fmt.Sprintf("https://%s.documents.azure.com:443/", db)

	client, err := azcosmos.NewClient(endpoint, cred, clientOpts)
	if err != nil {
		return err
	}

	cc, err := client.NewContainer(db, container)
	if err != nil {
		return fmt.Errorf(
			"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
			endpoint,
			container,
			err,
		)
	}

	if _, err := deleteContainer(ctx, cc); err != nil {
		return fmt.Errorf("failed to delete Cosmos DB container: container=%s. %w", container, err)
	}
	return nil
}

func fatalErr(logger *slog.Logger, msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	logger.Error(s, "fatal", "true")
	os.Exit(1)
}
