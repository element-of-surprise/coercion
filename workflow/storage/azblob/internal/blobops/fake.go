package blobops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
)

// Fake implements the Ops interface for testing purposes.
// It stores blobs in memory and can be configured to return errors.
type Fake struct {
	mu sync.RWMutex

	// containers stores container data: containerName -> blobName -> blobData
	containers map[string]map[string]*blobData

	// Error injection functions - if set, these will be called and their error returned
	CreateContainerErr func(containerName string) error
	EnsureContainerErr func(containerName string) error
	ContainerExistsErr func(containerName string) error
	UploadBlobErr      func(containerName, blobName string) error
	DeleteBlobErr      func(containerName, blobName string) error
	GetMetadataErr     func(containerName, blobName string) error
	GetBlobErr         func(containerName, blobName string) error
	// NextListPageErr is called on each NextListPage call. Returning an error fails that call.
	NextListPageErr func(containerName string) error
}

type blobData struct {
	metadata map[string]*string
	data     []byte
}

var _ Ops = (*Fake)(nil)

// NewFake creates a new fake blobops client.
func NewFake() *Fake {
	return &Fake{
		containers: make(map[string]map[string]*blobData),
	}
}

// CreateContainer creates a container.
func (f *Fake) CreateContainer(ctx context.Context, containerName string) error {
	if f.CreateContainerErr != nil {
		if err := f.CreateContainerErr(containerName); err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.containers[containerName]; exists {
		// Return conflict error if container already exists
		return &azcore.ResponseError{ErrorCode: string(bloberror.ContainerAlreadyExists)}
	}

	f.containers[containerName] = make(map[string]*blobData)
	return nil
}

// EnsureContainer creates a container if it doesn't exist.
func (f *Fake) EnsureContainer(ctx context.Context, containerName string) error {
	if f.EnsureContainerErr != nil {
		if err := f.EnsureContainerErr(containerName); err != nil {
			return err
		}
	}

	exists, err := f.ContainerExists(ctx, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return f.CreateContainer(ctx, containerName)
	}
	return nil
}

// ContainerExists checks if a container exists.
func (f *Fake) ContainerExists(ctx context.Context, containerName string) (bool, error) {
	if f.ContainerExistsErr != nil {
		if err := f.ContainerExistsErr(containerName); err != nil {
			return false, err
		}
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	_, exists := f.containers[containerName]
	return exists, nil
}

// UploadBlob uploads a blob with the given metadata and data.
func (f *Fake) UploadBlob(ctx context.Context, containerName, blobName string, md map[string]*string, data []byte) error {
	if f.UploadBlobErr != nil {
		if err := f.UploadBlobErr(containerName, blobName); err != nil {
			return err
		}
	}

	// Like the service, reject metadata it cannot store as is.
	size := 0
	for k, v := range md {
		if v == nil {
			continue
		}
		size += len(k) + len(*v)
		if !ValidMetadataValue(*v) {
			return fmt.Errorf("invalid metadata value for %q: %q", k, *v)
		}
	}
	if size > MaxMetadataSize {
		return fmt.Errorf("metadata is %d bytes, more than the %d allowed", size, MaxMetadataSize)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	container, exists := f.containers[containerName]
	if !exists {
		return &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}
	}

	// Copy data to avoid external modifications
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	container[blobName] = &blobData{
		metadata: copyMetadata(md),
		data:     dataCopy,
	}

	return nil
}

// DeleteBlob deletes the specified blob from the given container.
func (f *Fake) DeleteBlob(ctx context.Context, containerName string, blobName string) error {
	if f.DeleteBlobErr != nil {
		if err := f.DeleteBlobErr(containerName, blobName); err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	container, exists := f.containers[containerName]
	if !exists {
		return &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}
	}

	if _, exists := container[blobName]; !exists {
		return &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)}
	}

	delete(container, blobName)
	return nil
}

// GetMetadata retrieves the metadata of a blob.
func (f *Fake) GetMetadata(ctx context.Context, containerName, blobName string) (map[string]*string, error) {
	if f.GetMetadataErr != nil {
		if err := f.GetMetadataErr(containerName, blobName); err != nil {
			return nil, err
		}
	}
	// Like Real, a call whose ctx has ended fails.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	container, exists := f.containers[containerName]
	if !exists {
		return nil, &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}
	}

	blob, exists := container[blobName]
	if !exists {
		return nil, &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)}
	}

	// Return a copy to avoid external modifications
	return copyMetadata(blob.metadata), nil
}

