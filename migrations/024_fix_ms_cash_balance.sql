-- +goose Up
-- Repair Morgan Stanley transactions so the MS account is cash-neutral.
-- See internal/parser/morganstanley.go for the model: vests are in-kind transfers,
-- share sales emit sell + matching withdrawal (auto-wire), explicit cash rows are dropped.

-- Vests were imported as 'buy', which subtracted their FMV from cash. They should be transfers.
UPDATE transactions
   SET type = 'transfer'
 WHERE type = 'buy'
   AND reference LIKE 'ms-release-%';

-- Mirror 'deposit' rows added per sale (suffix "-cash") double-counted the cash leg.
-- Flip them to 'withdrawal' so each sell now nets to zero (auto-wired proceeds).
UPDATE transactions
   SET type = 'withdrawal',
       counterparty = replace(counterparty, 'Sale proceeds', 'Morgan Stanley (auto-wire of') || ' proceeds)',
       reference = replace(reference, '-cash', '-wire')
 WHERE type = 'deposit'
   AND reference LIKE '%-cash'
   AND counterparty LIKE 'Sale proceeds%';

-- The CSV's explicit cash wire rows are now redundant under the auto-wire model.
DELETE FROM transactions
 WHERE type = 'withdrawal'
   AND counterparty = 'Morgan Stanley (cash withdrawal)'
   AND account_id IN (SELECT id FROM accounts WHERE institution = 'morgan_stanley');

REFRESH MATERIALIZED VIEW current_holdings;

-- +goose Down
-- Best-effort reverse (cannot reconstruct deleted cash wire rows).
UPDATE transactions
   SET type = 'deposit',
       counterparty = 'Sale proceeds (' || trim(both ' )' from replace(replace(counterparty, 'Morgan Stanley (auto-wire of ', ''), 'proceeds)', '')) || ')',
       reference = replace(reference, '-wire', '-cash')
 WHERE type = 'withdrawal'
   AND reference LIKE '%-wire'
   AND counterparty LIKE 'Morgan Stanley (auto-wire of%';

UPDATE transactions
   SET type = 'buy'
 WHERE type = 'transfer'
   AND reference LIKE 'ms-release-%';

REFRESH MATERIALIZED VIEW current_holdings;
