CREATE TABLE rate_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identifier TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT 'analysis',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rate_events_identifier ON rate_events(identifier, created_at);
