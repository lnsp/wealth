-- +goose Up
ALTER TABLE accounts
  ADD COLUMN tax_treatment TEXT NOT NULL DEFAULT 'taxable'
    CHECK (tax_treatment IN ('taxable', 'bav', 'riester', 'rurup', 'savings')),
  ADD COLUMN employer_match_pct NUMERIC(6,4);

-- +goose Down
ALTER TABLE accounts
  DROP COLUMN IF EXISTS employer_match_pct,
  DROP COLUMN IF EXISTS tax_treatment;
