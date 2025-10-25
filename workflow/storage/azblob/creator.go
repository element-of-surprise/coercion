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
	uploader *uploader

	private.Storage
}

// Create implements storage.Creator.Create(). It creates a new Plan in storage.
func (c creator) Create(ctx context.Context, plan *workflow.Plan) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.uploader.uploadPlan(ctx, plan, true)
}
