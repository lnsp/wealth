-- +goose Up

-- Add 'transfer_out' to the allowed transaction types.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN ('buy', 'sell', 'dividend', 'interest', 'deposit', 'withdrawal', 'fee', 'transfer', 'savings_plan', 'tax', 'transfer_out'));

-- Delete existing security transfer rows so they are re-imported with correct
-- types (transfer vs transfer_out). Only affects in-kind security transfers;
-- cash transfers (security_isin IS NULL) are left untouched.
DELETE FROM transactions WHERE type = 'transfer' AND security_isin IS NOT NULL;

-- Recreate the materialized view to handle transfer_out (subtract shares).
DROP MATERIALIZED VIEW IF EXISTS current_holdings;
CREATE MATERIALIZED VIEW current_holdings AS
SELECT
    t.account_id,
    t.security_isin,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
             WHEN t.type IN ('sell', 'transfer_out') THEN -t.quantity
             ELSE 0 END) AS quantity,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.amount + t.fee
             WHEN t.type IN ('sell', 'transfer_out') THEN -(t.amount - t.fee)
             ELSE 0 END) /
    NULLIF(SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
                     WHEN t.type IN ('sell', 'transfer_out') THEN -t.quantity
                     ELSE 0 END), 0) AS avg_cost_basis,
    SUM(CASE WHEN t.type = 'dividend' THEN t.amount ELSE 0 END) AS total_dividends
FROM transactions t
WHERE t.security_isin IS NOT NULL
GROUP BY t.account_id, t.security_isin
HAVING SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
                 WHEN t.type IN ('sell', 'transfer_out') THEN -t.quantity
                 ELSE 0 END) > 0;

CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin);

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS current_holdings;

-- Restore original materialized view without transfer_out handling.
CREATE MATERIALIZED VIEW current_holdings AS
SELECT
    t.account_id,
    t.security_isin,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
             WHEN t.type = 'sell' THEN -t.quantity
             ELSE 0 END) AS quantity,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.amount + t.fee
             WHEN t.type = 'sell' THEN -(t.amount - t.fee)
             ELSE 0 END) /
    NULLIF(SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
                     WHEN t.type = 'sell' THEN -t.quantity
                     ELSE 0 END), 0) AS avg_cost_basis,
    SUM(CASE WHEN t.type = 'dividend' THEN t.amount ELSE 0 END) AS total_dividends
FROM transactions t
WHERE t.security_isin IS NOT NULL
GROUP BY t.account_id, t.security_isin
HAVING SUM(CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN t.quantity
                 WHEN t.type = 'sell' THEN -t.quantity
                 ELSE 0 END) > 0;

CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin);

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN ('buy', 'sell', 'dividend', 'interest', 'deposit', 'withdrawal', 'fee', 'transfer', 'savings_plan', 'tax'));
