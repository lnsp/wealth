-- name: CreateAccount :one
INSERT INTO accounts (name, institution, type, currency, iban)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: ListAccounts :many
SELECT * FROM accounts WHERE is_active = true ORDER BY name;

-- name: UpdateAccount :exec
UPDATE accounts SET name = $2, iban = $3, is_active = $4 WHERE id = $1;

-- name: GetAccountByInstitutionAndType :one
SELECT * FROM accounts WHERE institution = $1 AND type = $2 AND is_active = true LIMIT 1;

-- name: UpsertSecurity :exec
INSERT INTO securities (isin, wkn, symbol, name, asset_class, currency)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (isin) DO UPDATE SET
    name = COALESCE(NULLIF(EXCLUDED.name, ''), securities.name),
    symbol = COALESCE(NULLIF(EXCLUDED.symbol, ''), securities.symbol);

-- name: GetSecurity :one
SELECT * FROM securities WHERE isin = $1;

-- name: ListSecurities :many
SELECT * FROM securities ORDER BY name;

-- name: UpdateSecuritySymbol :exec
UPDATE securities SET symbol = $2 WHERE isin = $1;

-- name: UpdateSecurityMetadata :exec
UPDATE securities SET
    sector_weights = $2,
    country_weights = $3,
    ter = $4,
    metadata_updated_at = now()
WHERE isin = $1;

-- name: InsertTransaction :exec
INSERT INTO transactions (account_id, date, type, security_isin, quantity, price, amount, fee, tax, currency, counterparty, reference, category, import_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (import_hash) DO NOTHING;

-- name: ListTransactions :many
SELECT t.*, a.name as account_name, a.institution
FROM transactions t
JOIN accounts a ON t.account_id = a.id
ORDER BY t.date DESC
LIMIT $1 OFFSET $2;

-- name: ListTransactionsByAccount :many
SELECT t.*, a.name as account_name, a.institution
FROM transactions t
JOIN accounts a ON t.account_id = a.id
WHERE t.account_id = $1
ORDER BY t.date DESC
LIMIT $2 OFFSET $3;

-- name: CountTransactions :one
SELECT count(*) FROM transactions;

-- name: GetCashBalance :one
SELECT COALESCE(SUM(
    CASE
        WHEN type IN ('deposit', 'interest') THEN amount
        WHEN type IN ('withdrawal', 'fee') THEN -amount
        WHEN type = 'transfer' THEN amount
        ELSE 0
    END
), 0)::numeric(18,4) as balance
FROM transactions
WHERE account_id = $1 AND security_isin IS NULL;

-- name: ListCurrentHoldings :many
SELECT ch.*, s.name, s.symbol, s.asset_class, s.currency
FROM current_holdings ch
JOIN securities s ON ch.security_isin = s.isin;

-- name: ListCurrentHoldingsByAccount :many
SELECT ch.*, s.name, s.symbol, s.asset_class, s.currency
FROM current_holdings ch
JOIN securities s ON ch.security_isin = s.isin
WHERE ch.account_id = $1;

-- name: UpsertMarketData :exec
INSERT INTO market_data (security_isin, date, close, currency)
VALUES ($1, $2, $3, $4)
ON CONFLICT (security_isin, date) DO UPDATE SET close = EXCLUDED.close;

-- name: GetLatestPrice :one
SELECT close, date, currency FROM market_data
WHERE security_isin = $1
ORDER BY date DESC LIMIT 1;

-- name: UpsertExchangeRate :exec
INSERT INTO exchange_rates (date, currency, rate)
VALUES ($1, $2, $3)
ON CONFLICT (date, currency) DO UPDATE SET rate = EXCLUDED.rate;

-- name: GetLatestExchangeRate :one
SELECT rate, date FROM exchange_rates
WHERE currency = $1
ORDER BY date DESC LIMIT 1;

-- name: UpsertETFHolding :exec
INSERT INTO etf_holdings (etf_isin, holding_isin, holding_name, weight_pct, sector, country, as_of_date)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (etf_isin, holding_isin) DO UPDATE SET
    holding_name = EXCLUDED.holding_name,
    weight_pct = EXCLUDED.weight_pct,
    sector = EXCLUDED.sector,
    country = EXCLUDED.country,
    as_of_date = EXCLUDED.as_of_date;

-- name: ListETFHoldings :many
SELECT * FROM etf_holdings WHERE etf_isin = $1 ORDER BY weight_pct DESC;

-- name: UpsertNetWorthSnapshot :exec
INSERT INTO net_worth_snapshots (date, total, cash_component, investment_component)
VALUES ($1, $2, $3, $4)
ON CONFLICT (date) DO UPDATE SET
    total = EXCLUDED.total,
    cash_component = EXCLUDED.cash_component,
    investment_component = EXCLUDED.investment_component;

-- name: ListNetWorthSnapshots :many
SELECT * FROM net_worth_snapshots ORDER BY date DESC LIMIT $1;

-- name: ListSecuritiesWithSymbol :many
SELECT isin, symbol FROM securities WHERE symbol IS NOT NULL AND symbol != '';

-- name: RefreshCurrentHoldings :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY current_holdings;
