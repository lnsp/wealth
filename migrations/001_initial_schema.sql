-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    institution TEXT NOT NULL CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital')),
    type TEXT NOT NULL CHECK (type IN ('checking', 'savings', 'brokerage')),
    currency TEXT NOT NULL DEFAULT 'EUR',
    iban TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE securities (
    isin TEXT PRIMARY KEY,
    wkn TEXT,
    symbol TEXT,
    name TEXT NOT NULL,
    asset_class TEXT NOT NULL CHECK (asset_class IN ('etf', 'stock', 'bond', 'fund', 'commodity')),
    currency TEXT NOT NULL DEFAULT 'EUR',
    ter NUMERIC(6,4),
    sector_weights JSONB DEFAULT '{}',
    country_weights JSONB DEFAULT '{}',
    metadata_updated_at TIMESTAMPTZ
);

CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    date DATE NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('buy', 'sell', 'dividend', 'interest', 'deposit', 'withdrawal', 'fee', 'transfer', 'savings_plan')),
    security_isin TEXT REFERENCES securities(isin),
    quantity NUMERIC(18,8),
    price NUMERIC(18,8),
    amount NUMERIC(18,4) NOT NULL,
    fee NUMERIC(18,4) NOT NULL DEFAULT 0,
    tax NUMERIC(18,4) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'EUR',
    counterparty TEXT,
    reference TEXT,
    category TEXT,
    import_hash TEXT UNIQUE NOT NULL
);

CREATE INDEX idx_transactions_account_id ON transactions(account_id);
CREATE INDEX idx_transactions_date ON transactions(date);
CREATE INDEX idx_transactions_security_isin ON transactions(security_isin);
CREATE INDEX idx_transactions_type ON transactions(type);

CREATE TABLE market_data (
    security_isin TEXT NOT NULL REFERENCES securities(isin),
    date DATE NOT NULL,
    close NUMERIC(18,8) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    PRIMARY KEY (security_isin, date)
);

CREATE TABLE etf_holdings (
    etf_isin TEXT NOT NULL REFERENCES securities(isin),
    holding_isin TEXT NOT NULL,
    holding_name TEXT NOT NULL,
    weight_pct NUMERIC(8,4) NOT NULL,
    sector TEXT,
    country TEXT,
    as_of_date DATE NOT NULL,
    PRIMARY KEY (etf_isin, holding_isin)
);

CREATE TABLE exchange_rates (
    date DATE NOT NULL,
    currency TEXT NOT NULL,
    rate NUMERIC(18,8) NOT NULL,
    PRIMARY KEY (date, currency)
);

CREATE TABLE net_worth_snapshots (
    date DATE PRIMARY KEY,
    total NUMERIC(18,4) NOT NULL,
    cash_component NUMERIC(18,4) NOT NULL DEFAULT 0,
    investment_component NUMERIC(18,4) NOT NULL DEFAULT 0
);

CREATE MATERIALIZED VIEW current_holdings AS
SELECT
    t.account_id,
    t.security_isin,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
             WHEN t.type = 'sell' THEN -t.quantity
             ELSE 0 END) AS quantity,
    SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.amount + t.fee
             WHEN t.type = 'sell' THEN -(t.amount - t.fee)
             ELSE 0 END) /
    NULLIF(SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
                     WHEN t.type = 'sell' THEN -t.quantity
                     ELSE 0 END), 0) AS avg_cost_basis,
    SUM(CASE WHEN t.type = 'dividend' THEN t.amount ELSE 0 END) AS total_dividends
FROM transactions t
WHERE t.security_isin IS NOT NULL
GROUP BY t.account_id, t.security_isin
HAVING SUM(CASE WHEN t.type IN ('buy', 'savings_plan') THEN t.quantity
                 WHEN t.type = 'sell' THEN -t.quantity
                 ELSE 0 END) > 0;

CREATE UNIQUE INDEX ON current_holdings (account_id, security_isin);

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS current_holdings;
DROP TABLE IF EXISTS net_worth_snapshots;
DROP TABLE IF EXISTS exchange_rates;
DROP TABLE IF EXISTS etf_holdings;
DROP TABLE IF EXISTS market_data;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS securities;
DROP TABLE IF EXISTS accounts;
