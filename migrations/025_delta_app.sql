-- +goose Up

-- Allow Delta as an institution (the cross-asset portfolio tracker app).
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other', 'morgan_stanley', 'revolut', 'delta'));

-- Add 'crypto' to the security asset class enum. Crypto positions are stored
-- in the securities table with a synthetic ISIN prefixed "CRYPTO:" (e.g.
-- CRYPTO:BTC) so the existing holdings / valuation pipeline applies.
ALTER TABLE securities DROP CONSTRAINT IF EXISTS securities_asset_class_check;
ALTER TABLE securities ADD CONSTRAINT securities_asset_class_check
    CHECK (asset_class IN ('etf', 'stock', 'bond', 'fund', 'commodity', 'derivative', 'crypto'));

-- +goose Down
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_institution_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_institution_check
    CHECK (institution IN ('sparkasse', 'n26', 'scalable_capital', 'ing', 'dkb', 'comdirect', 'trade_republic', 'manual', 'other', 'morgan_stanley', 'revolut'));

ALTER TABLE securities DROP CONSTRAINT IF EXISTS securities_asset_class_check;
ALTER TABLE securities ADD CONSTRAINT securities_asset_class_check
    CHECK (asset_class IN ('etf', 'stock', 'bond', 'fund', 'commodity', 'derivative'));
