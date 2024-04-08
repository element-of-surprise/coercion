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

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"
	"github.com/google/uuid"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	_ "embed"
)

// This validates that the ReadWriter type implements the storage.ReadWriter interface.
var _ storage.ReadWriter = &ReadWriter{}

// ReadWriter implements the storage.ReadWriter interface.
type ReadWriter struct {
	// root is the root path for the storage.
	root string
	conn *sqlite.Conn

	*planReader

	private.Storage
}

// Option is an option for configuring a ReadWriter.
type Option func(*ReadWriter) error

// New is the constructor for *ReadWriter. root is the root path for the storage.
// If the root path does not exist, it will be created.
func New(ctx context.Context, root string, options ...Option) (*ReadWriter, error) {
	ctx = context.WithoutCancel(ctx)

	r := &ReadWriter{root: root}
	for _, o := range options {
		if err := o(r); err != nil {
			return nil, err
		}
	}

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

	path := filepath.Join(root, "workstream.db")

	conn, err := sqlite.OpenConn(fmt.Sprintf("%s;Version=3;", path), sqlite.OpenReadWrite, sqlite.OpenCreate)
	if err != nil {
		return nil, err
	}

	r.conn = conn
	r.planReader = &planReader{conn: conn}
	return r, nil
}

// createTables creates the tables for the storage using the schema stored in the embedded file.
func (r *ReadWriter) createTables(ctx context.Context) error {
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

// Create writes the plan to storage. If the plan already exists, this will return an error.
func (r *ReadWriter) Create(ctx context.Context, plan *workflow.Plan) error {
	if plan.ID == uuid.Nil {
		return fmt.Errorf("plan ID cannot be nil")
	}

	exist, err := r.Exists(ctx, plan.ID)
	if err != nil {
		return err
	}

	if exist {
		return fmt.Errorf("plan with ID(%s) already exists", plan.ID)
	}

	return commitPlan(ctx, r.conn, plan)
}

func (r *ReadWriter) Writer(ctx context.Context, id uuid.UUID) (storage.PlanWriter, error) {
	exist, err := r.Exists(ctx, id)
	if err != nil {
		return nil, err
	}
	if !exist {
		return nil, fmt.Errorf("plan with ID(%s) does not exist", id)
	}

	return &planWriter{conn: r.conn, id: id}, nil
}

// Close closes the ReadWriter and releases all resources.
func (r *ReadWriter) Close(ctx context.Context) error {
	return r.conn.Close()
}

// blockReader implements the storage.BlockReader interface.
type blockWriter struct {
	conn *sqlite.Conn
	mu   sync.Mutex
}

// Write writes Block data to storage, but not underlying data.
func (b *blockWriter) Write(ctx context.Context, block *workflow.Block) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

// Checks returns a ChecksWriter for Checks in this Block.
func (b *blockWriter) Checks() storage.ChecksWriter {

	panic("not implemented") // TODO: Implement
}

// Sequence returns a SequenceWriter for Sequence in this Block.
func (b *blockWriter) Sequence(id uuid.UUID) storage.SequenceWriter {

	panic("not implemented") // TODO: Implement
}

func (b *blockWriter) private() {
	panic("not implemented") // TODO: Implement
}

type checksWriter struct {
	mu   sync.Mutex
	conn *sqlite.Conn
}

func (c *checksWriter) PreChecks(ctx context.Context, checks *workflow.Checks) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

func (c *checksWriter) ContChecks(ctx context.Context, checks *workflow.Checks) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

func (c *checksWriter) PostChecks(cts context.Context, checks *workflow.Checks) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

// Action returns an ActionWriter for Actions in either PreChecks, ContChecks or PostChecks.
// The type of the Action is determined by the workflow.ObjectType. This must be:
// workflow.OTPreCheck, workflow.OTContCheck or workflow.OTPostCheck or this will panic.
func (c *checksWriter) Action(t workflow.ObjectType) actionWriter {
	panic("not implemented") // TODO: Implement
}

type sequenceWriter struct {
	mu  sync.Mutex
	con *sqlite.Conn
}

// Write writes Sequence data to storage for Sequences in the specific Block, but not underlying data.
func (s *sequenceWriter) Write(ctx context.Context, seq *workflow.Sequence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

// Action returns an ActionWriter for Actions in this Sequence.
func (s *sequenceWriter) Action() actionWriter {
	panic("not implemented") // TODO: Implement
}

func (s *sequenceWriter) private() {
	panic("not implemented") // TODO: Implement
}

type actionWriter struct {
	mu   sync.Mutex
	conn *sqlite.Conn
}

func (a *actionWriter) Write(ctx context.Context, action *workflow.Action) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

func (a *actionWriter) private() {
	panic("not implemented") // TODO: Implement
}
