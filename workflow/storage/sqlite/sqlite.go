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

	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"

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
	conn      *sqlite.Conn
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
func New(ctx context.Context, root string, options ...Option) (*Vault, error) {
	ctx = context.WithoutCancel(ctx)

	r := &Vault{
		root:      root,
		openFlags: []sqlite.OpenFlags{sqlite.OpenReadWrite, sqlite.OpenCreate},
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

	conn, err := sqlite.OpenConn(path, r.openFlags...)
	if err != nil {
		return nil, err
	}

	if err = createTables(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}

	mu := &sync.Mutex{}

	r.conn = conn
	r.reader = reader{conn: conn}
	r.creator = creator{mu: mu, conn: conn}
	r.updater = newUpdater(mu, conn)
	r.closer = closer{conn: conn}
	return r, nil
}

// createTables creates the tables for the storage using the schema stored in the embedded file.
func (r *Vault) createTables(ctx context.Context) error {
	return createTables(ctx, r.conn)
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
