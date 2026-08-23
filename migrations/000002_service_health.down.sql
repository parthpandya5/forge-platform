DROP INDEX IF EXISTS services_health_status_idx;
ALTER TABLE services
    DROP COLUMN IF EXISTS health_checked_at,
    DROP COLUMN IF EXISTS health_message,
    DROP COLUMN IF EXISTS health_status;
