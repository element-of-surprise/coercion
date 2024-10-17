package sqlite

import (
	"strings"

	"github.com/google/uuid"
)

// This file holds various SQL statements used by the sqlite package.

const fetchPlanByID = `
SELECT
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
FROM plans
WHERE id = $id`

const fetchBlocksByID = `
SELECT
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
FROM blocks
WHERE id = $id`

const fetchChecksByID = `
SELECT
	id,
	key,
	plan_id,
	actions,
	delay,
	state_status,
	state_start,
	state_end
FROM checks
where id = $id`

const fetchSequencesByID = `
SELECT
	id,
	key,
	plan_id,
	name,
	descr,
	actions,
	state_status,
	state_start,
	state_end
FROM sequences
where id = $id`

const fetchActionsByID = `
SELECT
	id,
	key,
	plan_id,
	name,
	descr,
	plugin,
	timeout,
	retries,
	req,
	attempts,
	state_status,
	state_start,
	state_end
FROM actions
where id IN $ids
ORDER BY pos ASC`

func replaceWithIDs(query, replace string, ids []uuid.UUID) (string, []any) {
	args := make([]any, 0, len(ids))
	b := strings.Builder{}
	b.WriteString("(")
	for i := range ids {
		args = append(args, ids[i])
		if i < len(ids)-1 {
			b.WriteString("?,")
		} else {
			b.WriteString("?")
		}
	}
	b.WriteString(")")
	return strings.Replace(query, replace, b.String(), 1), args
}
