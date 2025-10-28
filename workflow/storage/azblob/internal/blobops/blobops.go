package blobops

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/retry/exponential"
	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow/context"
)

var backoff *exponential.Backoff

func init() {
	policy := plugins.SecondsRetryPolicy()
	policy.MaxAttempts = 5
	backoff = exponential.Must(exponential.New(exponential.WithPolicy(policy)))
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
	// Returns a pager that can be used to iterate through blob listings.
	NewListBlobsFlatPager(containerName string, options *azblob.ListBlobsFlatOptions) *runtime.Pager[azblob.ListBlobsFlatResponse]
}

var _ Ops = (*Real)(nil)

// Real implements the Ops interface using the Azure Blob Storage SDK.
type Real struct {
	Client *azblob.Client
}

// DeleteBlob deletes the specified blob from the given container.
func (r *Real) DeleteBlob(ctx context.Context, containerName string, blobName string) error {
	blobClient := r.Client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	_, err := blobClient.Delete(ctx, nil)
	return err
}

// ContainerExists checks if a container exists.
func (r *Real) ContainerExists(ctx context.Context, containerName string) (bool, error) {
	containerClient := r.Client.ServiceClient().NewContainerClient(containerName)
	_, err := containerClient.GetProperties(ctx, nil)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check container existence: %w", err)
	}
	return true, nil
}

// CreateContainer creates a container if it doesn't exist.
func (r *Real) CreateContainer(ctx context.Context, containerName string) error {
	containerClient := r.Client.ServiceClient().NewContainerClient(containerName)
	_, err := containerClient.Create(ctx, nil)
	if err != nil {
		if IsConflict(err) {
			context.Log(ctx).Debug(fmt.Sprintf("container(%s) already exists", containerName))
			return nil
		}
		return fmt.Errorf("failed to create container(%s): %w", containerName, err)
	}
	return nil
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

// UploadBlob uploads a blob with retry logic.
func (r *Real) UploadBlob(ctx context.Context, containerName, blobName string, md map[string]*string, data []byte) error {
	uploadOp := func(ctx context.Context, rec exponential.Record) error {
		opts := &azblob.UploadBufferOptions{
			Metadata: md,
		}
		_, err := r.Client.UploadBuffer(ctx, containerName, blobName, data, opts)
		if err != nil {
			if !IsRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return nil
	}

	if err := backoff.Retry(context.WithoutCancel(ctx), uploadOp); err != nil {
		return fmt.Errorf("failed to upload blob %s: %w", blobName, err)
	}

	return nil
}

// NewListBlobsFlatPager creates a new pager to list blobs in a container.
func (r *Real) NewListBlobsFlatPager(containerName string, o *azblob.ListBlobsFlatOptions) *runtime.Pager[azblob.ListBlobsFlatResponse] {
	return r.Client.ServiceClient().NewContainerClient(containerName).NewListBlobsFlatPager(o)
}

// GetMetadata retrieves the metadata of a blob.
func (r *Real) GetMetadata(ctx context.Context, containerName, blobName string) (map[string]*string, error) {
	blobClient := r.Client.ServiceClient().NewContainerClient(containerName).NewBlobClient(blobName)
	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		return nil, err
	}
	return props.Metadata, nil
}

// GetBlob downloads the blob data.
func (r *Real) GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	resp, err := r.Client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download planEntry blob: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read planEntry blob: %w", err)
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

// IsRetriableError returns true if the error is a retriable error.
// Retriable errors are typically transient network or service errors.
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific blob error codes
	if bloberror.HasCode(err,
		bloberror.ServerBusy,
		bloberror.InternalError,
		bloberror.OperationTimedOut) {
		return true
	}

	// Check for HTTP status codes that indicate transient errors
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case http.StatusRequestTimeout, // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout:      // 504
			return true
		}
	}

	return false
}

// toPtr is a generic helper to get a pointer to a value.
func toPtr[T any](v T) *T {
	return &v
}
