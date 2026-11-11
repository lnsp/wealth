-- +goose Up

-- Published house price indices (e.g. Eurostat prc_hpi_q for Germany), one
-- row per quarter, keyed by an internal series id. Property valuation follows
-- the index between appraisals and the asset's price_drift beyond the last
-- published quarter.
CREATE TABLE price_index_points (
    series TEXT NOT NULL,
    period DATE NOT NULL, -- first day of the quarter
    value NUMERIC(12,4) NOT NULL CHECK (value > 0),
    PRIMARY KEY (series, period)
);

-- '' = fixed yearly price change (price_drift) only.
ALTER TABLE assets ADD COLUMN price_index TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE assets DROP COLUMN IF EXISTS price_index;
DROP TABLE IF EXISTS price_index_points;
