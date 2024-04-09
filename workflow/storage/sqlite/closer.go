package sqlite

import (
	"context"

	"zombiezen.com/go/sqlite"
)

type closer struct {
	conn *sqlite.Conn
}

func (c *closer) Close(ctx context.Context) error {
	return c.conn.Close()
}
