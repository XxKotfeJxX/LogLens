import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { SourceLibrary } from "./pages/SourceLibrary";
import { ImportProgress } from "./pages/ImportProgress";
import { Parsers } from "./pages/Parsers";
import { ParserStudio } from "./pages/ParserStudio";
import { SourceExtraction } from "./pages/SourceExtraction";

const Explorer = lazy(() =>
  import("./pages/Explorer").then((module) => ({ default: module.Explorer })),
);
const Analyze = lazy(() =>
  import("./pages/Analyze").then((module) => ({ default: module.Analyze })),
);
const SourceGroups = lazy(() =>
  import("./pages/SourceGroups").then((module) => ({
    default: module.SourceGroups,
  })),
);
const SourceGroupDetail = lazy(() =>
  import("./pages/SourceGroups").then((module) => ({
    default: module.SourceGroupDetail,
  })),
);

export function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<SourceLibrary />} />
        <Route path="/parsers" element={<Parsers />} />
        <Route path="/parsers/:profileId" element={<ParserStudio />} />
        <Route path="/import/:jobId" element={<ImportProgress />} />
        <Route
          path="/groups"
          element={
            <Suspense
              fallback={
                <div className="page loading-page">Opening source groups…</div>
              }
            >
              <SourceGroups />
            </Suspense>
          }
        />
        <Route
          path="/groups/:groupId"
          element={
            <Suspense
              fallback={
                <div className="page loading-page">
                  Opening merged evidence…
                </div>
              }
            >
              <SourceGroupDetail />
            </Suspense>
          }
        />
        <Route
          path="/sources/:sourceId/extraction"
          element={<SourceExtraction />}
        />
        <Route
          path="/sources/:sourceId/analyze"
          element={
            <Suspense
              fallback={
                <div className="page loading-page">
                  Opening analysis workspace…
                </div>
              }
            >
              <Analyze />
            </Suspense>
          }
        />
        <Route
          path="/sources/:sourceId"
          element={
            <Suspense
              fallback={
                <div className="page loading-page">Opening indexed source…</div>
              }
            >
              <Explorer />
            </Suspense>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppShell>
  );
}
