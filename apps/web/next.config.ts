import type { NextConfig } from "next";

const apiOrigin = (() => {
	try {
		return new URL(
			process.env.NEXT_PUBLIC_FORGE_API_URL ?? "http://localhost:8080",
		).origin;
	} catch {
		return "http://localhost:8080";
	}
})();
const developmentScriptPolicy =
	process.env.NODE_ENV === "development" ? " 'unsafe-eval'" : "";
const developmentConnectPolicy =
	process.env.NODE_ENV === "development" ? " ws://localhost:3000" : "";

const nextConfig: NextConfig = {
	output: "standalone",
	poweredByHeader: false,
	reactStrictMode: true,
	turbopack: { root: process.cwd() },
	async headers() {
		return [{
			source: "/:path*",
			headers: [
				{ key: "Content-Security-Policy", value: `default-src 'self'; script-src 'self' 'unsafe-inline'${developmentScriptPolicy}; style-src 'self' 'unsafe-inline'; connect-src 'self' ${apiOrigin}${developmentConnectPolicy}; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'` },
				{ key: "Referrer-Policy", value: "no-referrer" },
				{ key: "X-Content-Type-Options", value: "nosniff" },
				{ key: "X-Frame-Options", value: "DENY" },
			],
		}];
	},
};

export default nextConfig;
