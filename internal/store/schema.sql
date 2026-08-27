CREATE TABLE IF NOT EXISTS images (
    content_id   TEXT PRIMARY KEY,
    size_bytes   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS build_keys (
    build_key  TEXT PRIMARY KEY,
    content_id TEXT NOT NULL REFERENCES images(content_id),
    spec       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
