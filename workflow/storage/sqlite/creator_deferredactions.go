package sqlite

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/workflow"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
)

const (
	listKindOnFailure = "onfailure"
	listKindOnSuccess = "onsuccess"
)

const insertDeferredActions = `
	INSERT INTO deferredactions (
		id,
		plan_id,
		onfailure,
		onsuccess,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $onfailure, $onsuccess, $state_status, $state_start, $state_end)`

func commitDeferredActions(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, da *workflow.DeferredActions, capture *CaptureStmts) error {
	if da == nil {
		return nil
	}

	stmt := Stmt{}
	stmt.Query(insertDeferredActions)

	var onFailure, onSuccess []byte
	var err error
	if len(da.OnFailure) > 0 {
		onFailure, err = idsToJSON(da.OnFailure)
		if err != nil {
			return fmt.Errorf("commitDeferredActions(idsToJSON(onfailure)): %w", err)
		}
	}
	if len(da.OnSuccess) > 0 {
		onSuccess, err = idsToJSON(da.OnSuccess)
		if err != nil {
			return fmt.Errorf("commitDeferredActions(idsToJSON(onsuccess)): %w", err)
		}
	}

	stmt.SetText("$id", da.ID.String())
	stmt.SetText("$plan_id", planID.String())
	if onFailure != nil {
		stmt.SetBytes("$onfailure", onFailure)
	} else {
		stmt.SetNull("$onfailure")
	}
	if onSuccess != nil {
		stmt.SetBytes("$onsuccess", onSuccess)
	} else {
		stmt.SetNull("$onsuccess")
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

	for i, b := range da.OnFailure {
		if err := commitDeferBatch(ctx, conn, planID, da.ID, listKindOnFailure, i, b, capture); err != nil {
			return fmt.Errorf("commitDeferredActions(onfailure): %w", err)
		}
	}
	for i, b := range da.OnSuccess {
		if err := commitDeferBatch(ctx, conn, planID, da.ID, listKindOnSuccess, i, b, capture); err != nil {
			return fmt.Errorf("commitDeferredActions(onsuccess): %w", err)
		}
	}
	return nil
}

const insertDeferBatch = `
	INSERT INTO deferbatches (
		id,
		plan_id,
		deferredactions_id,
		list_kind,
		pos,
		fail_element,
		name,
		descr,
		actions,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $deferredactions_id, $list_kind, $pos, $fail_element, $name, $descr,
	$actions, $state_status, $state_start, $state_end)`

func commitDeferBatch(ctx context.Context, conn *sqlite.Conn, planID, daID uuid.UUID, listKind string, pos int, batch *workflow.DeferBatch, capture *CaptureStmts) error {
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
	stmt.SetText("$list_kind", listKind)
	stmt.SetInt64("$pos", int64(pos))
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
