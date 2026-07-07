-- +goose Up

-- Add Revolut to the institution enum so users can create Revolut current
-- and Instant Access Savings accounts.
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other', 'morgan_stanley', 'revolut'));

-- +goose Down
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other', 'morgan_stanley'));
