-- +goose Up

-- Add cash_transfer_in and cash_transfer_out to allowed transaction types.
-- These represent internal cash movements between accounts (e.g., brokerage → savings).
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN ('buy', 'sell', 'dividend', 'interest', 'deposit', 'withdrawal', 'fee', 'transfer', 'savings_plan', 'tax', 'transfer_out', 'cash_transfer_in', 'cash_transfer_out'));

-- +goose Down
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN ('buy', 'sell', 'dividend', 'interest', 'deposit', 'withdrawal', 'fee', 'transfer', 'savings_plan', 'tax', 'transfer_out'));
