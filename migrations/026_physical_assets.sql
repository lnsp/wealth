-- +goose Up

-- Physical (non-financial) assets, starting with cars. Value is not stored but
-- derived from the purchase anchor and observations via an exponential
-- depreciation model (see internal/analytics/depreciation.go):
--   V = V_anchor · (1 − annual_depreciation)^years · (1 − km_depreciation)^(km / 10,000)
-- Defaults were calibrated on 296 used ID.3 Pro S listings (AutoScout24, Nov 2026).
CREATE TABLE assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL DEFAULT 'car' CHECK (kind IN ('car')),
    name TEXT NOT NULL,
    list_price NUMERIC(18,4),
    purchase_price NUMERIC(18,4) NOT NULL CHECK (purchase_price > 0),
    purchase_date DATE NOT NULL,
    first_registration DATE,
    purchase_odometer_km INTEGER NOT NULL DEFAULT 0 CHECK (purchase_odometer_km >= 0),
    annual_depreciation NUMERIC(6,4) NOT NULL DEFAULT 0.078 CHECK (annual_depreciation >= 0 AND annual_depreciation < 1),
    km_depreciation NUMERIC(6,4) NOT NULL DEFAULT 0.030 CHECK (km_depreciation >= 0 AND km_depreciation < 1),
    expected_km_per_year INTEGER NOT NULL DEFAULT 15000 CHECK (expected_km_per_year >= 0),
    -- The cash payment for the asset. Linked rows are tagged with category
    -- 'asset_purchase' so cashflow/attribution treat them as an asset swap.
    purchase_transaction_id UUID REFERENCES transactions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Odometer readings and/or appraisals. An appraisal re-anchors the curve.
CREATE TABLE asset_observations (
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    odometer_km INTEGER CHECK (odometer_km >= 0),
    appraised_value NUMERIC(18,4) CHECK (appraised_value > 0),
    note TEXT,
    PRIMARY KEY (asset_id, date),
    CHECK (odometer_km IS NOT NULL OR appraised_value IS NOT NULL)
);

ALTER TABLE net_worth_snapshots ADD COLUMN physical_component NUMERIC(18,4) NOT NULL DEFAULT 0;
ALTER TABLE net_worth_intraday ADD COLUMN physical_component NUMERIC(18,4) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE net_worth_intraday DROP COLUMN IF EXISTS physical_component;
ALTER TABLE net_worth_snapshots DROP COLUMN IF EXISTS physical_component;
DROP TABLE IF EXISTS asset_observations;
DROP TABLE IF EXISTS assets;
