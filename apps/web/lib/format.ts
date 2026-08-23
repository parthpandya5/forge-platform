export function relativeTime(value: string): string {
  const seconds = Math.round((Date.now() - new Date(value).getTime()) / 1000);
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

export function duration(start: string | null, end: string | null): string {
  if (!start) return "—";
  const milliseconds = new Date(end ?? Date.now()).getTime() - new Date(start).getTime();
  if (milliseconds < 1000) return `${milliseconds}ms`;
  const seconds = Math.round(milliseconds / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

export function shortSHA(sha: string | null) { return sha?.slice(0, 8) ?? "pending"; }
export function repositoryName(url: string) { return url.replace(/\.git$/, "").split("/").slice(-2).join("/"); }
export function errorMessage(error: unknown) { return error instanceof Error ? error.message : "An unexpected error occurred"; }
