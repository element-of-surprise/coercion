package sqlite

import (
	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/errors"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"zombiezen.com/go/sqlite/sqlitex"
)

type closer struct {
	pool *sqlitex.Pool
	mu   *sync.RWMutex

	private.Storage
}

func (c *closer) Close(ctx context.Context) error {
	err := c.pool.Close()
	if err == nil {
		return err
	}
	return errors.E(ctx, errors.CatInternal, errors.TypeStorageClose, err)
}
