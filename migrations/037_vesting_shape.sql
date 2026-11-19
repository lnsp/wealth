-- +goose Up
-- Share of an RSU refresh that vests in each year of its schedule; NULL vests evenly.
ALTER TABLE income_streams ADD COLUMN vest_weights DOUBLE PRECISION[];

-- +goose Down
ALTER TABLE income_streams DROP COLUMN IF EXISTS vest_weights;
