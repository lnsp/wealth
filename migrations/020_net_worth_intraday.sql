-- +goose Up
CREATE TABLE net_worth_intraday (
    recorded_at TIMESTAMPTZ PRIMARY KEY,
    total NUMERIC(18,4) NOT NULL,
    cash_component NUMERIC(18,4) NOT NULL DEFAULT 0,
    investment_component NUMERIC(18,4) NOT NULL DEFAULT 0
);

-- Keep only last 7 days of intraday data (pruned by scheduler)
CREATE INDEX idx_net_worth_intraday_date ON net_worth_intraday (recorded_at DESC);

-- +goose Down
DROP TABLE IF EXISTS net_worth_intraday;
