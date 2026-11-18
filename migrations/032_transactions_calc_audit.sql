-- +goose Up
-- Calculation audit 2026-11-18, Transactions & Settings.

-- 1. Amount signs (root cause 1). Stored amounts are magnitudes and the type
--    gives the direction: txnSignedCashDelta (fx_cash.go), the cash queries
--    and the importer (normalizeTxnSigns in import.go) all assume it. Rows
--    stored with a signed amount are rewritten with the importer's rules, so
--    every reader computes the same cash:
--      interest < 0         → fee (a charge, e.g. overdraft interest)
--      dividend < 0         → fee (there is no dividend-reversal type)
--      deposit < 0          → withdrawal
--      cash_transfer_in < 0 → cash_transfer_out
--      any other type       → magnitude, type unchanged
--    Fees and taxes always reduce cash and are stored as magnitudes too.
UPDATE transactions SET type = 'fee'
 WHERE amount < 0 AND type IN ('interest', 'dividend');
UPDATE transactions SET type = 'withdrawal'
 WHERE amount < 0 AND type = 'deposit';
UPDATE transactions SET type = 'cash_transfer_out'
 WHERE amount < 0 AND type = 'cash_transfer_in';
UPDATE transactions SET amount = -amount WHERE amount < 0;
UPDATE transactions SET fee = -fee WHERE fee < 0;
UPDATE transactions SET tax = -tax WHERE tax < 0;

-- The rules above cover every type, and the importer is the only writer of
-- transactions and applies the same rules, so the convention can be enforced.
ALTER TABLE transactions ADD CONSTRAINT transactions_magnitudes_nonnegative
    CHECK (amount >= 0 AND fee >= 0 AND tax >= 0);

-- 2. Morgan Stanley sales (W1): the Withdrawals Report only has the net
--    amount, which was stored as the sale amount with fee 0. Store gross
--    proceeds (quantity x price, rounded to cents) and the deducted fee, as
--    msSaleGrossAndFee in the parser now does, so re-imports of these rows
--    still match them in ListSimilarTransactions. Sales whose gross does not
--    exceed the net amount (rounding noise) are left unchanged, like the
--    parser does. gross - fee is the old amount, so cash does not change.
UPDATE transactions t
   SET amount = ROUND(t.quantity * t.price, 2),
       fee    = ROUND(t.quantity * t.price, 2) - t.amount
  FROM accounts a
 WHERE a.id = t.account_id
   AND t.type = 'sell'
   AND (a.institution = 'morgan_stanley' OR t.import_hash LIKE 'ms-sell-%')
   AND t.fee = 0
   AND t.quantity > 0
   AND t.price > 0
   AND ROUND(t.quantity * t.price, 2) > t.amount;

-- Cash rows carry no quantity or price (the auto-wire withdrawals stored 0).
UPDATE transactions SET quantity = NULL WHERE security_isin IS NULL AND quantity = 0;
UPDATE transactions SET price = NULL WHERE security_isin IS NULL AND price = 0;

-- 3. Import history records re-import corrections and RSU vesting rows (I5).
ALTER TABLE import_history ADD COLUMN IF NOT EXISTS updated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_history ADD COLUMN IF NOT EXISTS rsu_vests INTEGER NOT NULL DEFAULT 0;

-- 4. Employer match is a percentage up to 100; NUMERIC(6,4) stopped at 99.9999 (I8).
ALTER TABLE accounts ALTER COLUMN employer_match_pct TYPE NUMERIC(7,4);

REFRESH MATERIALIZED VIEW current_holdings;

-- +goose Down
ALTER TABLE accounts ALTER COLUMN employer_match_pct TYPE NUMERIC(6,4)
    USING LEAST(employer_match_pct, 99.9999);
ALTER TABLE import_history DROP COLUMN IF EXISTS rsu_vests;
ALTER TABLE import_history DROP COLUMN IF EXISTS updated;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_magnitudes_nonnegative;
-- The sign normalisation, the Morgan Stanley gross/fee split and the NULL
-- quantities are not reverted: they describe the same cash effect the
-- application computed before, and the signs cannot be recovered.
