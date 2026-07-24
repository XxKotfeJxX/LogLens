import type { ReactNode } from "react";
import { Activity, Github, ScanLine } from "lucide-react";
import { Link } from "react-router-dom";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen bg-canvas text-ink">
      <header className="app-header">
        <Link to="/" className="brand" aria-label="LogLens home">
          <span className="brand-mark">
            <ScanLine size={19} strokeWidth={1.8} />
          </span>
          <span>LogLens</span>
        </Link>
        <div className="header-meta">
          <span className="system-ready">
            <Activity size={13} /> local index
          </span>
          <a
            className="icon-link"
            href="https://github.com/"
            aria-label="GitHub repository"
            target="_blank"
            rel="noreferrer"
          >
            <Github size={18} />
          </a>
        </div>
      </header>
      <main>{children}</main>
    </div>
  );
}
