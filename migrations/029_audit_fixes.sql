-- +goose Up

-- 1. Ensure user_id foreign keys do not block deleting a household login (H1).
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_user_id_fkey;
ALTER TABLE accounts ADD CONSTRAINT accounts_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE financial_goals DROP CONSTRAINT IF EXISTS financial_goals_user_id_fkey;
ALTER TABLE financial_goals ADD CONSTRAINT financial_goals_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE price_alerts DROP CONSTRAINT IF EXISTS price_alerts_user_id_fkey;
ALTER TABLE price_alerts ADD CONSTRAINT price_alerts_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE wealth_reports DROP CONSTRAINT IF EXISTS wealth_reports_user_id_fkey;
ALTER TABLE wealth_reports ADD CONSTRAINT wealth_reports_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

-- 2. Ensure account/transaction deletions do not fail with FK errors mid-flight (C11.1).
ALTER TABLE financial_goals DROP CONSTRAINT IF EXISTS financial_goals_funding_account_id_fkey;
ALTER TABLE financial_goals ADD CONSTRAINT financial_goals_funding_account_id_fkey
    FOREIGN KEY (funding_account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE rsu_vests DROP CONSTRAINT IF EXISTS rsu_vests_transaction_id_fkey;
ALTER TABLE rsu_vests ADD CONSTRAINT rsu_vests_transaction_id_fkey
    FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE SET NULL;

-- 3. Track linked transaction directly on asset_improvements so deleting an
-- improvement cleanly unlinks asset_transactions and resets category (C11.3).
ALTER TABLE asset_improvements
    ADD COLUMN IF NOT EXISTS transaction_id UUID UNIQUE REFERENCES transactions(id) ON DELETE SET NULL;

-- Clean up any previously orphaned improvement links on assets with no improvements.
UPDATE transactions t
SET category = ''
WHERE t.category = 'asset_purchase'
  AND EXISTS (
      SELECT 1 FROM asset_transactions atx
      WHERE atx.transaction_id = t.id
        AND atx.purpose = 'improvement'
        AND NOT EXISTS (
            SELECT 1 FROM asset_improvements ai WHERE ai.asset_id = atx.asset_id
        )
  );

DELETE FROM asset_transactions atx
WHERE atx.purpose = 'improvement'
  AND NOT EXISTS (
      SELECT 1 FROM asset_improvements ai WHERE ai.asset_id = atx.asset_id
  );

-- 4. Allow 'crypto' as a first-class account type alongside brokerage (H6/H9),
-- and store optional fund equity_pct on securities for InvStG Teilfreistellung (H8.3).
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check
    CHECK (type IN ('checking', 'savings', 'brokerage', 'crypto', 'credit', 'real_estate', 'pension', 'precious_metals', 'liability'));

ALTER TABLE securities
    ADD COLUMN IF NOT EXISTS equity_pct NUMERIC(7, 4);

-- 5. Recreate current_holdings materialized view with moving-average cost basis
-- (preventing negative avg_cost_basis after profitable partial sells) and active-account
-- filtering (H4).
DROP MATERIALIZED VIEW IF EXISTS current_holdings;
CREATE MATERIALIZED VIEW current_holdings AS
WITH RECURSIVE sec_txns AS (
    SELECT
        t.account_id,
        t.security_isin,
        t.type,
        COALESCE(t.quantity, 0)::numeric AS quantity,
        t.amount::numeric AS amount,
        COALESCE(t.fee, 0)::numeric AS fee,
        ROW_NUMBER() OVER (
            PARTITION BY t.account_id, t.security_isin
            ORDER BY t.date ASC,
                     CASE WHEN t.type IN ('buy', 'savings_plan', 'transfer') THEN 0 ELSE 1 END ASC,
                     t.id ASC
        ) AS rn
    FROM transactions t
    JOIN accounts a ON a.id = t.account_id
    WHERE a.is_active = true
      AND t.security_isin IS NOT NULL
      AND t.type IN ('buy', 'savings_plan', 'transfer', 'sell', 'transfer_out')
),
running AS (
    SELECT
        account_id,
        security_isin,
        rn,
        CASE WHEN type IN ('buy', 'savings_plan', 'transfer') THEN quantity
             ELSE 0::numeric END AS rem_qty,
        CASE WHEN type IN ('buy', 'savings_plan', 'transfer') THEN amount + fee
             ELSE 0::numeric END AS rem_cost
    FROM sec_txns
    WHERE rn = 1
    UNION ALL
    SELECT
        s.account_id,
        s.security_isin,
        s.rn,
        CASE
            WHEN s.type IN ('buy', 'savings_plan', 'transfer') THEN r.rem_qty + s.quantity
            ELSE GREATEST(0::numeric, r.rem_qty - s.quantity)
        END AS rem_qty,
        CASE
            WHEN s.type IN ('buy', 'savings_plan', 'transfer') THEN r.rem_cost + s.amount + s.fee
            WHEN r.rem_qty <= 0 OR s.quantity >= r.rem_qty THEN 0::numeric
            ELSE r.rem_cost * ((r.rem_qty - s.quantity) / r.rem_qty)
        END AS rem_cost
    FROM running r
    JOIN sec_txns s
      ON s.account_id = r.account_id
     AND s.security_isin = r.security_isin
     AND s.rn = r.rn + 1
),
final_lots AS (
    SELECT DISTINCT ON (account_id, security_isin)
        account_id,
        security_isin,
        rem_qty,
        rem_cost
    FROM running
    ORDER BY account_id, security_isin, rn DESC
),
divs AS (
    SELECT
        t.account_id,
        t.security_isin,
        SUM(t.amount) AS total_dividends
    FROM transactions t
    JOIN accounts a ON a.id = t.account_id
    WHERE a.is_active = true
      AND t.security_isin IS NOT NULL
      AND t.type = 'dividend'
    GROUP BY t.account_id, t.security_isin
)
SELECT
    fl.account_id,
    fl.security_isin,
    fl.rem_qty AS quantity,
    (fl.rem_cost / NULLIF(fl.rem_qty, 0)) AS avg_cost_basis,
    COALESCE(d.total_dividends, 0) AS total_dividends
FROM final_lots fl
LEFT JOIN divs d
  ON d.account_id = fl.account_id
 AND d.security_isin = fl.security_isin
WHERE fl.rem_qty > 0.000000001;

CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin);

-- +goose Down
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

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check
    CHECK (type IN ('checking', 'savings', 'brokerage', 'credit', 'real_estate', 'pension', 'precious_metals', 'liability'));

ALTER TABLE securities DROP COLUMN IF EXISTS equity_pct;
ALTER TABLE asset_improvements DROP COLUMN IF EXISTS transaction_id;
