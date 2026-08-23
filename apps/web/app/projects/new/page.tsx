"use client";

import { type FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ErrorNotice } from "@/components/ui";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/format";

function slugify(value: string): string {
  return value.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 63);
}

export default function NewProjectPage() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [repositoryURL, setRepositoryURL] = useState("");
  const [branch, setBranch] = useState("main");
  const [port, setPort] = useState(8080);
  const [healthPath, setHealthPath] = useState("/health");
	const [autoDeploy, setAutoDeploy] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError(undefined);
    try {
		const project = await api.createProject({ name, slug, repository_url: repositoryURL, repository_branch: branch, auto_deploy: autoDeploy });
      try {
        await api.createService(project.id, { name: "web", internal_port: port, desired_replicas: 1, health_check_path: healthPath });
      } catch (serviceError) {
		try {
			await api.deleteProject(project.id);
		} catch (cleanupError) {
			throw new Error(`${errorMessage(serviceError)} Project cleanup also failed: ${errorMessage(cleanupError)}`);
		}
        throw serviceError;
      }
      router.push(`/projects/${project.id}`);
    } catch (caught) {
      setError(errorMessage(caught));
      setSubmitting(false);
    }
  }

  return (
    <div className="form-page">
      <Link href="/" className="back-link">← Projects</Link>
      <div className="page-heading compact"><div><p className="eyebrow">NEW APPLICATION</p><h1>Create project</h1><p>Forge will build the repository&apos;s root Dockerfile.</p></div></div>
      {error && <ErrorNotice message={error} />}
      <form className="form-card" onSubmit={submit}>
        <div className="field-grid">
          <label><span>Project name</span><input required maxLength={100} value={name} onChange={(event) => { const next = event.target.value; setName(next); if (!slug || slug === slugify(name)) setSlug(slugify(next)); }} placeholder="Demo API" /></label>
          <label><span>Slug</span><input required pattern="[a-z0-9][a-z0-9-]*" maxLength={63} value={slug} onChange={(event) => setSlug(slugify(event.target.value))} placeholder="demo-api" /></label>
        </div>
        <label><span>GitHub repository</span><input required type="url" value={repositoryURL} onChange={(event) => setRepositoryURL(event.target.value)} placeholder="https://github.com/your-name/demo-api" /><small>Public HTTPS repositories are supported in the local MVP.</small></label>
        <div className="field-grid">
          <label><span>Branch</span><input required value={branch} onChange={(event) => setBranch(event.target.value)} /></label>
          <label><span>Application port</span><input required type="number" min={1} max={65535} value={port} onChange={(event) => setPort(Number(event.target.value))} /></label>
        </div>
        <label><span>Health check path</span><input required value={healthPath} onChange={(event) => setHealthPath(event.target.value)} placeholder="/health" /></label>
		<label className="checkbox-field"><input type="checkbox" checked={autoDeploy} onChange={(event) => setAutoDeploy(event.target.checked)} /><span>Deploy pushes to this branch automatically</span></label>
        <div className="form-actions"><Link className="button button-secondary" href="/">Cancel</Link><button className="button button-primary" disabled={submitting}>{submitting ? "Creating…" : "Create project"}</button></div>
      </form>
    </div>
  );
}
