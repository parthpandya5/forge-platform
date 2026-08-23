DROP INDEX IF EXISTS deployments_latest_live_idx;
DROP INDEX IF EXISTS jobs_expired_lease_idx;
DROP INDEX IF EXISTS jobs_available_claim_idx;
DROP INDEX IF EXISTS projects_auto_deploy_repository_idx;

CREATE INDEX jobs_claim_idx ON jobs (available_at, id)
    WHERE state = 'available' OR state = 'running';
