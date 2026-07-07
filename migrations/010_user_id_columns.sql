-- +goose Up

-- Add user_id to accounts (core table — transactions/holdings cascade from here)
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id);

-- Add user_id to financial_goals
ALTER TABLE financial_goals ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id);

-- Add user_id to price_alerts
ALTER TABLE price_alerts ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id);

-- Add user_id to wealth_reports
ALTER TABLE wealth_reports ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id);

-- Add user_id to target_allocations (no FK since it's keyed by security_isin)
-- Target allocations are global for now; per-user scoping comes in Step 4

-- Create indexes for user_id lookups
CREATE INDEX IF NOT EXISTS idx_accounts_user ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_goals_user ON financial_goals(user_id);
CREATE INDEX IF NOT EXISTS idx_alerts_user ON price_alerts(user_id);
CREATE INDEX IF NOT EXISTS idx_reports_user ON wealth_reports(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_reports_user;
DROP INDEX IF EXISTS idx_alerts_user;
DROP INDEX IF EXISTS idx_goals_user;
DROP INDEX IF EXISTS idx_accounts_user;
ALTER TABLE wealth_reports DROP COLUMN IF EXISTS user_id;
ALTER TABLE price_alerts DROP COLUMN IF EXISTS user_id;
ALTER TABLE financial_goals DROP COLUMN IF EXISTS user_id;
ALTER TABLE accounts DROP COLUMN IF EXISTS user_id;
