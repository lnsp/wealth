-- +goose Up
CREATE TABLE IF NOT EXISTS decision_journal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    action_type TEXT NOT NULL, -- sell, rebalance, tax_harvest, allocation_change
    security_isin TEXT,
    amount NUMERIC(18,4),
    reason TEXT NOT NULL, -- user's "Why?" explanation
    outcome TEXT, -- retrospective: what happened after
    outcome_date TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_decision_journal_date ON decision_journal(date DESC);

-- +goose Down
DROP TABLE IF EXISTS decision_journal;
