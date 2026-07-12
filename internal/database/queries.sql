-- name: CreateAccount :one
INSERT INTO accounts (name, institution, type, currency, iban, tax_treatment, employer_match_pct, import_security_isin)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: ListAccounts :many
SELECT * FROM accounts WHERE is_active = true ORDER BY name;

-- name: ListAllAccounts :many
SELECT * FROM accounts ORDER BY is_active DESC, name;

-- name: UpdateAccount :exec
UPDATE accounts SET name = $2, iban = $3, is_active = $4, tax_treatment = $5, employer_match_pct = $6, import_security_isin = $7, currency = $8 WHERE id = $1;

-- name: DeleteAccountRSUVests :exec
-- Must run before DeleteAccountTransactions: rsu_vests.transaction_id
-- references transactions(id) with no ON DELETE behavior, so removing the
-- linked transactions first would violate the FK constraint.
DELETE FROM rsu_vests WHERE account_id = $1;

-- name: DeleteAccountTransactions :exec
DELETE FROM transactions WHERE account_id = $1;

-- name: DeleteAccountImportHistory :exec
DELETE FROM import_history WHERE account_id = $1;

-- name: DeleteAccount :exec
DELETE FROM accounts WHERE id = $1;

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
ON CONFLICT (import_hash) DO UPDATE SET type = EXCLUDED.type;

-- name: ListTransactions :many
SELECT t.*, a.name as account_name, a.institution
FROM transactions t
JOIN accounts a ON t.account_id = a.id
ORDER BY t.date DESC
LIMIT $1 OFFSET $2;

-- name: ListTransactionsByISIN :many
SELECT * FROM transactions WHERE security_isin = $1 ORDER BY date;

-- name: ListTransactionsByAccount :many
SELECT t.*, a.name as account_name, a.institution
FROM transactions t
JOIN accounts a ON t.account_id = a.id
WHERE t.account_id = $1
ORDER BY t.date DESC
LIMIT $2 OFFSET $3;

-- name: CountTransactions :one
SELECT count(*) FROM transactions;

-- name: CheckFileImportedToOtherAccount :one
SELECT a.name as account_name FROM import_history ih
JOIN accounts a ON ih.account_id = a.id
WHERE ih.account_id != $1 AND ih.filename = $2 AND ih.imported > 0
LIMIT 1;

-- name: GetCashBalance :one
SELECT COALESCE(SUM(
    CASE
        WHEN type IN ('deposit', 'interest', 'sell', 'dividend', 'cash_transfer_in') THEN amount
        WHEN type IN ('buy', 'savings_plan', 'withdrawal', 'fee', 'tax', 'cash_transfer_out') THEN -amount
        WHEN type = 'transfer' AND security_isin IS NULL THEN -amount
        -- security transfers (in-kind) have no cash impact
        ELSE 0
    END
), 0)::numeric(18,4) as balance
FROM transactions
WHERE account_id = $1;

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

-- name: ListLatestPrices :many
SELECT DISTINCT ON (security_isin) security_isin, close, date, currency
FROM market_data
ORDER BY security_isin, date DESC;

-- name: GetPriceAtDate :one
SELECT close, date, currency FROM market_data
WHERE security_isin = $1 AND date <= $2
ORDER BY date DESC LIMIT 1;

-- name: ListPriceHistory :many
SELECT date, close FROM market_data
WHERE security_isin = $1
ORDER BY date ASC;

-- name: UpsertExchangeRate :exec
INSERT INTO exchange_rates (date, currency, rate)
VALUES ($1, $2, $3)
ON CONFLICT (date, currency) DO UPDATE SET rate = EXCLUDED.rate;

-- name: GetLatestExchangeRate :one
SELECT rate, date FROM exchange_rates
WHERE currency = $1
ORDER BY date DESC LIMIT 1;

-- name: ListExchangeRateHistory :many
SELECT date, rate FROM exchange_rates
WHERE currency = $1
ORDER BY date ASC;

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

-- name: InsertNetWorthIntraday :exec
INSERT INTO net_worth_intraday (recorded_at, total, cash_component, investment_component)
VALUES ($1, $2, $3, $4);

