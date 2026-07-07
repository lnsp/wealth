-- +goose Up
CREATE TABLE IF NOT EXISTS target_allocations (
    security_isin TEXT NOT NULL REFERENCES securities(isin),
    target_weight_pct NUMERIC(6,2) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (security_isin)
);

-- +goose Down
DROP TABLE IF EXISTS target_allocations;
