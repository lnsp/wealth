-- +goose Up
CREATE TABLE IF NOT EXISTS backfill_jobs (
    name TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'pending',  -- pending, running, completed, failed
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    message TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS backfill_jobs;