-- name: ListNetWorthIntraday :many
SELECT * FROM net_worth_intraday
WHERE recorded_at >= $1
ORDER BY recorded_at ASC;

-- name: PruneNetWorthIntraday :exec
DELETE FROM net_worth_intraday WHERE recorded_at < NOW() - INTERVAL '7 days';

-- name: ListSecuritiesWithSymbol :many
SELECT isin, symbol FROM securities WHERE symbol IS NOT NULL AND symbol != '';

-- name: InsertImportHistory :exec
INSERT INTO import_history (account_id, institution, filename, total_rows, imported, skipped, new_securities)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListImportHistory :many
SELECT ih.*, a.name as account_name
FROM import_history ih
JOIN accounts a ON ih.account_id = a.id
ORDER BY ih.imported_at DESC
LIMIT $1;

-- name: RefreshCurrentHoldings :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY current_holdings;

-- name: ListTargetAllocations :many
SELECT ta.security_isin, ta.target_weight_pct, ta.updated_at, s.name as security_name
FROM target_allocations ta
JOIN securities s ON ta.security_isin = s.isin
ORDER BY ta.target_weight_pct DESC;

-- name: UpsertTargetAllocation :exec
INSERT INTO target_allocations (security_isin, target_weight_pct, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (security_isin) DO UPDATE SET target_weight_pct = $2, updated_at = now();

-- name: DeleteTargetAllocation :exec
DELETE FROM target_allocations WHERE security_isin = $1;

-- name: ListFinancialGoals :many
SELECT * FROM financial_goals ORDER BY target_date;

-- name: CreateFinancialGoal :one
INSERT INTO financial_goals (name, target_amount, target_date, monthly_contribution, assumed_return_pct, funding_account_id, priority)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateFinancialGoal :exec
UPDATE financial_goals SET name = $2, target_amount = $3, target_date = $4,
    monthly_contribution = $5, assumed_return_pct = $6, funding_account_id = $7, priority = $8, updated_at = now()
WHERE id = $1;

-- name: DeleteFinancialGoal :exec
DELETE FROM financial_goals WHERE id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY created_at;

-- name: CreateNotificationChannel :one
INSERT INTO notification_channels (type, name, config, enabled, channel_for, digest_frequency)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateNotificationChannel :exec
UPDATE notification_channels SET name = $2, config = $3, enabled = $4, channel_for = $5 WHERE id = $1;

-- name: DeleteNotificationChannel :exec
DELETE FROM notification_channels WHERE id = $1;

-- name: ListPriceAlerts :many
SELECT pa.*, s.name as security_name
FROM price_alerts pa
LEFT JOIN securities s ON pa.security_isin = s.isin
ORDER BY pa.created_at DESC;

-- name: ListActivePriceAlerts :many
SELECT pa.*, s.name as security_name
FROM price_alerts pa
LEFT JOIN securities s ON pa.security_isin = s.isin
WHERE pa.is_active = true;

-- name: CreatePriceAlert :one
INSERT INTO price_alerts (alert_type, security_isin, threshold)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeletePriceAlert :exec
DELETE FROM price_alerts WHERE id = $1;

-- name: TogglePriceAlert :exec
UPDATE price_alerts SET is_active = NOT is_active WHERE id = $1;

-- name: InsertNotification :exec
INSERT INTO notifications (alert_id, message, value)
VALUES ($1, $2, $3);

-- name: ListNotifications :many
SELECT n.*, COALESCE(pa.alert_type, '') as alert_type, COALESCE(pa.security_isin, '') as security_isin
FROM notifications n
LEFT JOIN price_alerts pa ON n.alert_id = pa.id
ORDER BY n.triggered_at DESC
LIMIT $1;

-- name: MarkNotificationsRead :exec
UPDATE notifications SET is_read = true WHERE is_read = false;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications WHERE is_read = false;

-- name: ListWealthReports :many
SELECT * FROM wealth_reports ORDER BY period_start DESC LIMIT $1;

-- name: GetWealthReport :one
SELECT * FROM wealth_reports WHERE id = $1;

-- name: InsertWealthReport :exec
INSERT INTO wealth_reports (report_type, period_label, period_start, period_end, data)
VALUES ($1, $2, $3, $4, $5);

-- name: ReportExistsForPeriod :one
SELECT EXISTS(SELECT 1 FROM wealth_reports WHERE report_type = $1 AND period_label = $2);

-- name: CreateUser :one
INSERT INTO users (username, password_hash, role)
VALUES ($1, $2, $3) RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT id, username, role, is_active, created_at FROM users ORDER BY created_at;

-- name: UpdateUserActive :exec
UPDATE users SET is_active = $2 WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateJournalEntry :one
INSERT INTO decision_journal (action_type, security_isin, amount, reason)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListJournalEntries :many
SELECT * FROM decision_journal ORDER BY date DESC LIMIT $1;

-- name: UpdateJournalOutcome :exec
UPDATE decision_journal SET outcome = $2, outcome_date = NOW() WHERE id = $1;

-- name: DeleteJournalEntry :exec
DELETE FROM decision_journal WHERE id = $1;

-- name: SetUserTOTPSecret :exec
UPDATE users SET totp_secret = $2 WHERE id = $1;

-- name: EnableUserTOTP :exec
UPDATE users SET totp_enabled = true WHERE id = $1;

-- name: DisableUserTOTP :exec
UPDATE users SET totp_enabled = false, totp_secret = '' WHERE id = $1;

-- name: GetTransactionIDByHash :one
SELECT id FROM transactions WHERE import_hash = $1;

-- name: ListSimilarTransactions :many
-- Finds existing rows that carry the same economic content as an incoming
-- import row, regardless of import_hash. Used to detect duplicates when the
-- hash scheme changed between importer versions. Amount/fee/tax are rounded
-- to the column scale so higher-precision parser values still match.
SELECT id, import_hash FROM transactions
WHERE account_id = $1
  AND date = $2
  AND type = $3
  AND security_isin IS NOT DISTINCT FROM $4
  AND coalesce(quantity, 0) = coalesce(round(sqlc.arg(quantity)::numeric, 8), 0)
  AND amount = round(sqlc.arg(amount)::numeric, 4)
  AND fee = round(sqlc.arg(fee)::numeric, 4)
  AND tax = round(sqlc.arg(tax)::numeric, 4)
  AND currency = sqlc.arg(currency)
ORDER BY id;

-- name: UpdateTransactionImportHash :exec
UPDATE transactions SET import_hash = $2 WHERE id = $1;

-- name: InsertRSUVest :exec
INSERT INTO rsu_vests (
    account_id, security_isin, vest_date, grant_number,
    gross_quantity, net_quantity, price, currency, vested, transaction_id, import_hash
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (import_hash) DO NOTHING;

-- name: GetPostTaxRatio :one
SELECT
    COALESCE(SUM(net_quantity), 0)::numeric(18,8) AS net_total,
    COALESCE(SUM(gross_quantity), 0)::numeric(18,8) AS gross_total
FROM rsu_vests
WHERE account_id = $1 AND vested = true AND net_quantity IS NOT NULL;

-- name: ListUnvestedRSUVests :many
SELECT
    id, account_id, security_isin, vest_date, grant_number,
    gross_quantity, currency
FROM rsu_vests
WHERE account_id = $1 AND vested = false
ORDER BY vest_date, grant_number;

-- name: DeleteUnvestedRSUVestsForAccount :exec
DELETE FROM rsu_vests WHERE account_id = $1 AND vested = false;

-- name: ListCashTransactions :many
-- Cash-account transactions used for cashflow classification. Excludes
-- internal transfers and investment moves — only true income/spend events.
SELECT t.id, t.date, t.type, t.amount, t.counterparty, t.reference, t.category,
       a.name as account_name
FROM transactions t
JOIN accounts a ON t.account_id = a.id
WHERE a.type IN ('checking', 'savings')
  AND t.date >= $1 AND t.date <= $2
  AND t.amount != 0
ORDER BY t.date DESC;

-- name: UpdateTransactionCategory :exec
UPDATE transactions SET category = $2 WHERE id = $1;
