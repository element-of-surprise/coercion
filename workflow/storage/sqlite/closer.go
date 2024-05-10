package sqlite

import (
	"context"

	"github.com/element-of-surprise/coercion/internal/private"
	"zombiezen.com/go/sqlite"
)

type closer struct {
	conn *sqlite.Conn

	private.Storage
}

func (c *closer) Close(ctx context.Context) error {
	return c.conn.Close()
}
