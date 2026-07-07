-- +goose Up

-- Extend institution enum to include Morgan Stanley At Work (Google RSUs).
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other', 'morgan_stanley'));

-- Per-account mapping for plan-share imports (e.g. "GSU Class C" -> chosen ISIN).
ALTER TABLE accounts ADD COLUMN import_security_isin TEXT;

-- RSU vesting events. Vested rows are persisted alongside the buy transaction so the
-- gross/net split survives in the database; unvested rows are written by the
-- "Unvested GSUs As At Date" CSV and have NULL net_quantity until they vest.
CREATE TABLE rsu_vests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    security_isin TEXT REFERENCES securities(isin),
    vest_date DATE NOT NULL,
    grant_number TEXT,
    gross_quantity NUMERIC(18,8) NOT NULL,
    net_quantity NUMERIC(18,8),
    price NUMERIC(18,8),
    currency TEXT NOT NULL DEFAULT 'USD',
    vested BOOLEAN NOT NULL,
    transaction_id UUID REFERENCES transactions(id),
    import_hash TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rsu_vests_account_id ON rsu_vests(account_id);
CREATE INDEX idx_rsu_vests_vested ON rsu_vests(account_id, vested);
CREATE INDEX idx_rsu_vests_vest_date ON rsu_vests(vest_date);

-- +goose Down
DROP TABLE IF EXISTS rsu_vests;
ALTER TABLE accounts DROP COLUMN IF EXISTS import_security_isin;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other'));
