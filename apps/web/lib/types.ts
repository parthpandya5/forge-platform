export type DeploymentStatus =
  | "QUEUED" | "CLONING" | "BUILDING" | "PUSHING" | "DEPLOYING"
  | "VERIFYING" | "LIVE" | "FAILED" | "CANCELLED";

export interface DeploymentSummary { id: string; status: DeploymentStatus; commit_sha: string | null; created_at: string }

export interface Project {
  id: string; user_id: string; name: string; slug: string; repository_url: string;
  repository_branch: string; auto_deploy: boolean; created_at: string; updated_at: string;
  current_deployment?: DeploymentSummary;
}

export interface Service {
  id: string; project_id: string; name: string; service_type: "web"; internal_port: number;
  desired_replicas: number; health_check_path: string; cpu_request: string; cpu_limit: string;
  memory_request: string; memory_limit: string; created_at: string; updated_at: string;
  health_status: "UNKNOWN" | "RUNNING" | "DEGRADED" | "UNHEALTHY";
  health_message: string | null; health_checked_at: string | null;
}

export interface Deployment {
  id: string; service_id: string; commit_sha: string | null; requested_ref: string | null;
  status: DeploymentStatus; image_tag: string | null; route_url: string | null; rollback_of: string | null;
  request_id: string; error_message: string | null; created_at: string; started_at: string | null;
  finished_at: string | null; updated_at: string; version: number;
}

export interface DeploymentEvent { id: number; deployment_id: string; event_type: string; message: string; metadata: Record<string, unknown>; created_at: string }
export interface DeploymentLog { id: number; deployment_id: string; stream: "system" | "build" | "runtime"; message: string; created_at: string }
export interface EnvironmentVariable { id: string; service_id: string; key: string; created_at: string; updated_at: string }
export interface APIErrorEnvelope { error: { code: string; message: string } }
