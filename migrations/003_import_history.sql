-- +goose Up
CREATE TABLE IF NOT EXISTS import_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    institution TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    total_rows INTEGER NOT NULL DEFAULT 0,
    imported INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    new_securities TEXT[] NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_import_history_account ON import_history(account_id);
CREATE INDEX idx_import_history_date ON import_history(imported_at DESC);

-- +goose Down
DROP TABLE IF EXISTS import_history;
