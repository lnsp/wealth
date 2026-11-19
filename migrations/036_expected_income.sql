-- +goose Up
CREATE TABLE planning_profile (
    id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    birth_year INT CHECK (birth_year BETWEEN 1900 AND 2100),
    retirement_age INT NOT NULL DEFAULT 67 CHECK (retirement_age BETWEEN 40 AND 80),
    children INT NOT NULL DEFAULT 0 CHECK (children >= 0),
    church_rate NUMERIC(4,3) NOT NULL DEFAULT 0 CHECK (church_rate IN (0, 0.08, 0.09)),
    health_kind TEXT NOT NULL DEFAULT 'gkv' CHECK (health_kind IN ('gkv', 'pkv')),
    gkv_zusatzbeitrag NUMERIC(5,4) NOT NULL DEFAULT 0.029 CHECK (gkv_zusatzbeitrag BETWEEN 0 AND 0.1),
    pkv_premium_monthly NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (pkv_premium_monthly >= 0),
    pkv_basic_share NUMERIC(4,3) NOT NULL DEFAULT 0.85 CHECK (pkv_basic_share BETWEEN 0 AND 1),
    savings_mode TEXT NOT NULL DEFAULT 'costs' CHECK (savings_mode IN ('costs', 'rate')),
    running_costs_monthly NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (running_costs_monthly >= 0),
    savings_rate NUMERIC(4,3) NOT NULL DEFAULT 0.4 CHECK (savings_rate BETWEEN 0 AND 1),
    rsu_count_full BOOLEAN NOT NULL DEFAULT true,
    inflation NUMERIC(5,4) NOT NULL DEFAULT 0.02 CHECK (inflation BETWEEN -0.05 AND 0.2),
    wage_growth NUMERIC(5,4) NOT NULL DEFAULT 0.03 CHECK (wage_growth BETWEEN -0.05 AND 0.2),
    share_drift NUMERIC(5,4) NOT NULL DEFAULT 0 CHECK (share_drift BETWEEN -0.5 AND 0.5),
    usd_per_eur NUMERIC(8,5) CHECK (usd_per_eur > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE income_streams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL CHECK (kind IN ('salary', 'bonus', 'rsu_refresh', 'one_off')),
    name TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'EUR',
    amount NUMERIC(14,4) NOT NULL CHECK (amount >= 0),
    growth NUMERIC(5,4) NOT NULL DEFAULT 0,
    month INT CHECK (month BETWEEN 1 AND 12),
    payout_factor NUMERIC(5,4),
    vest_months INT CHECK (vest_months > 0),
    vest_every INT CHECK (vest_every IN (1, 3, 6, 12)),
    cliff_months INT NOT NULL DEFAULT 0 CHECK (cliff_months >= 0),
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    first_year INT,
    start_date DATE NOT NULL,
    end_date DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (kind <> 'bonus' OR (month IS NOT NULL AND payout_factor IS NOT NULL)),
    CHECK (kind <> 'rsu_refresh' OR (month IS NOT NULL AND vest_months IS NOT NULL AND vest_every IS NOT NULL)),
    CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE TABLE income_stream_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id UUID NOT NULL REFERENCES income_streams(id) ON DELETE CASCADE,
    effective_date DATE NOT NULL,
    amount NUMERIC(14,2) NOT NULL CHECK (amount >= 0),
    UNIQUE (stream_id, effective_date)
);

CREATE TABLE rsu_grants (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    grant_number TEXT NOT NULL,
    first_seen_on DATE NOT NULL,
    last_seen_on DATE NOT NULL,
    first_vest_date DATE NOT NULL,
    last_vest_date DATE NOT NULL,
    PRIMARY KEY (account_id, grant_number)
);

-- Grants already in an import count as seen on their first known vest.
INSERT INTO rsu_grants (account_id, grant_number, first_seen_on, last_seen_on, first_vest_date, last_vest_date)
SELECT account_id, grant_number, LEAST(MIN(vest_date), MIN(created_at)::date), MAX(created_at)::date, MIN(vest_date), MAX(vest_date)
FROM rsu_vests
WHERE vested = false AND grant_number IS NOT NULL AND grant_number <> ''
GROUP BY account_id, grant_number;

-- +goose Down
DROP TABLE IF EXISTS rsu_grants;
DROP TABLE IF EXISTS income_stream_steps;
DROP TABLE IF EXISTS income_streams;
DROP TABLE IF EXISTS planning_profile;
