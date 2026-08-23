import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "Forge",
  description: "Self-hosted application delivery control plane",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>
        <aside className="sidebar">
          <Link className="brand" href="/" aria-label="Forge projects">
            <span className="brand-mark">F</span>
            <span>Forge</span>
          </Link>
          <nav className="nav" aria-label="Primary navigation">
            <Link href="/" className="nav-link"><span>▦</span> Projects</Link>
            <Link href="/projects/new" className="nav-link"><span>＋</span> New project</Link>
          </nav>
          <div className="sidebar-foot">
            <span className="health-dot" aria-hidden="true" />
            Local control plane
          </div>
        </aside>
        <div className="app-frame">
          <header className="topbar">
            <span className="topbar-label">Application delivery</span>
            <span className="avatar" title="Local Developer">LD</span>
          </header>
          <main className="page-shell">{children}</main>
        </div>
      </body>
    </html>
  );
}
