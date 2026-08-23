CREATE INDEX projects_auto_deploy_repository_idx
    ON projects (repository_url, repository_branch)
    WHERE auto_deploy = true;

CREATE INDEX jobs_available_claim_idx
    ON jobs (available_at, id)
    WHERE state = 'available';

CREATE INDEX jobs_expired_lease_idx
    ON jobs (lease_expires_at, id)
    WHERE state = 'running';

DROP INDEX jobs_claim_idx;

CREATE INDEX deployments_latest_live_idx
    ON deployments (service_id, finished_at DESC, id DESC)
    WHERE status = 'LIVE';
