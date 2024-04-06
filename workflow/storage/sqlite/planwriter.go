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

// planWriter implements the storage.PlanWriter interface.
type planWriter struct {
	id uuid.UUID
	// mu is a mutex to protect the underlying storage.
	// It ensures that only one write operation is happening at a time.
	mu   sync.Mutex
	conn *sqlite.Conn

	private.Storage
}

// Write writes Plan data to storage, and all underlying data.
func (p *planWriter) Write(ctx context.Context, plan *workflow.Plan) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	panic("not implemented") // TODO: Implement
}

// Checks returns a PlanChecksWriter.
func (p *planWriter) Checks() storage.ChecksWriter {

	panic("not implemented") // TODO: Implement
}

// Block returns a BlockWriter for the given Block ID. If the Block ID does not exist,
// this will panic.
func (p *planWriter) Block(id uuid.UUID) storage.BlockWriter {

	panic("not implemented") // TODO: Implement
}

func (p *planWriter) private() {
	panic("not implemented") // TODO: Implement
}
