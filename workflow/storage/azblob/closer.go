package azblob

import (
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.Closer = closer{}

// closer implements the storage.Closer interface.
type closer struct {
	private.Storage
}

// Close implements storage.Closer.Close(). Azure Blob Storage client does not require
// explicit cleanup, so this is a no-op.
func (c closer) Close(ctx context.Context) error {
	return nil
}
