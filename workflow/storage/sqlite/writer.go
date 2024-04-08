package sqlite

import (
	"context"
	"sync"

	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"
	"github.com/element-of-surprise/workstream/workflow/storage/internal/private"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

// PlanWriter implements the storage.PlanWriter interface.
type PlanWriter struct {
	id uuid.UUID
	// mu is a mutex to protect the underlying storage.
	// It ensures that only one write operation is happening at a time.
	mu   sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// Write writes Plan data to storage, and all underlying data.
func (p *PlanWriter) Write(ctx context.Context, plan *workflow.Plan) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return commitPlan(ctx, p.conn, plan)
}

// Checks returns a PlanChecksWriter.
func (p *PlanWriter) Checks() storage.ChecksWriter {
	return ChecksWriter{mu: &p.mu, conn: p.conn}
}

// Block returns a BlockWriter.
func (p *PlanWriter) Block() storage.BlockWriter {
	return BlockWriter{mu: &p.mu, conn: p.conn}
}

// Sequence returns a SequenceWriter.
func (p *PlanWriter) Sequence() storage.SequenceWriter {
	return SequenceWriter{mu: &p.mu, conn: p.conn}
}

// Action returns an ActionWriter.
func (p *PlanWriter) Action() storage.ActionWriter {
	return ActionWriter{mu: &p.mu, conn: p.conn}
}
