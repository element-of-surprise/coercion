/*
Package sqlite provides a sqlite-based storage implementation for workflow.Plan data. This is used
to implement the storage.ReadWriter interface.

This package is for use only by the workstream package and any use outside of workstream is not
supported.
*/
package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow/storage"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	_ "embed"
)

// This validates that the ReadWriter type implements the storage.ReadWriter interface.
var _ storage.Vault = &Vault{}

// Vault implements the storage.Vault interface.
type Vault struct {
	// root is the root path for the storage.
	root      string
	pool      *sqlitex.Pool
	openFlags []sqlite.OpenFlags

	reader
	creator
	updater
	closer

	private.Storage
}

// Option is an option for configuring a ReadWriter.
type Option func(*Vault) error

// WithInMemory creates an in-memory storage.
func WithInMemory() Option {
	return func(r *Vault) error {
		r.openFlags = append(r.openFlags, sqlite.OpenMemory)
		return nil
	}
}

// New is the constructor for *ReadWriter. root is the root path for the storage.
// If the root path does not exist, it will be created.
func New(ctx context.Context, root string, reg *registry.Register, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	r := &Vault{
		root:      root,
		openFlags: []sqlite.OpenFlags{sqlite.OpenReadWrite, sqlite.OpenCreate, sqlite.OpenWAL},
	}
	for _, o := range options {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	inMem := false
	for _, flag := range r.openFlags {
		if flag == sqlite.OpenMemory {
			inMem = true
			break
		}
	}
	if !inMem {
		_, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.MkdirAll(root, 0700); err != nil {
					return nil, fmt.Errorf("storage path(%s) did not exist and could not be created: %w", root, err)
				}
			} else {
				return nil, fmt.Errorf("storage path(%s) could not be accessed: %w", root, err)
			}
		}
	}

	path := filepath.Join(root, "workstream.db")
	var flags sqlite.OpenFlags
	for _, flag := range r.openFlags {
		flags |= flag
	}

	// NOTE: Pool is set to 1. I'm having a problem with multiple conns seeing the commits of each other.
	// Such as even Pool creation. Not sure what is wrong. PoolSize 1 is a workaround for the moment.
	pool, err := sqlitex.NewPool(path, sqlitex.PoolOptions{Flags: flags, PoolSize: 1})
	if err != nil {
		return nil, err
	}

	conn, err := pool.Take(ctx)
	if err != nil {
		return nil, err
	}
	defer pool.Put(conn)

	if err = createTables(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}

	mu := &sync.Mutex{}

	r.pool = pool
	r.reader = reader{pool: pool, reg: reg}
	r.creator = creator{mu: mu, pool: pool, reader: r.reader}
	r.updater = newUpdater(mu, pool)
	r.closer = closer{pool: pool}
	return r, nil
}

func createTables(ctx context.Context, conn *sqlite.Conn) error {
	for _, table := range tables {
		if err := sqlitex.ExecuteTransient(
			conn,
			table,
			&sqlitex.ExecOptions{},
		); err != nil {
			return fmt.Errorf("couldn't create table: %w", err)
		}
	}
	return nil
}
