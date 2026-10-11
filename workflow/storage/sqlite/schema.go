package sqlite

var tables = []string{
	planSchema,
	blocksSchema,
	checksSchema,
	sequencesSchema,
	actionsSchema,
	deferredActionsSchema,
	deferBatchesSchema,
}

var planSchema = `
CREATE Table If Not Exists plans (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL,
	name TEXT NOT NULL,
	descr TEXT NOT NULL,
	meta BLOB,
	bypasschecks TEXT,
	prechecks TEXT,
	postchecks TEXT,
	contchecks TEXT,
	deferredchecks TEXT,
	deferredactions TEXT,
	blocks BLOB NOT NULL,
	state_status INTEGER NOT NULL,
	state_start INTEGER NOT NULL,
	state_end INTEGER NOT NULL,
	submit_time INTEGER NOT NULL,
	reason INTEGER,
	runtime_update INTEGER NOT NULL DEFAULT 0
);`

// column is a column added to a table after the table was first released. CREATE TABLE IF NOT EXISTS leaves a table
// that already exists as it is, so createTables adds each one that a database is missing.
type column struct {
	table string
	name  string
	// def is the column definition ALTER TABLE ADD COLUMN takes. It needs a DEFAULT for rows that already exist.
	def string
}

// addedColumns are the columns added to tables after they were first released, in the order they were added.
var addedColumns = []column{
	// runtime_update holds Plan.RuntimeUpdate, the heartbeat startup recovery ages a running Plan out by.
	{table: "plans", name: "runtime_update", def: "INTEGER NOT NULL DEFAULT 0"},
}

var blocksSchema = `
CREATE Table If Not Exists blocks (
    id TEXT PRIMARY KEY,
    key TEXT,
    plan_id BLOB NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    pos INTEGER NOT NULL,
    entrancedelay INTEGER NOT NULL,
    exitdelay INTEGER NOT NULL,
    bypasschecks TEXT,
    prechecks TEXT,
    postchecks TEXT,
    contchecks TEXT,
    deferredchecks TEXT,
    sequences BLOB NOT NULL,
    concurrency INTEGER NOT NULL,
    toleratedfailures INTEGER NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var checksSchema = `
CREATE Table If Not Exists checks (
    id TEXT PRIMARY KEY,
    key TEXT,
    plan_id TEXT NOT NULL,
    actions BLOB NOT NULL,
    delay INTEGER NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var sequencesSchema = `
CREATE Table If Not Exists sequences (
    id TEXT PRIMARY KEY,
    key TEXT,
    plan_id TEXT NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    pos INTEGER NOT NULL,
    actions BLOB NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var actionsSchema = `
CREATE Table If Not Exists actions (
    id TEXT PRIMARY KEY,
    key TEXT,
    plan_id TEXT NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    pos INTEGER NOT NULL,
    plugin TEXT NOT NULL,
    timeout INTEGER NOT NULL,
    retries INTEGER NOT NULL,
    req BLOB,
    attempts BLOB,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var deferredActionsSchema = `
CREATE Table If Not Exists deferredactions (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    batches BLOB,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var deferBatchesSchema = `
CREATE Table If Not Exists deferbatches (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    deferredactions_id TEXT NOT NULL,
    pos INTEGER NOT NULL,
    when_run INTEGER NOT NULL,
    fail_element INTEGER NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    actions BLOB,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var indexes = []string{
	`CREATE INDEX If Not Exists idx_plans ON plans(id, group_id, state_status, state_start, state_end, reason);`,
	// idx_plans_submit orders the keyset pages that List and Search read, so a page doesn't scan and sort all plans.
	`CREATE INDEX If Not Exists idx_plans_submit ON plans(submit_time DESC, id DESC);`,
	`CREATE INDEX If Not Exists idx_blocks ON blocks(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_checks ON checks(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_sequences ON sequences(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_actions ON actions(id, key, plan_id, state_status, state_start, state_end, plugin);`,
	`CREATE INDEX If Not Exists idx_deferredactions ON deferredactions(id, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_deferbatches ON deferbatches(id, plan_id, deferredactions_id, state_status, state_start, state_end);`,
	// The idx_*_plan_id indexes let Delete find a plan's rows in each table without scanning it.
	`CREATE INDEX If Not Exists idx_blocks_plan_id ON blocks(plan_id);`,
	`CREATE INDEX If Not Exists idx_checks_plan_id ON checks(plan_id);`,
	`CREATE INDEX If Not Exists idx_sequences_plan_id ON sequences(plan_id);`,
	`CREATE INDEX If Not Exists idx_actions_plan_id ON actions(plan_id);`,
	`CREATE INDEX If Not Exists idx_deferredactions_plan_id ON deferredactions(plan_id);`,
	`CREATE INDEX If Not Exists idx_deferbatches_plan_id ON deferbatches(plan_id);`,
}
