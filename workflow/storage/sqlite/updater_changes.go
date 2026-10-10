package sqlite

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

var (
	_ storage.ChecksUpdater          = objectUpdater{}
	_ storage.BlockUpdater           = objectUpdater{}
	_ storage.SequenceUpdater        = objectUpdater{}
	_ storage.ActionUpdater          = objectUpdater{}
	_ storage.DeferredActionsUpdater = objectUpdater{}
	_ storage.DeferBatchUpdater      = objectUpdater{}
	_ storage.ChangesUpdater         = objectUpdater{}
)

// objectUpdater writes a Plan and the objects below it. Every write goes through writeItems, so objectStmt is the one place
// that maps an object type to its statement.
type objectUpdater struct {
	mu      *sync.RWMutex
	pool    *sqlitex.Pool
	capture *CaptureStmts

	private.Storage
}

// UpdateChecks implements storage.ChecksUpdater.UpdateChecks().
func (u objectUpdater) UpdateChecks(ctx context.Context, checks *workflow.Checks) error {
	return u.writeItems(ctx, []walk.Item{{Value: checks}})
}

// UpdateBlock implements storage.BlockUpdater.UpdateBlock().
func (u objectUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	return u.writeItems(ctx, []walk.Item{{Value: block}})
}

// UpdateSequence implements storage.SequenceUpdater.UpdateSequence().
func (u objectUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	return u.writeItems(ctx, []walk.Item{{Value: seq}})
}

// UpdateAction implements storage.ActionUpdater.UpdateAction().
func (u objectUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	return u.writeItems(ctx, []walk.Item{{Value: action}})
}

// UpdateDeferredActions implements storage.DeferredActionsUpdater.UpdateDeferredActions().
func (u objectUpdater) UpdateDeferredActions(ctx context.Context, da *workflow.DeferredActions) error {
	return u.writeItems(ctx, []walk.Item{{Value: da}})
}

// UpdateDeferBatch implements storage.DeferBatchUpdater.UpdateDeferBatch().
func (u objectUpdater) UpdateDeferBatch(ctx context.Context, batch *workflow.DeferBatch) error {
	return u.writeItems(ctx, []walk.Item{{Value: batch}})
}

// UpdateChanges implements storage.ChangesUpdater.UpdateChanges(). Every changed object is written in one transaction,
// so either all of them are stored or none are, and a parent can never be stored over children that were not. The
// transaction holds the Vault's lock for writing, so no read holds table locks it needs (see reader.mu).
func (u objectUpdater) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	items := changes.Since(plan, before)
	if len(items) == 0 {
		return nil
	}
	return u.writeItems(ctx, items)
}

// writeItems writes items in one transaction, so their order does not matter. A transaction that fails on a lock conflict
// is retried.
func (u objectUpdater) writeItems(ctx context.Context, items []walk.Item) error {
	return retryLocked(context.WithoutCancel(ctx), lockBackoff, func() error { return u.writeOnce(ctx, items) })
}

// writeOnce is one attempt at writeItems.
func (u objectUpdater) writeOnce(ctx context.Context, items []walk.Item) (err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	conn, err := u.pool.Take(context.WithoutCancel(ctx))
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeConn, fmt.Errorf("couldn't get a connection from the pool: %w", err))
	}
	defer u.pool.Put(conn)

	// Registered before the transaction's defer so it runs after the commit: a rolled back statement was not written.
	var written []Stmt
	defer func() {
		if err != nil {
			return
		}
		for _, stmt := range written {
			u.capture.Capture(stmt)
		}
	}()
	defer sqlitex.Transaction(conn)(&err)

	for _, item := range items {
		stmt, err := objectStmt(item)
		if err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("objectUpdater.writeItems: %w", err))
		}
		sStmt, err := stmt.Prepare(ctx, conn, errors.TypeStorageUpdate)
		if err != nil {
			return err
		}
		if _, err := sStmt.Step(); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("objectUpdater.writeItems(%v): %w", item.Value.Type(), err))
		}
		// An UPDATE of a missing row is not an error to sqlite. Without this, the rest would commit and report success.
		if n := conn.Changes(); n != 1 {
			return missingRowErr(ctx, conn, item, n)
		}
		written = append(written, stmt)
	}
	return nil
}

