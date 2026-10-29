-- One row per deploy/stop run of a stack: what triggered it, which version it
-- deployed, how it ended and its log.
CREATE TABLE deployments (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id       INTEGER NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
    action         TEXT    NOT NULL,             -- 'deploy' | 'stop'
    triggered_by   TEXT    NOT NULL,             -- 'manual' | 'webhook' | 'poll' | 'config'
    trigger_detail TEXT    NOT NULL DEFAULT '',  -- e.g. "push to main"
    status         TEXT    NOT NULL,             -- 'deploying' | 'success' | 'failed' | 'stopped'
    sha            TEXT    NOT NULL DEFAULT '',  -- commit (or inline revision) deployed
    error          TEXT    NOT NULL DEFAULT '',
    log            TEXT    NOT NULL DEFAULT '',
    started_at     TEXT    NOT NULL,
    finished_at    TEXT
);

CREATE INDEX deployments_stack ON deployments (stack_id, id);
