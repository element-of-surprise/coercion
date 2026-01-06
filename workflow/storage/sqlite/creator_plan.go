package sqlite

import (
	"fmt"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const insertPlan = `
	INSERT INTO plans (
		id,
		group_id,
		name,
		descr,
		meta,
		bypasschecks,
		prechecks,
		postchecks,
		contchecks,
		deferredchecks,
		blocks,
		state_status,
		state_start,
		state_end,
		submit_time,
		reason
	) VALUES ($id, $group_id, $name, $descr, $meta, $bypasschecks, $prechecks, $postchecks, $contchecks, $deferredchecks,
	$blocks, $state_status, $state_start, $state_end, $submit_time, $reason)`

var zeroTime = time.Unix(0, 0)

// commitPlan commits a plan to the database. This commits the entire plan and all sub-objects.
func commitPlan(ctx context.Context, conn *sqlite.Conn, p *workflow.Plan, capture *CaptureStmts) (err error) {
	if p == nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("planToSQL: plan cannot be nil"))
	}

	defer sqlitex.Transaction(conn)(&err)

	stmt := Stmt{}
	stmt.Query(insertPlan)
	stmt.SetText("$id", p.ID.String())
	stmt.SetText("$group_id", p.GroupID.String())
	stmt.SetText("$name", p.Name)
	stmt.SetText("$descr", p.Descr)
	stmt.SetBytes("$meta", p.Meta)
	if p.BypassChecks != nil {
		stmt.SetText("$bypasschecks", p.BypassChecks.ID.String())
	}
	if p.PreChecks != nil {
		stmt.SetText("$prechecks", p.PreChecks.ID.String())
	}
	if p.PostChecks != nil {
		stmt.SetText("$postchecks", p.PostChecks.ID.String())
	}
	if p.ContChecks != nil {
		stmt.SetText("$contchecks", p.ContChecks.ID.String())
	}
	if p.DeferredChecks != nil {
		stmt.SetText("$deferredchecks", p.DeferredChecks.ID.String())
	}
	blocks, err := idsToJSON(p.Blocks)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("planToSQL(idsToJSON(blocks)): %w", err))
	}
	stmt.SetBytes("$blocks", blocks)
	stmt.SetInt64("$state_status", int64(p.State.Get().Status))
	stmt.SetInt64("$state_start", p.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", p.State.Get().End.UnixNano())
	if p.SubmitTime.Before(zeroTime) {
		stmt.SetInt64("$submit_time", zeroTime.UnixNano())
	} else {
		stmt.SetInt64("$submit_time", p.SubmitTime.UnixNano())
	}
	stmt.SetInt64("$reason", int64(p.Reason))

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("planToSQL(insertPlan): %w", err))
	}

	_, err = sStmt.Step()
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL: %w", err))
	}
	capture.Capture(stmt)

	if err := commitChecks(ctx, conn, p.ID, p.BypassChecks, capture); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitChecks(bypasschecks)): %w", err))
	}
	if err := commitChecks(ctx, conn, p.ID, p.PreChecks, capture); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitChecks(prechecks)): %w", err))
	}
	if err := commitChecks(ctx, conn, p.ID, p.PostChecks, capture); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitChecks(postchecks)): %w", err))
	}
	if err := commitChecks(ctx, conn, p.ID, p.ContChecks, capture); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitChecks(contchecks)): %w", err))
	}
	if err := commitChecks(ctx, conn, p.ID, p.DeferredChecks, capture); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitChecks(deferredchecks)): %w", err))
	}
	for i, b := range p.Blocks {
		if err := commitBlock(ctx, conn, p.ID, i, b, capture); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("planToSQL(commitBlock): %w", err))
		}
	}

	return nil
}

const insertChecks = `
	INSERT INTO checks (
		id,
		key,
		plan_id,
		actions,
		delay,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $key, $plan_id, $actions, $delay,
	$state_status, $state_start, $state_end)`

