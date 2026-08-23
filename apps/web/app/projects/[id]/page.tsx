"use client";

import { type FormEvent, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { StatusBadge } from "@/components/status-badge";
import { EmptyState, ErrorNotice, LoadingRows } from "@/components/ui";
import { api } from "@/lib/api";
import { errorMessage, relativeTime, repositoryName, shortSHA } from "@/lib/format";
import type { Deployment, EnvironmentVariable, Project, Service } from "@/lib/types";

export default function ProjectPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [project, setProject] = useState<Project>();
  const [service, setService] = useState<Service>();
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [variables, setVariables] = useState<EnvironmentVariable[]>([]);
  const [loading, setLoading] = useState(true);
  const [deploying, setDeploying] = useState(false);
  const [error, setError] = useState<string>();
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [savingVariable, setSavingVariable] = useState(false);
  const [savingService, setSavingService] = useState(false);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const [nextProject, services] = await Promise.all([api.getProject(id), api.listServices(id)]);
        const primary = services[0];
        let history: Deployment[] = [];
        let environment: EnvironmentVariable[] = [];
        if (primary) {
          [history, environment] = await Promise.all([api.listDeployments(primary.id), api.listEnvironment(primary.id)]);
        }
        if (active) {
          setProject(nextProject); setService(primary); setDeployments(history); setVariables(environment); setError(undefined);
        }
      } catch (caught) { if (active) setError(errorMessage(caught)); }
      finally { if (active) setLoading(false); }
    })();
    return () => { active = false; };
  }, [id]);

  async function deploy() {
    if (!service) return;
    setDeploying(true);
    try { router.push(`/deployments/${(await api.createDeployment(service.id)).id}`); }
    catch (caught) { setError(errorMessage(caught)); setDeploying(false); }
  }

  async function saveVariable(event: FormEvent) {
    event.preventDefault();
    if (!service) return;
    setSavingVariable(true);
    try {
      await api.setEnvironment(service.id, key, value);
      setKey(""); setValue(""); setVariables(await api.listEnvironment(service.id));
    } catch (caught) { setError(errorMessage(caught)); }
    finally { setSavingVariable(false); }
  }

  async function removeVariable(variableID: string) {
    if (!service) return;
    try { await api.deleteEnvironment(service.id, variableID); setVariables((current) => current.filter((item) => item.id !== variableID)); }
    catch (caught) { setError(errorMessage(caught)); }
  }

  async function saveService(event: FormEvent) {
    event.preventDefault();
    if (!service) return;
    setSavingService(true);
    try {
      const updated = await api.updateService(service.id, service);
      setService(updated);
      setError(undefined);
    } catch (caught) { setError(errorMessage(caught)); }
    finally { setSavingService(false); }
  }

  function updateServiceField<K extends keyof Service>(field: K, value: Service[K]) {
    setService((current) => current ? { ...current, [field]: value } : current);
  }

  if (loading) return <LoadingRows count={5} />;
  if (!project) return <ErrorNotice message={error ?? "Project was not found"} />;

  return (
    <>
      <Link href="/" className="back-link">← Projects</Link>
      <div className="page-heading project-heading">
        <div><div className="title-line"><span className="resource-icon large">{project.name[0]}</span><h1>{project.name}</h1></div><p>{repositoryName(project.repository_url)} · {project.repository_branch}</p></div>
        <button className="button button-primary" onClick={() => void deploy()} disabled={!service || deploying}>{deploying ? "Queueing…" : "Deploy latest"}</button>
      </div>
      {error && <ErrorNotice message={error} />}
      {service && <div className="stat-grid">
        <div className="stat"><span>Service health</span><strong className={`health health-${service.health_status.toLowerCase()}`}>{service.health_status}</strong><small>{service.health_message ?? `${service.service_type} · port ${service.internal_port}`}</small></div>
        <div className="stat"><span>Replicas</span><strong>{service.desired_replicas}</strong><small>{service.cpu_request} CPU · {service.memory_request} memory</small></div>
        <div className="stat"><span>Health check</span><strong className="mono">{service.health_check_path}</strong><small>Kubernetes HTTP probe</small></div>
      </div>}
      <section className="section">
        <div className="section-heading"><div><h2>Deployments</h2><p>Immutable build and rollout history.</p></div></div>
        {deployments.length === 0 ? <EmptyState title="No deployments">Queue the first build from this project.</EmptyState> : (
          <div className="table-wrap"><table><thead><tr><th>Status</th><th>Commit</th><th>Created</th><th>Artifact</th><th /></tr></thead><tbody>
            {deployments.map((deployment) => <tr key={deployment.id}><td><StatusBadge status={deployment.status} /></td><td className="mono">{shortSHA(deployment.commit_sha)}</td><td>{relativeTime(deployment.created_at)}</td><td className="mono subdued">{deployment.image_tag?.split(":").at(-1)?.slice(0, 12) ?? "—"}</td><td><Link className="row-link" href={`/deployments/${deployment.id}`}>View ›</Link></td></tr>)}
          </tbody></table></div>
        )}
      </section>
      {service && <section className="section">
        <div className="section-heading"><div><h2>Service configuration</h2><p>Changes apply on the next deployment reconciliation.</p></div></div>
        <form className="form-card service-form" onSubmit={saveService}>
          <div className="field-grid"><label><span>Service name</span><input required value={service.name} onChange={(event) => updateServiceField("name", event.target.value)} /></label><label><span>Container port</span><input required type="number" min={1} max={65535} value={service.internal_port} onChange={(event) => updateServiceField("internal_port", Number(event.target.value))} /></label></div>
          <div className="field-grid"><label><span>Desired replicas</span><input required type="number" min={1} max={20} value={service.desired_replicas} onChange={(event) => updateServiceField("desired_replicas", Number(event.target.value))} /></label><label><span>Health check path</span><input required value={service.health_check_path} onChange={(event) => updateServiceField("health_check_path", event.target.value)} /></label></div>
          <div className="resource-fields"><label><span>CPU request</span><input required value={service.cpu_request} onChange={(event) => updateServiceField("cpu_request", event.target.value)} /></label><label><span>CPU limit</span><input required value={service.cpu_limit} onChange={(event) => updateServiceField("cpu_limit", event.target.value)} /></label><label><span>Memory request</span><input required value={service.memory_request} onChange={(event) => updateServiceField("memory_request", event.target.value)} /></label><label><span>Memory limit</span><input required value={service.memory_limit} onChange={(event) => updateServiceField("memory_limit", event.target.value)} /></label></div>
          <div className="form-actions"><button className="button button-secondary" disabled={savingService}>{savingService ? "Saving…" : "Save configuration"}</button></div>
        </form>
      </section>}
      <section className="section">
        <div className="section-heading"><div><h2>Environment variables</h2><p>Values are encrypted at rest and never returned by the API.</p></div></div>
        <div className="environment-card">
          {variables.length === 0 ? <p className="muted">No environment variables configured.</p> : variables.map((variable) => (
            <div className="environment-row" key={variable.id}><code>{variable.key}</code><span className="secret-mask">••••••••••••</span><button className="text-button danger" onClick={() => void removeVariable(variable.id)}>Delete</button></div>
          ))}
          <form className="environment-form" onSubmit={saveVariable}><input required pattern="[A-Za-z_][A-Za-z0-9_]*" placeholder="VARIABLE_NAME" value={key} onChange={(event) => setKey(event.target.value)} /><input required type="password" placeholder="Value" value={value} onChange={(event) => setValue(event.target.value)} /><button className="button button-secondary" disabled={savingVariable}>{savingVariable ? "Saving…" : "Add variable"}</button></form>
        </div>
      </section>
    </>
  );
}
