package blobops

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/gostdlib/base/values/sizes"

	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
)

// maxRetries bounds how many times a blob request is retried after its first try. With a 1s delay doubling to a 60s cap
// the waits between tries add up to roughly seven minutes, which rides out account-level throttling (503 ServerBusy)
// instead of failing a write part way through a multi-blob update. Each try can also run up to tryTimeout, so a request
// against a server that never answers takes about half an hour (12 tries x 2m plus the waits) before it fails. Writes
// are not cancelled by their callers, so that is how long one can hold a plan's write lock; reads are additionally
// bounded by the shared fetch's own timeout.
const maxRetries = 11

// bodyRetries bounds how many times a download whose body read fails part way is resumed. Each resume is a new request
// that goes through the client's full retry policy, so this is kept small: retrying it maxRetries times as well would
// multiply the time a stalled read can take.
const bodyRetries = 2

// tryTimeout bounds a single try of a blob request. The default transport has no response timeout, so without it a
// server that accepts a request and never answers would hold it, and any plan lock or pool slot above it, forever;
// writes and shared reads run on contexts that are never cancelled. It is generous enough for a full plan object.
const tryTimeout = 2 * time.Minute

// MaxMetadataSize is the most metadata, names and values together, Azure Blob Storage accepts for one blob.
const MaxMetadataSize = 8 * sizes.KiB

// ValidMetadataValue reports whether s can be a blob metadata value. Metadata is sent as HTTP headers, and Azure keeps
// a value as is only if it is printable ASCII with no leading or trailing whitespace, which HTTP trims.
func ValidMetadataValue(s string) bool {
	if strings.TrimSpace(s) != s {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ClientOptions returns the options the blob client must be built with. Their retry policy is the only retrying done
// for blob requests: the SDK applies it to every request, retrying transport errors and the throttling and server
// error statuses (408, 429, 500, 502, 503, 504), honoring Retry-After, and stopping when the request's context ends.
// Each try is bounded by tryTimeout and a try that runs out is retried. The operations on Real do not retry again on
// top of it.
func ClientOptions() *azblob.ClientOptions {
	return &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries:    maxRetries,
				TryTimeout:    tryTimeout,
				RetryDelay:    time.Second,
				MaxRetryDelay: time.Minute,
			},
		},
	}
}

// final marks err from a request the client's retry policy is done with. Retrying again would only repeat what the
// policy already did, so the error is permanent, unless ctx ended: the caller gave up, which a later try need not.
func final(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	return fmt.Errorf("%w: %w", err, errors.ErrPermanent)
}

// Ops defines operations for interacting with Azure Blob Storage.
type Ops interface {
	// CreateContainer creates a container.
	CreateContainer(ctx context.Context, containerName string) error
	// EnsureContainer creates a container if it doesn't exist.
	EnsureContainer(ctx context.Context, containerName string) error
	// ContainerExists checks if a container exists.
	ContainerExists(ctx context.Context, containerName string) (bool, error)
	// UploadBlob uploads a blob with the given metadata and data. md can be nil.
	UploadBlob(ctx context.Context, containerName, blobName string, md map[string]*string, data []byte) error
	// DeleteBlob deletes the specified blob from the given container.
	DeleteBlob(ctx context.Context, containerName string, blobName string) error
	// GetMetadata retrieves the metadata of a blob.
	GetMetadata(ctx context.Context, containerName, blobName string) (map[string]*string, error)
	// GetBlob downloads the blob data.
	GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error)

	// NewListBlobsFlatPager creates a pager for listing blobs in a container.
	// Returns a pager that can be used to iterate through blob listings. Fetch pages with NextListPage.
	NewListBlobsFlatPager(containerName string, options *azblob.ListBlobsFlatOptions) *runtime.Pager[azblob.ListBlobsFlatResponse]
	// NextListPage fetches the next page from a pager made by NewListBlobsFlatPager. It does not retry: for Real, the
	// client's retry policy (see ClientOptions) retries the request.
	NextListPage(ctx context.Context, pager *runtime.Pager[azblob.ListBlobsFlatResponse]) (azblob.ListBlobsFlatResponse, error)
}

var _ Ops = (*Real)(nil)

// Real implements the Ops interface using the Azure Blob Storage SDK.
type Real struct {
	// Client must be built with ClientOptions, which carry the retry policy for every operation.
	Client *azblob.Client
}

