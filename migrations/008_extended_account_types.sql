-- +goose Up
-- Extend account types to support multi-asset wealth tracking
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check
    CHECK (type IN ('checking', 'savings', 'brokerage', 'credit', 'real_estate', 'pension', 'precious_metals', 'liability'));

-- Extend institution list to support manual entry
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other'));

-- +goose Down
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check
    CHECK (type IN ('checking', 'savings', 'brokerage'));

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital'));
