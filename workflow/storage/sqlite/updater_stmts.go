package sqlite

const updatePlan = `
UPDATE plans
SET
	reason = $reason,
	state_status = $state_status,
	state_start = $state_start,
	state_end = $state_end
WHERE id = $id`

const updateChecks = `
UPDATE checks
SET
	state_status = $state_status,
	state_start = $state_start,
	state_end = $state_end
WHERE id = $id`

const updateBlock = `
UPDATE blocks
SET
	state_status = $state_status,
	state_start = $state_start,
	state_end = $state_end
WHERE id = $id`

const updateSequence = `
UPDATE sequences
SET
	state_status = $state_status,
	state_start = $state_start,
	state_end = $state_end
WHERE id = $id`

const updateAction = `
UPDATE actions
SET
	attempts = $attempts,
	state_status = $state_status,
	state_start = $state_start,
	state_end = $state_end
WHERE id = $id`
