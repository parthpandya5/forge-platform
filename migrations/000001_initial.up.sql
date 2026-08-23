CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    repository_url text NOT NULL,
    repository_branch text NOT NULL CHECK (char_length(repository_branch) BETWEEN 1 AND 255),
    auto_deploy boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, slug)
);

CREATE INDEX projects_user_created_idx ON projects (user_id, created_at DESC);

CREATE TABLE services (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    service_type text NOT NULL DEFAULT 'web' CHECK (service_type IN ('web')),
    internal_port integer NOT NULL CHECK (internal_port BETWEEN 1 AND 65535),
    desired_replicas integer NOT NULL DEFAULT 1 CHECK (desired_replicas BETWEEN 1 AND 20),
    health_check_path text NOT NULL DEFAULT '/health' CHECK (health_check_path ~ '^/'),
    cpu_request text NOT NULL DEFAULT '100m',
    cpu_limit text NOT NULL DEFAULT '500m',
    memory_request text NOT NULL DEFAULT '128Mi',
    memory_limit text NOT NULL DEFAULT '512Mi',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE INDEX services_project_idx ON services (project_id, created_at);

CREATE TABLE deployments (
    id uuid PRIMARY KEY,
    service_id uuid NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    commit_sha text CHECK (commit_sha IS NULL OR commit_sha ~ '^[0-9a-f]{40}$'),
    requested_ref text,
    status text NOT NULL CHECK (status IN (
        'QUEUED', 'CLONING', 'BUILDING', 'PUSHING', 'DEPLOYING',
        'VERIFYING', 'LIVE', 'FAILED', 'CANCELLED'
    )),
    image_tag text,
    route_url text,
    rollback_of uuid REFERENCES deployments(id) ON DELETE RESTRICT,
    request_id text NOT NULL,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);

CREATE INDEX deployments_service_created_idx ON deployments (service_id, created_at DESC, id DESC);
CREATE INDEX deployments_active_idx ON deployments (status) WHERE status NOT IN ('LIVE', 'FAILED', 'CANCELLED');

CREATE TABLE deployment_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 4000),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX deployment_events_deployment_idx ON deployment_events (deployment_id, id);

CREATE TABLE deployment_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    stream text NOT NULL CHECK (stream IN ('system', 'build', 'runtime')),
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 16384),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX deployment_logs_deployment_idx ON deployment_logs (deployment_id, id);

CREATE TABLE environment_variables (
    id uuid PRIMARY KEY,
    service_id uuid NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    key text NOT NULL CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$'),
    encrypted_value bytea NOT NULL,
    nonce bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (service_id, key)
);

CREATE INDEX environment_variables_service_idx ON environment_variables (service_id, key);

CREATE TABLE jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deployment_id uuid NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    job_type text NOT NULL DEFAULT 'deployment' CHECK (job_type IN ('deployment')),
    state text NOT NULL DEFAULT 'available' CHECK (state IN ('available', 'running', 'completed', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_by text,
    lease_expires_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (state = 'running' AND locked_by IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR state <> 'running'
    )
);

CREATE INDEX jobs_claim_idx ON jobs (available_at, id)
    WHERE state = 'available' OR state = 'running';

CREATE TABLE webhook_deliveries (
    delivery_id text PRIMARY KEY,
    event_type text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    outcome text
);
