/*
Package cosmosdb provides a cosmosdb-based storage implementation for workflow.Plan data. This is used
to implement the storage.Vault interface.

This package is for use only by the coercion.Workstream and any use outside of that is not
supported.

DO NOT USE THIS PACKAGE!!!! SERIOUSLY, DO NOT USE THIS PACKAGE!!!!! See notes in the file READ_FIRST.md .
*/
package cosmosdb

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/gostdlib/base/retry/exponential"

	_ "embed"
)

// This validates that the Vault type implements the storage.Vault interface.
var _ storage.Vault = &Vault{}

// Vault implements the storage.Vault interface.
type Vault struct {
	// swarm is the name of the swarm in the database.
	swarm string
	// db is the CosmosDB database name for the storage.
	db string
	// container is the CosmosDB container name for the storage.
	container string
	// endpoint is the CosmosDB account endpoint
	endpoint string

	client     *azcosmos.Client
	contClient *azcosmos.ContainerClient

	clientOpts *azcosmos.ClientOptions
	props      azcosmos.ContainerProperties
	// maxRU is the maximum throughput in RU/s that a container can be autoscaled to.
	// https://learn.microsoft.com/en-us/azure/cosmos-db/request-units
	maxRU    int32
	itemOpts azcosmos.ItemOptions

	reader
	creator
	updater
	closer
	deleter
	recovery

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

// WithMaxThroughput sets container throughput in RU/s in autoscale mode. Default is 10000.
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

// New is the constructor for *Vault. swarm is the name of the swarm in the database. This is used to group
// a set of coercion nodes together while sharing the same database and container. db is the database name that will
// be inserted in "https://%s.documents.azure.com:443/". Container is the name of the CosmosDB container.
// "cred is the Azure CosmosDB token credential. reg is the coercion registry.
// If the container does not exist, it will be created.
func New(ctx context.Context, swarm, db, container string, cred azcore.TokenCredential, reg *registry.Register, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	if swarm == "" {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("swarm name cannot be empty"))
	}
	if db == "" {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("db name cannot be empty"))
	}
	if container == "" {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("container name cannot be empty"))
	}
	if cred == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("credential cannot be nil"))
	}
	if reg == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("registry cannot be nil"))
	}

	r := &Vault{
		swarm:     swarm,
		db:        db,
		container: container,
		endpoint:  fmt.Sprintf("https://%s.documents.azure.com:443/", db),
	}
	for _, o := range options {
		if err := o(r); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
		}
	}

	// This is necessary for enforcing ETag.
	r.itemOpts.EnableContentResponseOnWrite = true

	client, err := azcosmos.NewClient(r.endpoint, cred, r.clientOpts)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeConn, err)
	}
	r.client = client
	r.contClient, err = r.createContainerClient(ctx)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageCreate, err)
	}

	mu := &sync.RWMutex{}

	r.reader = reader{
		mu:           mu,
		swarm:        swarm,
		container:    container,
		client:       r.contClient,
		defaultIOpts: &r.itemOpts,
		reg:          reg,
	}
	r.creator = creator{
		mu:     mu,
		swarm:  swarm,
		client: r.contClient,
		reader: r.reader,
	}
	r.updater = newUpdater(mu, r.contClient, &r.itemOpts)
	r.deleter = deleter{
		mu:     mu,
		client: r.contClient,
		reader: r.reader,
	}
	r.closer = closer{}
	r.recovery = recovery{reader: r.reader, updater: r.updater}
	return r, nil
}

// createContainerClient creates a new CosmosDB container client.
func (v *Vault) createContainerClient(ctx context.Context) (*azcosmos.ContainerClient, error) {
	dc, err := v.client.NewDatabase(v.db)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB database client: %w", err)
	}

	exists, err := v.containerExists(ctx, v.client)
	if err != nil {
		return nil, err
	}
	if !exists {
		activityID, err := v.createContainer(ctx, dc, indexPaths)
		if err != nil {
			switch {
			case isConflict(err):
				slog.Default().Warn(fmt.Sprintf("the container(%s) did not exist, but when we tried to create it, it said it exists: %s", v.container, err))
			default:
				return nil, fmt.Errorf("failed to create Cosmos DB container(%s): %w", v.container, err)
			}
		} else {
			slog.Default().Info(activityID)
		}
	}

	cc, err := v.client.NewContainer(v.db, v.container)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cosmos DB container client: container=%s. %w", v.container, err)
	}
	v.contClient = cc

	if _, err = cc.Read(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed to connect to Cosmos DB container(%s) at endpoint(%s): %w", v.endpoint, v.container, err)
	}

	return cc, nil
}

// createContainer creates a container. Check for existence first.
func (v *Vault) createContainer(ctx context.Context, database *azcosmos.DatabaseClient, indexPaths []azcosmos.IncludedPath) (string, error) {
	v.props.ID = v.container
	v.props.PartitionKeyDefinition = azcosmos.PartitionKeyDefinition{
		Paths: []string{"/partitionKey"},
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
		v.maxRU = 10000
	}
	throughput := azcosmos.NewAutoscaleThroughputProperties(v.maxRU)
	response, err := database.CreateContainer(ctx, v.props, &azcosmos.CreateContainerOptions{ThroughputProperties: &throughput})
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
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, err)
	}

	cc, err := client.NewContainer(db, container)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn,
			fmt.Errorf(
				"failed to connect to Cosmos DB container: endpoint=%q, container=%q. %w",
				endpoint,
				container,
				err,
			),
		)
	}

	if _, err := deleteContainer(ctx, cc); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageDelete, fmt.Errorf("failed to delete Cosmos DB container: container=%s. %w", container, err))
	}
	return nil
}

// itemOptions returns a copy of the item options.
func itemOptions(defaults *azcosmos.ItemOptions) *azcosmos.ItemOptions {
	if defaults == nil {
		return &azcosmos.ItemOptions{}
	}

	n := &azcosmos.ItemOptions{
		PreTriggers:                    make([]string, len(defaults.PreTriggers)),
		PostTriggers:                   make([]string, len(defaults.PostTriggers)),
		SessionToken:                   defaults.SessionToken,
		ConsistencyLevel:               defaults.ConsistencyLevel,
		IndexingDirective:              defaults.IndexingDirective,
		EnableContentResponseOnWrite:   defaults.EnableContentResponseOnWrite,
		IfMatchEtag:                    defaults.IfMatchEtag,
		DedicatedGatewayRequestOptions: defaults.DedicatedGatewayRequestOptions,
	}
	copy(n.PreTriggers, defaults.PreTriggers)
	copy(n.PostTriggers, defaults.PostTriggers)
	return n
}

func pathToScalar(path string) azcosmos.IncludedPath {
	return azcosmos.IncludedPath{
		Path: fmt.Sprintf("/%s/?", path),
	}
}

// indexPaths are the included paths for the container.
var indexPaths = []azcosmos.IncludedPath{
	pathToScalar("swarm"),      // plans, checks, sequences, actions
	pathToScalar("type"),       // plans, checks, sequences, actions
	pathToScalar("groupID"),    // plans
	pathToScalar("submitTime"), // plans
	pathToScalar("key"),        // blocks, checks, sequences, actions
	pathToScalar("planID"),     // blocks, checks, sequences, actions
	pathToScalar("pos"),        // actions
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

func fatalErr(logger *slog.Logger, msg string, args ...any) {
	s := fmt.Sprintf(msg, args...)
	logger.Error(s, "fatal", "true")
	os.Exit(1)
}
