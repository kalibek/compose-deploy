CREATE TABLE stacks (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL UNIQUE,
    repo_url           TEXT    NOT NULL,
    branch             TEXT    NOT NULL,
    compose_path       TEXT    NOT NULL,
    auto_deploy        INTEGER NOT NULL DEFAULT 1,
    token_enc          TEXT    NOT NULL DEFAULT '',
    webhook_secret_enc TEXT    NOT NULL DEFAULT '',
    env_enc            TEXT    NOT NULL DEFAULT '',
    status             TEXT    NOT NULL DEFAULT 'idle',
    last_sha           TEXT    NOT NULL DEFAULT '',
    last_deploy_at     TEXT,
    last_error         TEXT    NOT NULL DEFAULT '',
    last_log           TEXT    NOT NULL DEFAULT '',
    created_at         TEXT    NOT NULL,
    updated_at         TEXT    NOT NULL
);

CREATE TABLE registries (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    url          TEXT NOT NULL UNIQUE,
    username     TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