func commitChecks(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, checks *workflow.Checks, capture *CaptureStmts) error {
	if checks == nil {
		return nil
	}

	stmt := Stmt{}
	stmt.Query(insertChecks)
	actions, err := idsToJSON(checks.Actions)
	if err != nil {
		return err
	}
	stmt.SetText("$id", checks.ID.String())
	stmt.SetText("$key", checks.Key.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetBytes("$actions", actions)
	stmt.SetInt64("$delay", int64(checks.Delay))
	stmt.SetInt64("$state_status", int64(checks.State.Get().Status))
	stmt.SetInt64("$state_start", checks.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", checks.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitCheck: %w", err)
	}

	_, err = sStmt.Step()
	if err != nil {
		return fmt.Errorf("commitCheck: %w", err)
	}
	capture.Capture(stmt)

	for i, a := range checks.Actions {
		if err := commitAction(ctx, conn, planID, i, a, capture); err != nil {
			return fmt.Errorf("commitCheck: %w", err)
		}
	}

	return nil
}

const insertBlock = `
	INSERT INTO blocks (
		id,
		key,
		plan_id,
		name,
		descr,
		pos,
		entrancedelay,
		exitdelay,
		bypasschecks,
		prechecks,
		postchecks,
		contchecks,
		deferredchecks,
		sequences,
		concurrency,
		toleratedfailures,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $key, $plan_id, $name, $descr, $pos, $entrancedelay, $exitdelay, $bypasschecks, $prechecks, $postchecks, $contchecks, $deferredchecks,
	$sequences, $concurrency, $toleratedfailures,$state_status, $state_start, $state_end)`

