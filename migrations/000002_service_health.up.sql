ALTER TABLE services
    ADD COLUMN health_status text NOT NULL DEFAULT 'UNKNOWN'
        CHECK (health_status IN ('UNKNOWN', 'RUNNING', 'DEGRADED', 'UNHEALTHY')),
    ADD COLUMN health_message text,
    ADD COLUMN health_checked_at timestamptz;

CREATE INDEX services_health_status_idx ON services (health_status, health_checked_at);
