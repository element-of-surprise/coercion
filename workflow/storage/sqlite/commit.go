package sqlite

import (
	"fmt"

	"github.com/element-of-surprise/workstream/workflow"

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
		prechecks,
		postchecks,
		contchecks,
		blocks,
		state_status,
		state_start,
		state_end,
		submit_time,
		reason
	) VALUES ($id, $group_id, $name, $descr, $meta, $prechecks, $postchecks, $contchecks, $blocks,
	$state_status, $state_start, $state_end, $submit_time, $reason)`

// commitPlan commits a plan to the database. This commits the entire plan and all sub-objects.
func commitPlan(conn *sqlite.Conn, p *workflow.Plan) error {
	if p == nil {
		return fmt.Errorf("planToSQL: plan cannot be nil")
	}

	do := func(conn *sqlite.Conn) (err error) {
		defer sqlitex.Transaction(conn)(&err)

		stmt, err := conn.Prepare(insertPlan)
		if err != nil {
			return fmt.Errorf("planToSQL(insertPlan): %w", err)
		}

		meta, err := json.Marshal(p.Meta)
		if err != nil {
			return fmt.Errorf("planToSQL(json.Marshal): %w", err)
		}

		stmt.SetText("$id", p.ID.String())
		stmt.SetText("$group_id", p.GroupID.String())
		stmt.SetText("$name", p.Name)
		stmt.SetText("$descr", p.Descr)
		stmt.SetBytes("$meta", meta)
		stmt.SetText("$prechecks", p.PreChecks.ID.String())
		stmt.SetText("$postchecks", p.PostChecks.ID.String())
		stmt.SetText("$contchecks", p.ContChecks.ID.String())

		blocks, err := idsToJSON(p.Blocks)
		if err != nil {
			return fmt.Errorf("planToSQL(idsToJSON(blocks)): %w", err)
		}
		stmt.SetBytes("$blocks", blocks)
		stmt.SetInt64("$state_status", int64(p.State.Status))
		stmt.SetInt64("$state_start", p.State.Start.UnixNano())
		stmt.SetInt64("$state_end", p.State.End.UnixNano())
		stmt.SetInt64("$submit_time", p.SubmitTime.UnixNano())
		stmt.SetInt64("$reason", int64(p.Reason))


		_, err = stmt.Step()
		if err != nil {
			return fmt.Errorf("planToSQL(plan): %w", err)
		}

		if err := commitChecks(conn, p.ID, p.PreChecks); err != nil {
			return fmt.Errorf("planToSQL(commitChecks(prechecks)): %w", err)
		}
		if err := commitChecks(conn, p.ID, p.PostChecks); err != nil {
			return fmt.Errorf("planToSQL(commitChecks(postchecks)): %w", err)
		}
		if err := commitChecks(conn, p.ID, p.ContChecks); err != nil {
			return fmt.Errorf("planToSQL(commitChecks(contchecks)): %w", err)
		}
		for i, b := range p.Blocks {
			if err := commitBlock(conn, p.ID, i, b); err != nil {
				return fmt.Errorf("planToSQL(commitBlocks): %w", err)
			}
		}

		return nil
	}

	return do(conn)
}

const insertChecks = `
	INSERT INTO checks (
		id,
		plan_id,
		actions,
		delay,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $actions, $delay,
	$state_status, $state_start, $state_end)`

func commitChecks(conn *sqlite.Conn, planID uuid.UUID, checks *workflow.Checks) error {
	stmt, err := conn.Prepare(insertChecks)
	if err != nil {
		return fmt.Errorf("conn.Prepare(insertCheck): %w", err)
	}

	actions, err := idsToJSON(checks.Actions)
	if err != nil {
		return fmt.Errorf("idsToJSON(checks.Actions): %w", err)
	}

	stmt.SetText("$id", checks.ID.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetBytes("$actions", actions)
	stmt.SetInt64("$delay", int64(checks.Delay))
	stmt.SetInt64("$state_status", int64(checks.State.Status))
	stmt.SetInt64("$state_start", checks.State.Start.UnixNano())
	stmt.SetInt64("$state_end", checks.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("commitChecks: %w", err)
	}

	for i, a := range checks.Actions {
		if err := commitAction(conn, planID, i, a); err != nil {
			return fmt.Errorf("commitAction: %w", err)
		}
	}

	return nil
}

const insertBlock = `
	INSERT INTO blocks (
		id,
		plan_id,
		name,
		descr,
		pos,
		prechecks,
		postchecks,
		contchecks,
		sequences,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $name, $descr, $pos, $prechecks, $postchecks, $contchecks, $sequences,
	$state_status, $state_start, $state_end)`

func commitBlock(conn *sqlite.Conn, planID uuid.UUID, pos int, block *workflow.Block) error {
	stmt, err := conn.Prepare(insertBlock)
	if err != nil {
		return fmt.Errorf("conn.Prepate(insertBlock): %w", err)
	}

	for _, c := range []*workflow.Checks{block.PreChecks, block.PostChecks, block.ContChecks} {
		if err := commitChecks(conn, planID, c); err != nil {
			return fmt.Errorf("commitBlock(commitChecks): %w", err)
		}
	}

	sequences, err := idsToJSON(block.Sequences)
	if err != nil {
		return fmt.Errorf("idsToJSON(sequences): %w", err)
	}

	stmt.SetText("$id", block.ID.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", block.Name)
	stmt.SetText("$descr", block.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetText("$prechecks", block.PreChecks.ID.String())
	stmt.SetText("$postchecks", block.PostChecks.ID.String())
	stmt.SetText("$contchecks", block.ContChecks.ID.String())
	stmt.SetBytes("$sequences", sequences)
	stmt.SetInt64("$state_status", int64(block.State.Status))
	stmt.SetInt64("$state_start", block.State.Start.UnixNano())
	stmt.SetInt64("$state_end", block.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return err
	}

	for i, seq := range block.Sequences {
		if err := commitSequence(conn, planID, i, seq); err != nil {
			return fmt.Errorf("(commitSequence: %w", err)
		}
	}
	return nil
}

const insertSequence = `
	INSERT INTO sequences (
		id,
		plan_id,
		name,
		descr,
		pos,
		actions,
		state_status,
		state_start,
		state_end
	) VALUES ($id, $plan_id, $name, $descr, $pos, $actions, $state_status, $state_start, $state_end)`

func commitSequence(conn *sqlite.Conn, planID uuid.UUID, pos int, seq *workflow.Sequence) error {
	stmt, err := conn.Prepare(insertSequence)
	if err != nil {
		return fmt.Errorf("conn.Prepare(insertSequence): %w", err)
	}

	actions, err := idsToJSON(seq.Actions)
	if err != nil {
		return fmt.Errorf("idsToJSON(actions): %w", err)
	}

	stmt.SetText("$id", seq.ID.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", seq.Name)
	stmt.SetText("$descr", seq.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetBytes("$actions", actions)
	stmt.SetInt64("$state_status", int64(seq.State.Status))
	stmt.SetInt64("$state_start", seq.State.Start.UnixNano())
	stmt.SetInt64("$state_end", seq.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return fmt.Errorf("commitSequence: %w", err)
	}

	for i, a := range seq.Actions {
		if err := commitAction(conn, planID, i, a); err != nil {
			return fmt.Errorf("planToSQL(commitAction): %w", err)
		}
	}
	return nil
}

const insertAction = `
	INSERT INTO actions (
		id,
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
	) VALUES ($id, $plan_id, $name, $descr, $plugin, $timeout, $retries, $req, $attempts,
	$state_status, $state_start, $state_end)`

func commitAction(conn *sqlite.Conn, planID uuid.UUID, pos int, action *workflow.Action) error {
	stmt, err := conn.Prepare(insertAction)
	if err != nil {
		return err
	}

	req, err := json.Marshal(action.Req)
	if err != nil {
		return fmt.Errorf("json.Marshal(req): %w", err)
	}
	attempts, err := json.Marshal(action.Attempts)
	if err != nil {
		return fmt.Errorf("json.Marshal(attempts): %w", err)
	}

	stmt.SetText("$id", action.ID.String())
	stmt.SetText("$plan_id", planID.String())
	stmt.SetText("$name", action.Name)
	stmt.SetText("$descr", action.Descr)
	stmt.SetInt64("$pos", int64(pos))
	stmt.SetText("$plugin", action.Plugin)
	stmt.SetInt64("$timeout", int64(action.Timeout))
	stmt.SetInt64("$retries", int64(action.Retries))
	stmt.SetBytes("$req", req)
	stmt.SetBytes("$attempts", attempts)
	stmt.SetInt64("$state_status", int64(action.State.Status))
	stmt.SetInt64("$state_start", action.State.Start.UnixNano())
	stmt.SetInt64("$state_end", action.State.End.UnixNano())

	_, err = stmt.Step()
	if err != nil {
		return err
	}
	return nil
}

type ider interface {
	GetID() uuid.UUID
}

func idsToJSON[T any] (objs []T) ([]byte, error) {
	ids := make([]string, 0, len(objs))
	for _, o := range objs {
		if ider, ok := any(o).(ider); ok {
			id := ider.GetID()
			ids = append(ids, id.String())
		}else{
			return nil, fmt.Errorf("idsToJSON: object %T does not implement ider", o)
		}
	}
	return json.Marshal(ids)
}
