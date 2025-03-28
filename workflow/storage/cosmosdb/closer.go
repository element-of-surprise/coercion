package cosmosdb

import (
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
)

// This is required to implement the storage.Vault interface.
type closer struct {
	private.Storage
}

// Close is currently a no-op for the CosmosDB storage.
func (c *closer) Close(ctx context.Context) error {
	// nothing to close
	return nil
}