// DeleteBlob deletes the specified blob from the given container. The delete is not abandoned when ctx ends.
func (r *Real) DeleteBlob(ctx context.Context, containerName string, blobName string) error {
	ctx = context.WithoutCancel(ctx)
	blobClient := r.Client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	if _, err := blobClient.Delete(ctx, nil); err != nil {
		return final(ctx, err)
	}
	return nil
}

// ContainerExists checks if a container exists.
func (r *Real) ContainerExists(ctx context.Context, containerName string) (bool, error) {
	containerClient := r.Client.ServiceClient().NewContainerClient(containerName)
	_, err := containerClient.GetProperties(ctx, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	}
	return false, final(ctx, err)
}

// CreateContainer creates a container if it doesn't exist. The create is not abandoned when ctx ends.
func (r *Real) CreateContainer(ctx context.Context, containerName string) error {
	ctx = context.WithoutCancel(ctx)
	containerClient := r.Client.ServiceClient().NewContainerClient(containerName)
	_, err := containerClient.Create(ctx, nil)
	switch {
	case err == nil:
		return nil
	case IsConflict(err):
		context.Log(ctx).Debug(fmt.Sprintf("container(%s) already exists", containerName))
		return nil
	}
	return final(ctx, fmt.Errorf("failed to create container(%s): %w", containerName, err))
}

// EnsureContainer creates a container if it doesn't exist.
func (r *Real) EnsureContainer(ctx context.Context, containerName string) error {
	exists, err := r.ContainerExists(ctx, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return r.CreateContainer(ctx, containerName)
	}
	return nil
}

// UploadBlob uploads a blob. The upload is not abandoned when ctx ends.
func (r *Real) UploadBlob(ctx context.Context, containerName, blobName string, md map[string]*string, data []byte) error {
	ctx = context.WithoutCancel(ctx)
	opts := &azblob.UploadBufferOptions{
		Metadata: md,
	}
	if _, err := r.Client.UploadBuffer(ctx, containerName, blobName, data, opts); err != nil {
		return final(ctx, fmt.Errorf("failed to upload blob %s: %w", blobName, err))
	}
	return nil
}

// NewListBlobsFlatPager creates a new pager to list blobs in a container.
func (r *Real) NewListBlobsFlatPager(containerName string, o *azblob.ListBlobsFlatOptions) *runtime.Pager[azblob.ListBlobsFlatResponse] {
	return r.Client.ServiceClient().NewContainerClient(containerName).NewListBlobsFlatPager(o)
}

// NextListPage fetches the next page from pager. A not found error (the container does not exist) is not retried.
// Unlike writes, listing is safe to abandon, so retries stop when ctx ends: a caller such as startup recovery can give
// up instead of being held for the whole retry budget while the account is throttled.
func (r *Real) NextListPage(ctx context.Context, pager *runtime.Pager[azblob.ListBlobsFlatResponse]) (azblob.ListBlobsFlatResponse, error) {
	page, err := pager.NextPage(ctx)
	if err != nil {
		return azblob.ListBlobsFlatResponse{}, final(ctx, err)
	}
	return page, nil
}

// GetMetadata retrieves the metadata of a blob.
func (r *Real) GetMetadata(ctx context.Context, containerName, blobName string) (map[string]*string, error) {
	blobClient := r.Client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		return nil, final(ctx, fmt.Errorf("failed to get metadata for blob(%s/%s): %w", containerName, blobName, err))
	}
	return props.Metadata, nil
}

// GetBlob downloads the blob data. A body read that fails part way is resumed from where it failed, up to bodyRetries
// times.
func (r *Real) GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	resp, err := r.Client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, final(ctx, fmt.Errorf("failed to download blob(%s/%s): %w", containerName, blobName, err))
	}
	body := resp.NewRetryReader(ctx, &blob.RetryReaderOptions{MaxRetries: bodyRetries})
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, final(ctx, fmt.Errorf("failed to read blob(%s/%s): %w", containerName, blobName, err))
	}
	return data, nil
}

// IsNotFound returns true if the error is a not found error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound)
}

// IsConflict returns true if the error is a conflict error (already exists).
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	return bloberror.HasCode(err, bloberror.ContainerAlreadyExists, bloberror.BlobAlreadyExists)
}

// toPtr is a generic helper to get a pointer to a value.
func toPtr[T any](v T) *T {
	return &v
}
