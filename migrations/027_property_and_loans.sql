-- +goose Up

-- Generalise physical assets beyond cars: apartments, houses, land and other
-- valuables. Property is valued as land (follows prices) plus building (follows
-- prices and ages in a straight line over its useful life, ImmoWertV §38); see
-- internal/analytics/property.go. price_drift = 0 means "appraisals only".
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check
    CHECK (kind IN ('car', 'apartment', 'house', 'land', 'other'));

ALTER TABLE assets
    ADD COLUMN purchase_costs NUMERIC(18,4) NOT NULL DEFAULT 0 CHECK (purchase_costs >= 0),
    ADD COLUMN notarized_date DATE,
    ADD COLUMN land_value NUMERIC(18,4) CHECK (land_value >= 0),
    ADD COLUMN year_built INTEGER CHECK (year_built BETWEEN 1000 AND 2200),
    ADD COLUMN useful_life_years INTEGER NOT NULL DEFAULT 80 CHECK (useful_life_years > 0),
    ADD COLUMN price_drift NUMERIC(6,4) NOT NULL DEFAULT 0 CHECK (price_drift > -0.5 AND price_drift < 0.5),
    ADD COLUMN self_used BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN living_area_m2 NUMERIC(8,2) CHECK (living_area_m2 > 0),
    ADD COLUMN details TEXT NOT NULL DEFAULT '';

-- An asset can be paid through several transactions (own funds to the notary,
-- transfer tax, broker, renovations). Linked rows are tagged 'asset_purchase'
-- and treated as an asset swap rather than spending.
CREATE TABLE asset_transactions (
    transaction_id UUID PRIMARY KEY REFERENCES transactions(id) ON DELETE CASCADE,
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL DEFAULT 'purchase' CHECK (purpose IN ('purchase', 'improvement'))
);
CREATE INDEX idx_asset_transactions_asset ON asset_transactions(asset_id);

INSERT INTO asset_transactions (transaction_id, asset_id, purpose)
SELECT purchase_transaction_id, id, 'purchase' FROM assets WHERE purchase_transaction_id IS NOT NULL;
ALTER TABLE assets DROP COLUMN purchase_transaction_id;

-- Capital spent on an asset after purchase (e.g. a new bathroom). Raises the
-- cost basis and the building value, which then ages with the building.
CREATE TABLE asset_improvements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    amount NUMERIC(18,4) NOT NULL CHECK (amount > 0),
    description TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_asset_improvements_asset ON asset_improvements(asset_id);

-- Annuity loans that finance an asset. The balance is derived from the terms
-- (internal/analytics/loan.go), so net worth never depends on bank statements.
CREATE TABLE loans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    lender TEXT NOT NULL DEFAULT '',
    principal NUMERIC(18,4) NOT NULL CHECK (principal > 0),
    payout_date DATE NOT NULL,
    first_payment_date DATE NOT NULL,
    nominal_rate NUMERIC(8,6) NOT NULL CHECK (nominal_rate >= 0 AND nominal_rate < 1),
    monthly_payment NUMERIC(18,4) NOT NULL CHECK (monthly_payment > 0),
    fixed_until DATE,
    followup_rate NUMERIC(8,6) CHECK (followup_rate >= 0 AND followup_rate < 1),
    extra_allowance_pct NUMERIC(6,4) NOT NULL DEFAULT 0 CHECK (extra_allowance_pct >= 0 AND extra_allowance_pct <= 1),
    payment_account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (first_payment_date > payout_date)
);
CREATE INDEX idx_loans_asset ON loans(asset_id);

-- Sondertilgungen. A linked transaction is the cash that paid it.
CREATE TABLE loan_extra_repayments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    amount NUMERIC(18,4) NOT NULL CHECK (amount > 0),
    transaction_id UUID UNIQUE REFERENCES transactions(id) ON DELETE SET NULL
);
CREATE INDEX idx_loan_extra_loan ON loan_extra_repayments(loan_id);

-- Regular payments matched to bank transactions (category 'loan_payment').
-- Cashflow splits them into interest (cost) and principal (saving).
CREATE TABLE loan_payment_links (
    transaction_id UUID PRIMARY KEY REFERENCES transactions(id) ON DELETE CASCADE,
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE CASCADE,
    due_date DATE NOT NULL,
    UNIQUE (loan_id, due_date)
);

ALTER TABLE net_worth_snapshots ADD COLUMN debt_component NUMERIC(18,4) NOT NULL DEFAULT 0;
ALTER TABLE net_worth_intraday ADD COLUMN debt_component NUMERIC(18,4) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE net_worth_intraday DROP COLUMN IF EXISTS debt_component;
ALTER TABLE net_worth_snapshots DROP COLUMN IF EXISTS debt_component;
DROP TABLE IF EXISTS loan_payment_links;
DROP TABLE IF EXISTS loan_extra_repayments;
DROP TABLE IF EXISTS loans;
DROP TABLE IF EXISTS asset_improvements;
ALTER TABLE assets ADD COLUMN purchase_transaction_id UUID REFERENCES transactions(id) ON DELETE SET NULL;
UPDATE assets a SET purchase_transaction_id = (
    SELECT t.transaction_id FROM asset_transactions t
    WHERE t.asset_id = a.id AND t.purpose = 'purchase' LIMIT 1);
DROP TABLE IF EXISTS asset_transactions;
ALTER TABLE assets
    DROP COLUMN IF EXISTS details,
    DROP COLUMN IF EXISTS living_area_m2,
    DROP COLUMN IF EXISTS self_used,
    DROP COLUMN IF EXISTS price_drift,
    DROP COLUMN IF EXISTS useful_life_years,
    DROP COLUMN IF EXISTS year_built,
    DROP COLUMN IF EXISTS land_value,
    DROP COLUMN IF EXISTS notarized_date,
    DROP COLUMN IF EXISTS purchase_costs;
DELETE FROM assets WHERE kind <> 'car';
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check CHECK (kind IN ('car'));
