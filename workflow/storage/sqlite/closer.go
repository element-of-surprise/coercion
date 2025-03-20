package sqlite

import (
	"context"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"zombiezen.com/go/sqlite/sqlitex"
)

type closer struct {
	pool *sqlitex.Pool
	mu   *sync.RWMutex

	private.Storage
}

func (c *closer) Close(ctx context.Context) error {
	return c.pool.Close()
}
