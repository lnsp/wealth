-- +goose Up
CREATE TABLE IF NOT EXISTS wealth_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_type TEXT NOT NULL CHECK (report_type IN ('monthly', 'annual')),
    period_label TEXT NOT NULL,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    data JSONB NOT NULL DEFAULT '{}',
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_wealth_reports_period ON wealth_reports(period_start DESC);

-- +goose Down
DROP TABLE IF EXISTS wealth_reports;
