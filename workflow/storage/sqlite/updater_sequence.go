package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"
	"zombiezen.com/go/sqlite"
)

var _ storage.SequenceUpdater = sequenceUpdater{}

// sequenceUpdater implements the storage.sequenceUpdater interface.
type sequenceUpdater struct {
	mu   *sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// UpdateSequence implements storage.SequenceUpdater.UpdateSequence().
func (s sequenceUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stmt, err := s.conn.Prepare(updateSequence)
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
