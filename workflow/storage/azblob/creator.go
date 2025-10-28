package azblob

import (
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
)

var _ storage.Creator = creator{}

// creator implements the storage.Creator interface.
type creator struct {
	mu       *planlocks.Group
	prefix   string
	endpoint string
	reader   reader
	uploader *uploader

	private.Storage
}

// Create implements storage.Creator.Create(). It creates a new Plan in storage.
func (c creator) Create(ctx context.Context, plan *workflow.Plan) error {
	c.mu.Lock(plan.ID)
	defer c.mu.Unlock(plan.ID)

	return c.uploader.uploadPlan(ctx, plan, uptCreate)
}
