"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { StatusBadge } from "@/components/status-badge";
import { ErrorNotice, LoadingRows } from "@/components/ui";
import { api } from "@/lib/api";
import { duration, errorMessage, shortSHA } from "@/lib/format";
import type { Deployment, DeploymentEvent, DeploymentLog } from "@/lib/types";

const terminal = new Set(["LIVE", "FAILED", "CANCELLED"]);

export default function DeploymentPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [deployment, setDeployment] = useState<Deployment>();
  const [events, setEvents] = useState<DeploymentEvent[]>([]);
  const [logs, setLogs] = useState<DeploymentLog[]>([]);
  const [error, setError] = useState<string>();
  const [rollingBack, setRollingBack] = useState(false);
	const eventCursor = useRef(0);
	const logCursor = useRef(0);
	const streamDeploymentID = useRef(id);

  const load = useCallback(async () => {
		if (streamDeploymentID.current !== id) {
			streamDeploymentID.current = id;
			eventCursor.current = 0;
			logCursor.current = 0;
			setEvents([]);
			setLogs([]);
		}
    try {
		const [nextDeployment, nextEvents, nextLogs] = await Promise.all([
			api.getDeployment(id),
			api.listEvents(id, eventCursor.current),
			api.listLogs(id, logCursor.current),
		]);
		if (streamDeploymentID.current !== id) return undefined;
		setDeployment(nextDeployment);
		if (nextEvents.length > 0) {
			eventCursor.current = nextEvents.at(-1)?.id ?? eventCursor.current;
			setEvents((current) => [...current, ...nextEvents]);
		}
		if (nextLogs.length > 0) {
			logCursor.current = nextLogs.at(-1)?.id ?? logCursor.current;
			setLogs((current) => [...current, ...nextLogs]);
		}
		setError(undefined);
		return { deployment: nextDeployment, hasMore: nextEvents.length === 1000 || nextLogs.length === 1000 };
    } catch (caught) { setError(errorMessage(caught)); return undefined; }
  }, [id]);

  useEffect(() => {
		let active = true;
		let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      const current = await load();
		if (active && (!current || current.hasMore || !terminal.has(current.deployment.status))) timer = setTimeout(poll, current?.hasMore ? 0 : 2000);
    };
		timer = setTimeout(poll, 0);
    return () => { active = false; if (timer) clearTimeout(timer); };
  }, [load]);

  async function rollback() {
    if (!deployment) return;
    setRollingBack(true);
    try { router.push(`/deployments/${(await api.rollback(deployment.id)).id}`); }
    catch (caught) { setError(errorMessage(caught)); setRollingBack(false); }
  }

  if (!deployment && !error) return <LoadingRows count={6} />;
  if (!deployment) return <ErrorNotice message={error ?? "Deployment was not found"} />;

  return (
    <>
      <button onClick={() => router.back()} className="back-link button-link">← Project</button>
      <div className="page-heading deployment-heading">
        <div><p className="eyebrow">DEPLOYMENT</p><div className="title-line"><h1 className="mono deployment-title">{deployment.id.slice(0, 12)}</h1><StatusBadge status={deployment.status} /></div><p>Commit <span className="mono">{shortSHA(deployment.commit_sha)}</span></p></div>
        <div className="action-row">{deployment.route_url && <a className="button button-secondary" href={deployment.route_url} target="_blank" rel="noreferrer">Open application ↗</a>}{deployment.status === "LIVE" && <button className="button button-secondary" disabled={rollingBack} onClick={() => void rollback()}>{rollingBack ? "Queueing…" : "Rollback to this"}</button>}</div>
      </div>
      {error && <ErrorNotice message={error} />}
      {deployment.error_message && <div className="alert alert-error"><strong>Deployment failed</strong><span>{deployment.error_message}</span></div>}
      <div className="stat-grid four">
        <div className="stat"><span>Started</span><strong>{deployment.started_at ? new Date(deployment.started_at).toLocaleString() : "Pending"}</strong></div>
        <div className="stat"><span>Duration</span><strong>{duration(deployment.started_at, deployment.finished_at)}</strong></div>
        <div className="stat"><span>State version</span><strong>v{deployment.version}</strong><small>Optimistic transition counter</small></div>
        <div className="stat"><span>Artifact</span><strong className="mono small-value">{deployment.image_tag ?? "Pending"}</strong></div>
      </div>
      <div className="detail-grid">
        <section className="section panel"><div className="section-heading"><div><h2>Timeline</h2><p>Durable state and orchestration events.</p></div></div>
          <ol className="timeline">{events.map((event, index) => <li key={event.id}><span className={`timeline-node ${index === events.length - 1 ? "current" : ""}`} /><div><strong>{event.message}</strong><small>{new Date(event.created_at).toLocaleString()} · {event.event_type}</small></div></li>)}</ol>
        </section>
		<section className="section panel logs-panel"><div className="section-heading"><div><h2>Logs</h2><p>Checkout, build, and initial runtime output.</p></div><span className="live-indicator">{terminal.has(deployment.status) ? "● SNAPSHOT" : "● POLLING"}</span></div>
          <div className="logs" role="log" aria-live="polite">{logs.length === 0 ? <span className="log-empty">Waiting for output…</span> : logs.map((log) => <div key={log.id}><span>{new Date(log.created_at).toLocaleTimeString()}</span><em>{log.stream}</em><code>{log.message}</code></div>)}</div>
        </section>
      </div>
    </>
  );
}
