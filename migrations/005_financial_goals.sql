-- +goose Up
CREATE TABLE IF NOT EXISTS financial_goals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    target_amount NUMERIC(14,2) NOT NULL,
    target_date DATE NOT NULL,
    monthly_contribution NUMERIC(10,2) NOT NULL DEFAULT 0,
    assumed_return_pct NUMERIC(5,2) NOT NULL DEFAULT 7.00,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS financial_goals;
