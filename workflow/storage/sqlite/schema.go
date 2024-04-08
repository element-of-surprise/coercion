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
	prechecks TEXT,
	postchecks TEXT,
	contchecks TEXT,
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
    plan_id BLOB NOT NULL,
    name TEXT NOT NULL,
    descr TEXT NOT NULL,
    pos INTEGER NOT NULL,
    entrancedelay INTEGER NOT NULL,
    exitdelay INTEGER NOT NULL,
    prechecks TEXT,
    postchecks TEXT,
    contchecks TEXT,
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
