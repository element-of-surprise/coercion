package cosmosdb

import (
	"context"

	"github.com/element-of-surprise/coercion/internal/private"
)

// This is required by the storage interface.
type closer struct {
	Client

	private.Storage
}

func (c *closer) Close(ctx context.Context) error {
	// nothing to close
	return nil
}
