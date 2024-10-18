package sqlite

var tables = []string{
	planSchema,
	blocksSchema,
	checksSchema,
	sequencesSchema,
	actionsSchema,
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
	blocks BLOB NOT NULL,
	state_status INTEGER NOT NULL,
	state_start INTEGER NOT NULL,
	state_end INTEGER NOT NULL,
	submit_time INTEGER NOT NULL,
	reason INTEGER
);`

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

var indexes = []string{
	`CREATE INDEX If Not Exists idx_plans ON plans(id, group_id, state_status, state_start, state_end, reason);`,
	`CREATE INDEX If Not Exists idx_blocks ON blocks(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_checks ON checks(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_sequences ON sequences(id, key, plan_id, state_status, state_start, state_end);`,
	`CREATE INDEX If Not Exists idx_actions ON actions(id, key, plan_id, state_status, state_start, state_end, plugin);`,
}
