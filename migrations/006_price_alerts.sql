-- +goose Up
CREATE TABLE IF NOT EXISTS price_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_type TEXT NOT NULL CHECK (alert_type IN ('price_above', 'price_below', 'daily_change', 'portfolio_milestone')),
    security_isin TEXT REFERENCES securities(isin),
    threshold NUMERIC(14,2) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID REFERENCES price_alerts(id) ON DELETE CASCADE,
    message TEXT NOT NULL,
    value NUMERIC(14,2),
    is_read BOOLEAN NOT NULL DEFAULT false,
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifications_triggered ON notifications(triggered_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS price_alerts;
