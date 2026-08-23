import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ForgeAPIError } from "./api";
import type { Service } from "./types";

afterEach(() => {
	vi.unstubAllGlobals();
});

describe("Forge API client", () => {
	it("sends only writable service fields", async () => {
		const service: Service = {
			id: "service-id",
			project_id: "project-id",
			name: "web",
			service_type: "web",
			internal_port: 8080,
			desired_replicas: 2,
			health_check_path: "/health",
			cpu_request: "100m",
			cpu_limit: "500m",
			memory_request: "128Mi",
			memory_limit: "512Mi",
			health_status: "RUNNING",
			health_message: "2/2 replicas available",
			health_checked_at: "2026-08-21T00:00:00Z",
			created_at: "2026-08-21T00:00:00Z",
			updated_at: "2026-08-21T00:00:00Z",
		};
		const fetchMock = vi.fn().mockResolvedValue({
			ok: true,
			status: 200,
			json: async () => ({ data: service }),
		});
		vi.stubGlobal("fetch", fetchMock);

		await api.updateService(service.id, service);

		const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
		const body = JSON.parse(String(init.body)) as Record<string, unknown>;
		expect(Object.keys(body).sort()).toEqual([
			"cpu_limit", "cpu_request", "desired_replicas", "health_check_path",
			"internal_port", "memory_limit", "memory_request", "name",
		].sort());
		expect((init.headers as Headers).get("Content-Type")).toBe("application/json");
	});

	it("does not force a JSON content type on GET requests", async () => {
		const fetchMock = vi.fn().mockResolvedValue({
			ok: true,
			status: 200,
			json: async () => ({ data: [] }),
		});
		vi.stubGlobal("fetch", fetchMock);

		await api.listProjects();

		const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
		expect((init.headers as Headers).has("Content-Type")).toBe(false);
	});

	it("preserves the backend error code and status", async () => {
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
			ok: false,
			status: 409,
			json: async () => ({ error: { code: "CONFLICT", message: "already exists" } }),
		}));

		await expect(api.listProjects()).rejects.toEqual(
			expect.objectContaining<Partial<ForgeAPIError>>({ code: "CONFLICT", message: "already exists", status: 409 }),
		);
	});
});
