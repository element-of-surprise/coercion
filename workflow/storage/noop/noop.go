// Package noop provides a no-op implementation of the storage.Vault interface.
// This is useful when you simply some function is going to update storage for its data
// and you don't want to have to write a complete plan object to storage in order to test.
package noop

import (
	"context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/google/uuid"
)

// Vault implements the storage.Vault interface. It is a no-op implementation for
// testing purposes. It does not actually store any data, but it does implement
// the interface so that tests can be run without needing a real storage backend.
// It can only be used for writing, not for reading (which will panic).
type Vault struct {
	private.Storage
}

// Exists implements the storage.Vault interface. It will panic if called.
func (v *Vault) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	panic("reads are not allowed in the noop storage")
}

// Read implements the storage.Vault interface. It will panic if called.
func (v *Vault) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	panic("reads are not allowed in the noop storage")
}

// Search implements the storage.Vault interface. It will panic if called.
func (v *Vault) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	panic("reads are not allowed in the noop storage")
}

// List implements the storage.Vault interface. It will panic if called.
func (v *Vault) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	panic("reads are not allowed in the noop storage")
}

// Close implements the storage.Vault interface. It does nothing.
func (v *Vault) Close(ctx context.Context) error {
	return nil
}

// Create implements the storage.Vault interface. It does nothing.
func (v *Vault) Create(ctx context.Context, plan *workflow.Plan) error {
	return nil
}

// Delete implements the storage.Vault interface. It does nothing.
func (v *Vault) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

// UpdatePlan implements the storage.Vault interface. It does nothing.
func (v *Vault) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	return nil
}

// UpdateBlock implements the storage.Vault interface. It does nothing.
func (v *Vault) UpdateBlock(context.Context, *workflow.Block) error {
	return nil
}

// UpdateChecks implements the storage.Vault interface. It does nothing.
func (v *Vault) UpdateChecks(context.Context, *workflow.Checks) error {
	return nil
}

// UpdateSequence implements the storage.Vault interface. It does nothing.
func (v *Vault) UpdateSequence(context.Context, *workflow.Sequence) error {
	return nil
}

// UpdateAction implements the storage.Vault interface. It does nothing.
func (v *Vault) UpdateAction(context.Context, *workflow.Action) error {
	return nil
}
