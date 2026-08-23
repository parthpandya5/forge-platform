"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { StatusBadge } from "@/components/status-badge";
import { EmptyState, ErrorNotice, LoadingRows } from "@/components/ui";
import { api } from "@/lib/api";
import { errorMessage, relativeTime, repositoryName } from "@/lib/format";
import type { Project } from "@/lib/types";

export default function ProjectsPage() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    void api.listProjects().then((data) => {
      if (active) { setProjects(data); setError(undefined); }
    }).catch((caught: unknown) => {
      if (active) setError(errorMessage(caught));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  return (
    <>
      <div className="page-heading">
        <div><p className="eyebrow">WORKSPACE</p><h1>Projects</h1><p>Applications managed by this Forge control plane.</p></div>
        <Link className="button button-primary" href="/projects/new">New project</Link>
      </div>
      {error && <ErrorNotice message={error} />}
      {loading ? <LoadingRows /> : projects.length === 0 ? (
        <EmptyState title="No projects yet" action={<Link className="button button-primary" href="/projects/new">Create your first project</Link>}>
          Connect a public GitHub repository with a Dockerfile to begin.
        </EmptyState>
      ) : (
        <div className="resource-list">
          {projects.map((project) => (
            <Link href={`/projects/${project.id}`} className="resource-row" key={project.id}>
              <span className="resource-icon">{project.name.slice(0, 1).toUpperCase()}</span>
              <span className="resource-main"><strong>{project.name}</strong><small>{repositoryName(project.repository_url)} · {project.repository_branch}</small></span>
              <span className="resource-meta">
                {project.current_deployment ? <StatusBadge status={project.current_deployment.status} /> : <span className="muted">Not deployed</span>}
                <small>{relativeTime(project.updated_at)}</small>
              </span>
              <span className="chevron">›</span>
            </Link>
          ))}
        </div>
      )}
    </>
  );
}