// GetBlob downloads the blob data.
func (f *Fake) GetBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	if f.GetBlobErr != nil {
		if err := f.GetBlobErr(containerName, blobName); err != nil {
			return nil, err
		}
	}
	// Like Real, a call whose ctx has ended fails.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	container, exists := f.containers[containerName]
	if !exists {
		return nil, &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}
	}

	blob, exists := container[blobName]
	if !exists {
		return nil, &azcore.ResponseError{ErrorCode: string(bloberror.BlobNotFound)}
	}

	// Return a copy to avoid external modifications
	dataCopy := make([]byte, len(blob.data))
	copy(dataCopy, blob.data)

	return dataCopy, nil
}

// NewListBlobsFlatPager creates a pager for listing blobs in a container. The pager returns every blob matching
// options.Prefix, sorted by name, in a single page. Metadata is included when options.Include.Metadata is set.
func (f *Fake) NewListBlobsFlatPager(containerName string, options *azblob.ListBlobsFlatOptions) *runtime.Pager[azblob.ListBlobsFlatResponse] {
	return runtime.NewPager(runtime.PagingHandler[azblob.ListBlobsFlatResponse]{
		More: func(azblob.ListBlobsFlatResponse) bool { return false },
		Fetcher: func(ctx context.Context, _ *azblob.ListBlobsFlatResponse) (azblob.ListBlobsFlatResponse, error) {
			return f.listPage(containerName, options)
		},
	})
}

// NextListPage fetches the next page from pager.
func (f *Fake) NextListPage(ctx context.Context, pager *runtime.Pager[azblob.ListBlobsFlatResponse]) (azblob.ListBlobsFlatResponse, error) {
	return pager.NextPage(ctx)
}

// listPage builds the single page NewListBlobsFlatPager returns.
func (f *Fake) listPage(containerName string, options *azblob.ListBlobsFlatOptions) (azblob.ListBlobsFlatResponse, error) {
	if f.NextListPageErr != nil {
		if err := f.NextListPageErr(containerName); err != nil {
			return azblob.ListBlobsFlatResponse{}, err
		}
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	blobs, exists := f.containers[containerName]
	if !exists {
		return azblob.ListBlobsFlatResponse{}, &azcore.ResponseError{ErrorCode: string(bloberror.ContainerNotFound)}
	}

	prefix := ""
	withMD := false
	if options != nil {
		if options.Prefix != nil {
			prefix = *options.Prefix
		}
		withMD = options.Include.Metadata
	}

	names := make([]string, 0, len(blobs))
	for name := range blobs {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	items := make([]*container.BlobItem, 0, len(names))
	for _, name := range names {
		item := &container.BlobItem{Name: toPtr(name)}
		if withMD {
			item.Metadata = copyMetadata(blobs[name].metadata)
		}
		items = append(items, item)
	}

	resp := azblob.ListBlobsFlatResponse{}
	resp.Segment = &container.BlobFlatListSegment{BlobItems: items}
	return resp, nil
}

// copyMetadata returns a copy of md so callers cannot modify stored metadata.
func copyMetadata(md map[string]*string) map[string]*string {
	c := make(map[string]*string, len(md))
	for k, v := range md {
		if v != nil {
			val := *v
			c[k] = &val
		}
	}
	return c
}

// GetContainer returns the blobs in a container for test assertions.
func (f *Fake) GetContainer(containerName string) map[string]*blobData {
	f.mu.RLock()
	defer f.mu.RUnlock()

	container, exists := f.containers[containerName]
	if !exists {
		return nil
	}

	// Return a copy to avoid external modifications
	copy := make(map[string]*blobData)
	for k, v := range container {
		copy[k] = &blobData{
			metadata: v.metadata,
			data:     v.data,
		}
	}

	return copy
}

// BlobExists checks if a blob exists in a container.
func (f *Fake) BlobExists(containerName, blobName string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	container, exists := f.containers[containerName]
	if !exists {
		return false
	}

	_, exists = container[blobName]
	return exists
}