// planIDer is an object below a Plan that knows its Plan's ID.
type planIDer interface {
	GetPlanID() uuid.UUID
}

// missingRowErr returns the error for an update of item that changed n rows instead of 1. No Plan row means the Plan
// is gone, whether item is the Plan or an object below it, so that is an errors.ErrNotFound. A missing row below a
// stored Plan means its storage is damaged, so that is a TypeStorageInconsistent.
func missingRowErr(ctx context.Context, conn *sqlite.Conn, item walk.Item, n int) error {
	err := fmt.Errorf("objectUpdater.writeItems(%v %s): updated %d rows, want 1", item.Value.Type(), item.Value.(ider).GetID(), n)

	planID := item.Value.(ider).GetID()
	if item.Value.Type() != workflow.OTPlan {
		p, ok := item.Value.(planIDer)
		if !ok || p.GetPlanID() == uuid.Nil {
			// Without its Plan's ID the Plan can't be looked up, so this can't be told apart from damage.
			return errors.ErrStorageInconsistent(ctx, err)
		}
		planID = p.GetPlanID()
	}
	stored, lookupErr := planStored(ctx, conn, planID)
	switch {
	case lookupErr != nil:
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("%w: and couldn't check for its plan: %w", err, lookupErr))
	case !stored:
		return errors.ErrNotFound(ctx, err)
	}
	return errors.ErrStorageInconsistent(ctx, err)
}

// objectStmt returns the statement that updates one object below a Plan. It returns an error for an Item with no
// object, nil or not, rather than panicking under the write lock.
func objectStmt(item walk.Item) (Stmt, error) {
	if item.Value == nil {
		return Stmt{}, fmt.Errorf("cannot write a zero walk.Item")
	}
	switch item.Value.Type() {
	case workflow.OTPlan:
		if plan := item.Plan(); plan != nil {
			return planStmt(plan), nil
		}
		return Stmt{}, fmt.Errorf("cannot write a nil %T", item.Value)
	case workflow.OTCheck:
		return objectStateStmt(updateChecks, item.Checks())
	case workflow.OTBlock:
		return objectStateStmt(updateBlock, item.Block())
	case workflow.OTSequence:
		return objectStateStmt(updateSequence, item.Sequence())
	case workflow.OTAction:
		if action := item.Action(); action != nil {
			return actionStmt(action)
		}
		return Stmt{}, fmt.Errorf("cannot write a nil %T", item.Value)
	case workflow.OTDeferredActions:
		return objectStateStmt(updateDeferredActions, item.DeferredActions())
	case workflow.OTBatch:
		return objectStateStmt(updateDeferBatch, item.DeferBatch())
	}
	return Stmt{}, fmt.Errorf("cannot write object of type %v", item.Value.Type())
}

// objectStateStmt returns the statement query that updates obj's state, or an error if obj is nil.
func objectStateStmt[T interface {
	*E
	GetID() uuid.UUID
	GetState() workflow.State
}, E any](query string, obj T) (Stmt, error) {
	if obj == nil {
		return Stmt{}, fmt.Errorf("cannot write a nil %T", obj)
	}
	return stateStmt(query, obj.GetID(), obj.GetState()), nil
}

// stateStmt returns the statement query that updates the state of the object with id.
func stateStmt(query string, id uuid.UUID, state workflow.State) Stmt {
	stmt := Stmt{}
	stmt.Query(query)
	stmt.SetText("$id", id.String())
	stmt.SetInt64("$state_status", int64(state.Status))
	stmt.SetInt64("$state_start", state.Start.UnixNano())
	stmt.SetInt64("$state_end", state.End.UnixNano())
	return stmt
}

// actionStmt returns the statement that updates an Action's state and attempts.
func actionStmt(action *workflow.Action) (Stmt, error) {
	stmt := stateStmt(updateAction, action.ID, action.State.Get())
	b, err := encodeAttempts(action.Attempts.Get())
	if err != nil {
		return Stmt{}, err
	}
	stmt.SetBytes("$attempts", b)
	return stmt, nil
}
