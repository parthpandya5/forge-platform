import type { APIErrorEnvelope, Deployment, DeploymentEvent, DeploymentLog, EnvironmentVariable, Project, Service } from "./types";

const API_URL = (process.env.NEXT_PUBLIC_FORGE_API_URL ?? "http://localhost:8080").replace(/\/$/, "");
interface DataEnvelope<T> { data: T }
interface ListEnvelope<T> extends DataEnvelope<T[]> { pagination?: { limit: number; offset: number; count: number } }

export class ForgeAPIError extends Error {
  constructor(public readonly code: string, message: string, public readonly status: number) {
    super(message); this.name = "ForgeAPIError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const headers = new Headers(init?.headers);
	if (init?.body != null && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(`${API_URL}${path}`, {
		...init, headers, cache: "no-store",
  });
  if (!response.ok) {
    let body: APIErrorEnvelope | undefined;
    try { body = (await response.json()) as APIErrorEnvelope; } catch { /* Proxy failures may not be JSON. */ }
    throw new ForgeAPIError(body?.error.code ?? "HTTP_ERROR", body?.error.message ?? `Request failed with status ${response.status}`, response.status);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  async listProjects() { return (await request<ListEnvelope<Project>>("/api/projects?limit=100")).data; },
  async getProject(id: string) { return (await request<DataEnvelope<Project>>(`/api/projects/${id}`)).data; },
  async createProject(input: Pick<Project, "name" | "slug" | "repository_url" | "repository_branch" | "auto_deploy">) { return (await request<DataEnvelope<Project>>("/api/projects", { method: "POST", body: JSON.stringify(input) })).data; },
  async deleteProject(id: string) { await request<void>(`/api/projects/${id}`, { method: "DELETE" }); },
  async listServices(projectID: string) { return (await request<DataEnvelope<Service[]>>(`/api/projects/${projectID}/services`)).data; },
  async createService(projectID: string, input: Pick<Service, "name" | "internal_port" | "desired_replicas" | "health_check_path">) { return (await request<DataEnvelope<Service>>(`/api/projects/${projectID}/services`, { method: "POST", body: JSON.stringify(input) })).data; },
  async updateService(serviceID: string, input: Pick<Service, "name" | "internal_port" | "desired_replicas" | "health_check_path" | "cpu_request" | "cpu_limit" | "memory_request" | "memory_limit">) {
		const payload = {
			name: input.name,
			internal_port: input.internal_port,
			desired_replicas: input.desired_replicas,
			health_check_path: input.health_check_path,
			cpu_request: input.cpu_request,
			cpu_limit: input.cpu_limit,
			memory_request: input.memory_request,
			memory_limit: input.memory_limit,
		};
		return (await request<DataEnvelope<Service>>(`/api/services/${serviceID}`, { method: "PUT", body: JSON.stringify(payload) })).data;
	},
  async listDeployments(serviceID: string) { return (await request<ListEnvelope<Deployment>>(`/api/services/${serviceID}/deployments?limit=100`)).data; },
  async createDeployment(serviceID: string) { return (await request<DataEnvelope<Deployment>>(`/api/services/${serviceID}/deployments`, { method: "POST", body: "{}" })).data; },
  async getDeployment(id: string) { return (await request<DataEnvelope<Deployment>>(`/api/deployments/${id}`)).data; },
  async listEvents(id: string, afterID = 0) { return (await request<DataEnvelope<DeploymentEvent[]>>(`/api/deployments/${id}/events?limit=1000&after_id=${afterID}`)).data; },
  async listLogs(id: string, afterID = 0) { return (await request<DataEnvelope<DeploymentLog[]>>(`/api/deployments/${id}/logs?limit=1000&after_id=${afterID}`)).data; },
  async rollback(id: string) { return (await request<DataEnvelope<Deployment>>(`/api/deployments/${id}/rollback`, { method: "POST", body: "{}" })).data; },
  async listEnvironment(serviceID: string) { return (await request<DataEnvelope<EnvironmentVariable[]>>(`/api/services/${serviceID}/environment`)).data; },
  async setEnvironment(serviceID: string, key: string, value: string) { return (await request<DataEnvelope<EnvironmentVariable>>(`/api/services/${serviceID}/environment/${encodeURIComponent(key)}`, { method: "PUT", body: JSON.stringify({ value }) })).data; },
  async deleteEnvironment(serviceID: string, variableID: string) { await request<void>(`/api/services/${serviceID}/environment/${variableID}`, { method: "DELETE" }); },
};
