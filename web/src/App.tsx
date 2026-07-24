import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { SourceLibrary } from "./pages/SourceLibrary";
import { ImportProgress } from "./pages/ImportProgress";

const Explorer = lazy(() => import("./pages/Explorer").then((module) => ({ default: module.Explorer })));

export function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<SourceLibrary />} />
        <Route path="/import/:jobId" element={<ImportProgress />} />
        <Route
          path="/sources/:sourceId"
          element={<Suspense fallback={<div className="page loading-page">Opening indexed source…</div>}><Explorer /></Suspense>}
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppShell>
  );
}
