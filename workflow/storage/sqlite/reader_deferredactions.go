package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fieldToDeferredActions reads the "deferredactions" field from a plan row and
// fetches the associated DeferredActions hierarchy. Returns nil if the field is empty.
func (r reader) fieldToDeferredActions(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.DeferredActions, error) {
	strID := stmt.GetText("deferredactions")
	if strID == "" {
		return nil, nil
	}
	id, err := uuid.Parse(strID)
	if err != nil {
		return nil, fmt.Errorf("fieldToDeferredActions: couldn't parse id: %w", err)
	}
	return r.fetchDeferredActionsByID(ctx, conn, id)
}

func (r reader) fetchDeferredActionsByID(ctx context.Context, conn *sqlite.Conn, id uuid.UUID) (*workflow.DeferredActions, error) {
	var da *workflow.DeferredActions
	err := sqlitex.Execute(
		conn,
		fetchDeferredActionsByID,
		&sqlitex.ExecOptions{
			Named: map[string]any{
				"$id": id.String(),
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				d, err := r.deferredActionsRowToDeferredActions(ctx, conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't convert row to DeferredActions: %w", err)
				}
				da = d
				return nil
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch DeferredActions by id: %w", err)
	}
	if da == nil {
		return nil, fmt.Errorf("couldn't find DeferredActions by id(%s)", id)
	}
	return da, nil
}

func (r reader) deferredActionsRowToDeferredActions(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.DeferredActions, error) {
	da := &workflow.DeferredActions{}

	var err error
	da.ID, err = uuid.Parse(stmt.GetText("id"))
	if err != nil {
		return nil, fmt.Errorf("deferredActionsRowToDeferredActions: couldn't parse id: %w", err)
	}

	planID, err := uuid.Parse(stmt.GetText("plan_id"))
	if err != nil {
		return nil, fmt.Errorf("deferredActionsRowToDeferredActions: couldn't parse plan_id: %w", err)
	}
	da.SetPlanID(planID)

	state, err := fieldToState(stmt)
	if err != nil {
		return nil, fmt.Errorf("deferredActionsRowToDeferredActions: %w", err)
	}
	da.State.Set(*state)

	if b := fieldToBytes("onfailure", stmt); b != nil {
		ids, err := fieldToIDs("onfailure", stmt)
		if err != nil {
			return nil, fmt.Errorf("deferredActionsRowToDeferredActions(onfailure): %w", err)
		}
		batches, err := r.fetchDeferBatchesByIDs(ctx, conn, ids)
		if err != nil {
			return nil, fmt.Errorf("deferredActionsRowToDeferredActions(onfailure fetch): %w", err)
		}
		da.OnFailure = batches
	}
	if b := fieldToBytes("onsuccess", stmt); b != nil {
		ids, err := fieldToIDs("onsuccess", stmt)
		if err != nil {
			return nil, fmt.Errorf("deferredActionsRowToDeferredActions(onsuccess): %w", err)
		}
		batches, err := r.fetchDeferBatchesByIDs(ctx, conn, ids)
		if err != nil {
			return nil, fmt.Errorf("deferredActionsRowToDeferredActions(onsuccess fetch): %w", err)
		}
		da.OnSuccess = batches
	}
	return da, nil
}

func (r reader) fetchDeferBatchesByIDs(ctx context.Context, conn *sqlite.Conn, ids []uuid.UUID) ([]*workflow.DeferBatch, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	query, args := replaceWithIDs(fetchDeferBatchesByID, "$ids", ids)
	// pos-based ordering inside fetchDeferBatchesByID is per-list; since we pass only
	// one list's worth of ids at a time, the ORDER BY pos ASC yields that list in order.
	byID := make(map[uuid.UUID]*workflow.DeferBatch, len(ids))
	err := sqlitex.Execute(
		conn,
		query,
		&sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *sqlite.Stmt) error {
				b, err := r.deferBatchRowToDeferBatch(ctx, conn, stmt)
				if err != nil {
					return fmt.Errorf("couldn't convert row to DeferBatch: %w", err)
				}
				byID[b.ID] = b
				return nil
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch DeferBatches by ids: %w", err)
	}

	// Preserve the caller-provided order.
	out := make([]*workflow.DeferBatch, 0, len(ids))
	for _, id := range ids {
		b, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("couldn't find DeferBatch by id(%s)", id)
		}
		out = append(out, b)
	}
	return out, nil
}

func (r reader) deferBatchRowToDeferBatch(ctx context.Context, conn *sqlite.Conn, stmt *sqlite.Stmt) (*workflow.DeferBatch, error) {
	b := &workflow.DeferBatch{}

	var err error
	b.ID, err = uuid.Parse(stmt.GetText("id"))
	if err != nil {
		return nil, fmt.Errorf("deferBatchRowToDeferBatch: couldn't parse id: %w", err)
	}

	planID, err := uuid.Parse(stmt.GetText("plan_id"))
	if err != nil {
		return nil, fmt.Errorf("deferBatchRowToDeferBatch: couldn't parse plan_id: %w", err)
	}
	b.SetPlanID(planID)

	b.FailElement = stmt.GetInt64("fail_element") != 0
	b.Name = stmt.GetText("name")
	b.Descr = stmt.GetText("descr")

	state, err := fieldToState(stmt)
	if err != nil {
		return nil, fmt.Errorf("deferBatchRowToDeferBatch: %w", err)
	}
	b.State.Set(*state)

	b.Actions, err = r.fieldToActions(ctx, conn, stmt)
	if err != nil {
		return nil, fmt.Errorf("deferBatchRowToDeferBatch(actions): %w", err)
	}
	return b, nil
}
