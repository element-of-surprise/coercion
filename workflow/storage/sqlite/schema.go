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
	id BLOB PRIMARY KEY,
	group_id BLOB NOT NULL,
	name TEXT NOT NULL,
	descr TEXT NOT NULL,
	meta BLOB,
	prechecks BLOB,
	postchecks BLOB,
	contchecks BLOB,
	blocks BLOB NOT NULL,
	state_status INTEGER NOT NULL,
	state_start INTEGER NOT NULL,
	state_end INTEGER NOT NULL,
	submit_time INTEGER NOT NULL,
	reason INTEGER
);`

var blocksSchema = `
CREATE Table If Not Exists blocks (
    id BLOB PRIMARY KEY,
    plan_id BLOB NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    prechecks BLOB,
    postchecks BLOB,
    contchecks BLOB,
    sequences BLOB NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var checksSchema = `
CREATE Table If Not Exists checks (
    id BLOB PRIMARY KEY,
    plan_id BLOB NOT NULL,
    actions BLOB NOT NULL,
    delay INTEGER NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var sequencesSchema = `
CREATE Table If Not Exists sequences (
    id BLOB PRIMARY KEY,
    plan_id BLOB NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    actions BLOB NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`

var actionsSchema = `
CREATE Table If Not Exists actions (
    id BLOB PRIMARY KEY,
    plan_id BLOB NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    plugin TEXT NOT NULL,
    timeout INTEGER NOT NULL,
    retries INTEGER NOT NULL,
    req BLOB,
    attempts BLOB NOT NULL,
    state_status INTEGER NOT NULL,
    state_start INTEGER NOT NULL,
    state_end INTEGER NOT NULL
);`
