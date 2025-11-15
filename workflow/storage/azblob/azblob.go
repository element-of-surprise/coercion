/*
Package azblob provides an Azure Blob Storage-based storage implementation for workflow.Plan data.
This is used to implement the storage.Vault interface.

When on Azure, consider using for either cost or availability reasons. SQLite on distributed storage can be a cost
effective solution. CosmosDB is a more robust solution, but it has higher costs, the Go SDK is not as mature (so we had
to do some hacks around cross partition key searches) and is not available in all regions.

Blob storage is cheap and highly available. However without transaction support, the first data write and the last one
are very expensive. It is the number of objects + 2 writes per plan and we use concurrency to make this livable.
This lets us do a kind of transaction journal that lets us recover from partial writes. Updates are cheap,
as they only touch the object that changed.

This package is for use only by the coercion.Workstream and any use outside of that is not supported.

DO NOT USE THIS PACKAGE!!!! SERIOUSLY, DO NOT USE THIS PACKAGE!!!!!
This package is internal to the workflow engine.
*/
package azblob

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/google/uuid"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
	"golang.org/x/sync/singleflight"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"

	_ "embed"
)

// This validates that the Vault type implements the storage.Vault interface.
var _ storage.Vault = &Vault{}

// Vault implements the storage.Vault interface using Azure Blob Storage.
type Vault struct {
	// prefix is the prefix for blob names and container names.
	// This is typically a cluster ID or similar identifier.
	prefix string
	// endpoint is the Azure Blob Storage account endpoint.
	// For example: https://mystorageaccount.blob.core.windows.net
	endpoint string

	client *azblob.Client
	mu     *planlocks.Group

	reader
	creator
	updater
	closer
	deleter
	recovery

	private.Storage
}

// Mark for delete
var backoff *exponential.Backoff

func init() {
	policy := plugins.SecondsRetryPolicy()
	policy.MaxAttempts = 5
	backoff = exponential.Must(exponential.New(exponential.WithPolicy(policy)))
}

// Option is an option for configuring a Vault.
type Option func(*Vault) error

// New is the constructor for *Vault. prefix is used as the container prefix and blob prefix
// to namespace blobs for this instance. endpoint is the Azure Blob Storage account endpoint
// (e.g., https://mystorageaccount.blob.core.windows.net). cred is the Azure token credential.
// reg is the coercion registry.
func New(ctx context.Context, prefix, endpoint string, cred azcore.TokenCredential, reg *registry.Register, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	if prefix == "" {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("prefix cannot be empty"))
	}
	if endpoint == "" {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("endpoint cannot be empty"))
	}
	if cred == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("credential cannot be nil"))
	}
	if reg == nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, errors.New("registry cannot be nil"))
	}

	v := &Vault{
		prefix:   prefix,
		endpoint: endpoint,
		mu:       planlocks.New(ctx),
	}

	for _, o := range options {
		if err := o(v); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
		}
	}

	client, err := azblob.NewClient(endpoint, cred, nil)
	if err != nil {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("failed to create blob client: %w", err))
	}
	v.client = client
	opsClient := &blobops.Real{Client: client}

	uploader := &uploader{
		client: opsClient,
		mu:     v.mu,
		prefix: v.prefix,
		pool:   context.Pool(ctx).Limited(20),
	}

	v.reader = reader{
		mu:           v.mu,
		readFlight:   &singleflight.Group{},
		existsFlight: &singleflight.Group{},
		prefix:       prefix,
		client:       opsClient,
		reg:          reg,
	}
	v.creator = creator{
		mu:       v.mu,
		prefix:   prefix,
		endpoint: endpoint,
		reader:   v.reader,
		uploader: uploader,
	}
	v.updater = newUpdater(v.mu, prefix, opsClient, endpoint, uploader)
	v.deleter = deleter{
		mu:     v.mu,
		prefix: prefix,
		client: opsClient,
		reader: v.reader,
	}
	v.closer = closer{}
	v.recovery = recovery{
		reader:   v.reader,
		updater:  v.updater,
		uploader: uploader,
	}

	return v, nil
}

// Teardown deletes all containers and blobs with the given prefix. This is intended for use in tests only.
func Teardown(ctx context.Context, endpoint, prefix string, cred azcore.TokenCredential) error {
	client, err := azblob.NewClient(endpoint, cred, nil)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("failed to create blob client: %w", err))
	}
	pager := client.NewListContainersPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("failed to list containers: %w", err))
		}
		for _, container := range page.ContainerItems {
			if container.Name == nil {
				continue
			}
			name := *container.Name
			if strings.HasPrefix(name, prefix) {

				if _, err := client.DeleteContainer(ctx, name, nil); err != nil {
					return errors.E(ctx, errors.CatInternal, errors.TypeStorageDelete, fmt.Errorf("failed to delete container(%s): %w", name, err))
				}
			}
		}
	}
	return nil
}

// findObjectContainer finds the container where an object blob exists.
func findObjectContainer(prefix string, obj workflow.Object) (string, error) {
	// Extract planID from the object (all objects have a planID)
	var planID uuid.UUID
	switch o := obj.(type) {
	case *workflow.Block:
		planID = o.GetPlanID()
	case *workflow.Sequence:
		planID = o.GetPlanID()
	case *workflow.Checks:
		planID = o.GetPlanID()
	case *workflow.Action:
		planID = o.GetPlanID()
	default:
		return "", fmt.Errorf("unknown object type: %T", obj)
	}

	t := time.Unix(planID.Time().UnixTime()).UTC()
	return containerName(prefix, t), nil
}

func strToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func bytesToStr(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
