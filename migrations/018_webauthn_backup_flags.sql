-- +goose Up
ALTER TABLE webauthn_credentials ADD COLUMN IF NOT EXISTS backup_eligible BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE webauthn_credentials ADD COLUMN IF NOT EXISTS backup_state BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE webauthn_credentials DROP COLUMN IF EXISTS backup_eligible;
ALTER TABLE webauthn_credentials DROP COLUMN IF EXISTS backup_state;
