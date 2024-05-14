package sqlite

import (
	"context"

	"github.com/element-of-surprise/coercion/internal/private"
	"zombiezen.com/go/sqlite/sqlitex"
)

type closer struct {
	pool *sqlitex.Pool

	private.Storage
}

func (c *closer) Close(ctx context.Context) error {
	return c.pool.Close()
}
