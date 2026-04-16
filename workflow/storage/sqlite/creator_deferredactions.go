package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

const insertDeferredActions = `
	INSERT INTO deferredactions (
		id,
		plan_id,
		batches,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $batches, $state_status, $state_start, $state_end)`

func commitDeferredActions(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, da *workflow.DeferredActions, capture *CaptureStmts) error {
	if da == nil {
		return nil
	}

	stmt := Stmt{}
	stmt.Query(insertDeferredActions)

	var batches []byte
	var err error
	if len(da.DeferredBatches) > 0 {
		batches, err = idsToJSON(da.DeferredBatches)
		if err != nil {
			return fmt.Errorf("commitDeferredActions(idsToJSON(batches)): %w", err)
		}
	}

	stmt.SetText("$id", da.ID.String())
	stmt.SetText("$plan_id", planID.String())
	if batches != nil {
		stmt.SetBytes("$batches", batches)
	} else {
		stmt.SetNull("$batches")
	}
	stmt.SetInt64("$state_status", int64(da.State.Get().Status))
	stmt.SetInt64("$state_start", da.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", da.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitDeferredActions: %w", err)
	}
	if _, err := sStmt.Step(); err != nil {
		return fmt.Errorf("commitDeferredActions: %w", err)
	}
	capture.Capture(stmt)

	for i, b := range da.DeferredBatches {
		if err := commitDeferBatch(ctx, conn, planID, da.ID, i, b, capture); err != nil {
			return fmt.Errorf("commitDeferredActions(batch %d): %w", i, err)
		}
	}
	return nil
}

const insertDeferBatch = `
	INSERT INTO deferbatches (
		id,
		plan_id,
		deferredactions_id,
		pos,
		when_run,
		fail_element,
		name,
		descr,
		actions,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $deferredactions_id, $pos, $when_run, $fail_element, $name, $descr,
	$actions, $state_status, $state_start, $state_end)`

func commitDeferBatch(ctx context.Context, conn *sqlite.Conn, planID, daID uuid.UUID, pos int, batch *workflow.DeferBatch, capture *CaptureStmts) error {
	stmt := Stmt{}
	stmt.Query(insertDeferBatch)

	var actions []byte
	var err error
	if len(batch.Actions) > 0 {
		actions, err = idsToJSON(batch.Actions)
		if err != nil {
			return fmt.Errorf("commitDeferBatch(idsToJSON(actions)): %w", err)
		}
	}

	stmt.SetText("$id", batch.ID.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$deferredactions_id", daID.String())
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetInt64("$when_run", int64(batch.When))
	stmt.SetBool("$fail_element", batch.FailElement)
	stmt.SetText("$name", batch.Name)
	stmt.SetText("$descr", batch.Descr)
	if actions != nil {
		stmt.SetBytes("$actions", actions)
	} else {
		stmt.SetNull("$actions")
	}
	stmt.SetInt64("$state_status", int64(batch.State.Get().Status))
	stmt.SetInt64("$state_start", batch.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", batch.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitDeferBatch: %w", err)
	}
	if _, err := sStmt.Step(); err != nil {
		return fmt.Errorf("commitDeferBatch: %w", err)
	}
	capture.Capture(stmt)

	for i, a := range batch.Actions {
		if err := commitAction(ctx, conn, planID, i, a, capture); err != nil {
			return fmt.Errorf("commitDeferBatch: %w", err)
		}
	}
	return nil
}
