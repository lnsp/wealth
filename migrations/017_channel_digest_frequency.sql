-- +goose Up
ALTER TABLE notification_channels
  ADD COLUMN digest_frequency TEXT NOT NULL DEFAULT 'monthly'
    CHECK (digest_frequency IN ('weekly', 'monthly', 'quarterly', 'never'));

-- +goose Down
ALTER TABLE notification_channels
  DROP COLUMN IF EXISTS digest_frequency;
