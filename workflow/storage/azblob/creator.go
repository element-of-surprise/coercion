package azblob

import (
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.Creator = creator{}

// creator implements the storage.Creator interface.
type creator struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string
	reader   reader

	private.Storage
}

// Create implements storage.Creator.Create(). It creates a new Plan in storage.
// This method:
// 1. Creates the daily container if it doesn't exist
// 2. Writes the full Plan hierarchy to the plan blob (for recovery)
// 3. Writes individual sub-object blobs (blocks, sequences, checks, actions)
// 4. Sets blob index tags on the plan blob (state, groupID)
//
// This operation is atomic in the sense that if it fails, the plan won't be readable.
func (c creator) Create(ctx context.Context, plan *workflow.Plan) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.commitPlan(ctx, plan)
}
