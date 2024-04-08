package sqlite

// This file holds various SQL statements used by the sqlite package.

const fetchPlanByID = `
SELECT
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
FROM plans
WHERE id = $id`

const fetchBlocksByID = `
SELECT
	id,
	plan_id,
	name,
	descr,
	pos,
	entrancedelay,
	exitdelay,
	prechecks,
	postchecks,
	contchecks,
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
where id = ($ids)
ORDER BY pos ASC`
