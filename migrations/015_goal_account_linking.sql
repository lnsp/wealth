-- +goose Up
ALTER TABLE financial_goals
  ADD COLUMN funding_account_id UUID REFERENCES accounts(id),
  ADD COLUMN priority INTEGER NOT NULL DEFAULT 50;

-- +goose Down
ALTER TABLE financial_goals
  DROP COLUMN IF EXISTS priority,
  DROP COLUMN IF EXISTS funding_account_id;
