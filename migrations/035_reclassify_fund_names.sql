-- +goose Up

-- Re-run the 2026-11 asset-class name rules (internal/parser/classify.go) on
-- securities imported under the old ones. Only the three cases the rules
-- changed are touched, so nothing classified another way is overridden.
-- The class drives Teilfreistellung (30 % for equity ETFs, 0 % for stocks),
-- the loss pots and §23 treatment of commodities.

-- 1. Broker-abbreviated bond funds stored as stocks
--    ("XTR2-EUROZ.GOV.BD 1C").
UPDATE securities SET asset_class = 'bond'
WHERE asset_class = 'stock'
  AND name ~* '(^|[^[:alnum:]])(ishs(i{1,3}|iv|v|vi{1,2})?|xtr2?|vang|lyx|etfs?)([^[:alnum:]]|$)'
  AND name ~* '(^|[^[:alnum:]])(bd|bds|govies)([^[:alnum:]]|$)';

-- 2. Broker-abbreviated ETFs stored as stocks
--    ("ISHSIII-MSCI EM USD(ACC)", "XTR.(IE)-MSCI WORLD 1C",
--    "VANG.FTSE ALL-WLD", "AIS-AM.MSCI WORLD").
UPDATE securities SET asset_class = 'etf'
WHERE asset_class = 'stock'
  AND (name ~* '(^|[^[:alnum:]])(ishs(i{1,3}|iv|v|vi{1,2})?|xtr2?|vang|lyx|etfs?)([^[:alnum:]]|$)'
       OR name ~* '(^|[^[:alnum:]])ais[^[:alnum:]]+am');

-- 3. Stocks whose name merely contains "etf" or "gold" inside a word
--    ("NETFLIX INC." became an ETF, "Goldman Sachs" a commodity).
UPDATE securities SET asset_class = 'stock'
WHERE asset_class = 'etf'
  AND name ~* 'etf'
  AND name !~* '(^|[^[:alnum:]])(etfs?|ishs(i{1,3}|iv|v|vi{1,2})?|xtr2?|vang|lyx)([^[:alnum:]]|$)'
  AND name !~* '(^|[^[:alnum:]])ais[^[:alnum:]]+am'
  AND name !~* '(ucits|ishares|vanguard|xtrackers|amundi|lyxor|spdr|invesco)';

UPDATE securities SET asset_class = 'stock'
WHERE asset_class = 'commodity'
  AND name ~* 'gold'
  AND name !~* '(^|[^[:alnum:]])(gold|silver)([^[:alnum:]]|$)'
  AND name !~* '(commodity|rohstoff|euwax)';

-- +goose Down
-- Not reversible: the previous classes came from a heuristic that was wrong
-- for exactly these rows.
SELECT 1;
