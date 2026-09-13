CREATE TABLE IF NOT EXISTS images (
    content_key  TEXT PRIMARY KEY,
    size_bytes   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS build_keys (
    build_key  TEXT PRIMARY KEY,
    content_key TEXT NOT NULL REFERENCES images(content_key),
    spec       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS chart_state (
    chart_id TEXT PRIMARY KEY,
    state INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS charts (
    chart_id       TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    namespace      TEXT NOT NULL,
    maintainer     TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    spec           TEXT NOT NULL,
    updated_at     INTEGER NOT NULL
);