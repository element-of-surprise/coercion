package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite/sqlitex"
)

var _ storage.SequenceUpdater = sequenceUpdater{}

// sequenceUpdater implements the storage.sequenceUpdater interface.
type sequenceUpdater struct {
	mu   *sync.Mutex
	pool *sqlitex.Pool

	private.Storage
}

// UpdateSequence implements storage.SequenceUpdater.UpdateSequence().
func (s sequenceUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, err := s.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("couldn't get a connection from the pool: %w", err)
	}
	defer s.pool.Put(conn)

	stmt, err := conn.Prepare(updateSequence)
	if err != nil {
		return fmt.Errorf("SequenceWriter.Write(updateAction): %w", err)
	}

	stmt.SetText("$id", seq.ID.String())
	stmt.SetInt64("$state_status", int64(seq.State.Status))
	stmt.SetInt64("$state_start", seq.State.Start.UnixNano())
	stmt.SetInt64("$state_end", seq.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("SequenceWriter.Write: %w", err)
	}

	return nil
}