func commitBlock(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, pos int, block *workflow.Block, capture *CaptureStmts) error {
	stmt := Stmt{}
	stmt.Query(insertBlock)

	for _, c := range [5]*workflow.Checks{block.BypassChecks, block.PreChecks, block.PostChecks, block.ContChecks, block.DeferredChecks} {
		if err := commitChecks(ctx, conn, planID, c, capture); err != nil {
			return fmt.Errorf("commitBlock: %w", err)
		}
	}

	sequences, err := idsToJSON(block.Sequences)
	if err != nil {
		return fmt.Errorf("commitBlock: %w", err)
	}

	stmt.SetText("$id", block.ID.String())
	stmt.SetText("$key", block.Key.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", block.Name)
	stmt.SetText("$descr", block.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetInt64("$entrancedelay", int64(block.EntranceDelay))
	stmt.SetInt64("$exitdelay", int64(block.ExitDelay))
	if block.BypassChecks != nil {
		stmt.SetText("$bypasschecks", block.BypassChecks.ID.String())
	}
	if block.PreChecks != nil {
		stmt.SetText("$prechecks", block.PreChecks.ID.String())
	}
	if block.PostChecks != nil {
		stmt.SetText("$postchecks", block.PostChecks.ID.String())
	}
	if block.ContChecks != nil {
		stmt.SetText("$contchecks", block.ContChecks.ID.String())
	}
	if block.DeferredChecks != nil {
		stmt.SetText("$deferredchecks", block.DeferredChecks.ID.String())
	}
	stmt.SetBytes("$sequences", sequences)
	stmt.SetInt64("$concurrency", int64(block.Concurrency))
	stmt.SetInt64("$toleratedfailures", int64(block.ToleratedFailures))
	stmt.SetInt64("$state_status", int64(block.State.Get().Status))
	stmt.SetInt64("$state_start", block.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", block.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitBlock: %w", err)
	}

	_, err = sStmt.Step()
	if err != nil {
		return fmt.Errorf("commitBlock: %w", err)
	}
	capture.Capture(stmt)

	for i, seq := range block.Sequences {
		if err := commitSequence(ctx, conn, planID, i, seq, capture); err != nil {
			return fmt.Errorf("commitBlock: %w", err)
		}
	}
	return nil
}

const insertSequence = `
	INSERT INTO sequences (
		id,
		key,
		plan_id,
		name,
		descr,
		pos,
		actions,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $key, $plan_id, $name, $descr, $pos, $actions, $state_status, $state_start, $state_end)`

func commitSequence(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, pos int, seq *workflow.Sequence, capture *CaptureStmts) error {
	stmt := Stmt{}
	stmt.Query(insertSequence)

	actions, err := idsToJSON(seq.Actions)
	if err != nil {
		return fmt.Errorf("commitSequence: %w", err)
	}

	stmt.SetText("$id", seq.ID.String())
	stmt.SetText("$key", seq.Key.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", seq.Name)
	stmt.SetText("$descr", seq.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetBytes("$actions", actions)
	stmt.SetInt64("$state_status", int64(seq.State.Get().Status))
	stmt.SetInt64("$state_start", seq.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", seq.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitSequence: %w", err)
	}

	_, err = sStmt.Step()
	if err != nil {
		return fmt.Errorf("commitSequence: %w", err)
	}
	capture.Capture(stmt)

	for i, a := range seq.Actions {
		if err := commitAction(ctx, conn, planID, i, a, capture); err != nil {
			return fmt.Errorf("commitSequence: %w", err)
		}
	}
	return nil
}

const insertAction = `
	INSERT INTO actions (
		id,
		key,
		plan_id,
		name,
		descr,
		pos,
		plugin,
		timeout,
		retries,
		req,
		attempts,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $key, $plan_id, $name, $descr, $pos, $plugin, $timeout, $retries, $req, $attempts,
	$state_status, $state_start, $state_end)`

func commitAction(ctx context.Context, conn *sqlite.Conn, planID uuid.UUID, pos int, action *workflow.Action, capture *CaptureStmts) error {
	stmt := Stmt{}
	stmt.Query(insertAction)

	req, err := json.Marshal(action.Req)
	if err != nil {
		return fmt.Errorf("commitAction: %w", err)
	}

	attempts, err := encodeAttempts(action.Attempts.Get())
	if err != nil {
		return fmt.Errorf("commitAction: %w", err)
	}

	stmt.SetText("$id", action.ID.String())
	stmt.SetText("$key", action.Key.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", action.Name)
	stmt.SetText("$descr", action.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetText("$plugin", action.Plugin)
	stmt.SetInt64("$timeout", int64(action.Timeout))
	stmt.SetInt64("$retries", int64(action.Retries))
	stmt.SetBytes("$req", req)
	if attempts != nil {
		stmt.SetBytes("$attempts", attempts)
	}
	stmt.SetInt64("$state_status", int64(action.State.Get().Status))
	stmt.SetInt64("$state_start", action.State.Get().Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.Get().End.UnixNano())

	sStmt, err := stmt.Prepare(conn)
	if err != nil {
		return fmt.Errorf("commitAction: %w", err)
	}

	_, err = sStmt.Step()
	if err != nil {
		return fmt.Errorf("commitAction: %w", err)
	}
	capture.Capture(stmt)
	return nil
}

// encodeAttempts encodes a slice of attempts into a JSON array hodling JSON encoded attempts as byte slices.
func encodeAttempts(attempts []workflow.Attempt) ([]byte, error) {
	if len(attempts) == 0 {
		return nil, nil
	}
	var out [][]byte
	if len(attempts) > 0 {
		out = make([][]byte, 0, len(attempts))
		for _, a := range attempts {
			b, err := json.Marshal(a)
			if err != nil {
				return nil, fmt.Errorf("encodeAttempts: %w", err)
			}
			out = append(out, b)
		}
	}
	return json.Marshal(out)
}

// decodeAttempts decodes a JSON array of JSON encoded attempts as byte slices into a slice of attempts.
func decodeAttempts(rawAttempts []byte, plug plugins.Plugin) ([]workflow.Attempt, error) {
	rawList := make([][]byte, 0)
	if err := json.Unmarshal(rawAttempts, &rawList); err != nil {
		return nil, fmt.Errorf("decodeAttempts: %w", err)
	}

	attempts := make([]workflow.Attempt, 0, len(rawList))
	for _, raw := range rawList {
		var a = workflow.Attempt{Resp: plug.Response()}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("decodeAttempts: %w", err)
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}

type ider interface {
	GetID() uuid.UUID
}

func idsToJSON[T any](objs []T) ([]byte, error) {
	ids := make([]string, 0, len(objs))
	for _, o := range objs {
		if ider, ok := any(o).(ider); ok {
			id := ider.GetID()
			ids = append(ids, id.String())
		} else {
			return nil, fmt.Errorf("idsToJSON: object %T does not implement ider", o)
		}
	}
	return json.Marshal(ids)
}
