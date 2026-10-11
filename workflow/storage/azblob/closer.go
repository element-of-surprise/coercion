package azblob

import (
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

var _ storage.Closer = closer{}

// closer implements the storage.Closer interface.
type closer struct {
	mu *planlocks.Group

	private.Storage
}

// Close implements storage.Closer.Close(). It stops the removal of unused plan locks. The Azure Blob Storage client
// does not require explicit cleanup, and reads and writes keep working after Close.
func (c closer) Close(ctx context.Context) error {
	c.mu.Close()
	return nil
}
