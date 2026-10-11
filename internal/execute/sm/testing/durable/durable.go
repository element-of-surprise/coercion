// Package durable provides a fake storage.Vault for recovery tests that keeps its own copy of a Plan, the way real
// storage does, so a test can check what is stored after a write fails part way through and a retry runs.
package durable

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

var cloneOpts = []clone.Option{clone.WithKeepSecrets(), clone.WithKeepState()}

// ErrWrite is returned by the write that Store is set to fail.
var ErrWrite = errors.New("durable: injected write failure")

type object interface {
	GetID() uuid.UUID
	GetState() workflow.State
	SetState(workflow.State)
}

// Store is a storage.Vault that holds its own copy of one Plan and applies each Plan or object write to that copy by
// ID. Only the methods recovery uses are implemented. The first write of an object of the fail type returns ErrWrite
// and changes nothing.
type Store struct {
	storage.Vault

	stored   *workflow.Plan
	index    map[uuid.UUID]object
	failType workflow.ObjectType
	failed   bool
}

// New returns a Store holding a copy of plan. The first write of an object of type failType fails; pass
// workflow.OTUnknown for a Store that never fails.
func New(ctx context.Context, plan *workflow.Plan, failType workflow.ObjectType) *Store {
	s := &Store{stored: clone.Plan(ctx, plan, cloneOpts...), index: map[uuid.UUID]object{}, failType: failType}
	for item := range walk.Plan(s.stored) {
		obj := item.Value.(object)
		s.index[obj.GetID()] = obj
	}
	return s
}

// Read returns a copy of the stored Plan, as a real store decodes a new Plan on every read.
func (s *Store) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	return clone.Plan(ctx, s.stored, cloneOpts...), nil
}

// Statuses returns the stored status of every object in the Plan, keyed by walk position and type, for comparing
// what two Stores hold.
func (s *Store) Statuses() map[string]string {
	out := map[string]string{}
	i := 0
	for item := range walk.Plan(s.stored) {
		out[fmt.Sprintf("%02d %v", i, item.Value.Type())] = item.Value.(object).GetState().Status.String()
		i++
	}
	return out
}

func (s *Store) write(obj object, t workflow.ObjectType) error {
	if t == s.failType && !s.failed {
		s.failed = true
		return ErrWrite
	}
	target, ok := s.index[obj.GetID()]
	if !ok {
		return fmt.Errorf("durable: no stored object %s", obj.GetID())
	}
	target.SetState(obj.GetState())
	if a, ok := obj.(*workflow.Action); ok {
		stored := target.(*workflow.Action)
		if !a.Attempts.IsSet() {
			stored.Attempts.Clear()
			return nil
		}
		stored.Attempts.Set(a.Attempts.Get())
	}
	return nil
}

func (s *Store) UpdatePlan(ctx context.Context, p *workflow.Plan) error {
	if err := s.write(p, workflow.OTPlan); err != nil {
		return err
	}
	s.stored.Reason = p.Reason
	return nil
}

func (s *Store) UpdateChecks(ctx context.Context, c *workflow.Checks) error {
	return s.write(c, workflow.OTCheck)
}

func (s *Store) UpdateAction(ctx context.Context, a *workflow.Action) error {
	return s.write(a, workflow.OTAction)
}

func (s *Store) UpdateBlock(ctx context.Context, b *workflow.Block) error {
	return s.write(b, workflow.OTBlock)
}

func (s *Store) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	return s.write(seq, workflow.OTSequence)
}

func (s *Store) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	return s.write(da, workflow.OTDeferredActions)
}

func (s *Store) UpdateDeferBatch(ctx context.Context, b *workflow.DeferBatch) error {
	return s.write(b, workflow.OTBatch)
}

// UpdateChanges writes the changed objects one at a time, each after its descendants, the way a Vault that cannot
// group writes does, so a failure part way leaves what such a Vault would.
func (s *Store) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	return storage.WriteChanges(ctx, s, plan, before)
}
