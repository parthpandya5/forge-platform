import type { DeploymentStatus } from "@/lib/types";

const labels: Record<DeploymentStatus, string> = {
  QUEUED: "Queued", CLONING: "Cloning", BUILDING: "Building", PUSHING: "Pushing",
  DEPLOYING: "Deploying", VERIFYING: "Verifying", LIVE: "Live", FAILED: "Failed", CANCELLED: "Cancelled",
};

export function StatusBadge({ status }: { status: DeploymentStatus }) {
  const active = ["QUEUED", "CLONING", "BUILDING", "PUSHING", "DEPLOYING", "VERIFYING"].includes(status);
  return <span className={`status status-${status.toLowerCase()}`}><span className={active ? "status-dot status-pulse" : "status-dot"} aria-hidden="true" />{labels[status]}</span>;
}
