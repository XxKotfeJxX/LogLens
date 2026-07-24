import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  CheckCircle2,
  Database,
  FileSearch,
  LoaderCircle,
  XCircle,
} from "lucide-react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  cancelImport,
  getImport,
  importEventsURL,
  type ImportJob,
} from "../api";
import { formatBytes, formatCount } from "../lib/format";

export function ImportProgress() {
  const { jobId = "" } = useParams();
  const [search] = useSearchParams();
  const sourceId = search.get("sourceId");
  const queryClient = useQueryClient();
  const jobQuery = useQuery({
    queryKey: ["import", jobId],
    queryFn: () => getImport(jobId),
  });
  const [liveJob, setLiveJob] = useState<ImportJob | null>(null);
  const job = liveJob ?? jobQuery.data;
  const cancel = useMutation({
    mutationFn: () => cancelImport(jobId),
  });

  useEffect(() => {
    if (!jobId) return;
    const events = new EventSource(importEventsURL(jobId));
    const update = (event: MessageEvent<string>) => {
      const next = JSON.parse(event.data) as ImportJob;
      setLiveJob(next);
      if (
        ["completed", "failed", "cancelled", "interrupted"].includes(
          next.status,
        )
      ) {
        events.close();
        void queryClient.invalidateQueries({ queryKey: ["sources"] });
      }
    };
    [
      "import.started",
      "import.progress",
      "import.completed",
      "import.failed",
      "import.cancelled",
      "import.interrupted",
    ].forEach((name) => events.addEventListener(name, update as EventListener));
    return () => events.close();
  }, [jobId, queryClient]);

  const fraction = job?.totalBytes
    ? Math.min(job.bytesProcessed / job.totalBytes, 1)
    : 0;
  const terminal =
    job &&
    ["completed", "failed", "cancelled", "interrupted"].includes(job.status);
  const successful = job?.status === "completed";

  return (
    <section className="page import-page">
      <div className="eyebrow">Indexing laboratory</div>
      <div className="import-card">
        <div
          className={`import-glyph ${successful ? "done" : terminal ? "failed" : ""}`}
        >
          {successful ? (
            <CheckCircle2 />
          ) : terminal ? (
            <XCircle />
          ) : (
            <LoaderCircle className="spin" />
          )}
        </div>
        <span className="step-label">Import job · {jobId.slice(0, 8)}</span>
        <h1>
          {successful
            ? "The index is ready."
            : terminal
              ? "Indexing stopped."
              : "Reading the source tape…"}
        </h1>
        <p>
          {successful
            ? "Byte offsets and extracted fields are now available without copying raw content into SQLite."
            : (job?.errorMessage ??
              "Streaming entries through the parser into ordered SQLite batches.")}
        </p>

        <div className="big-progress">
          <div className="progress-copy">
            <strong>{Math.round(fraction * 100)}%</strong>
            <span>{job?.status ?? "connecting"}</span>
          </div>
          <div className="progress-track">
            <span style={{ width: `${fraction * 100}%` }} />
          </div>
          <div className="progress-endpoints">
            <span>{formatBytes(job?.bytesProcessed ?? 0)}</span>
            <span>{formatBytes(job?.totalBytes ?? 0)}</span>
          </div>
        </div>

        <div className="import-metrics">
          <div>
            <FileSearch />
            <span>Entries found</span>
            <strong>{formatCount(job?.entriesFound ?? 0)}</strong>
          </div>
          <div>
            <Database />
            <span>Bytes indexed</span>
            <strong>{formatBytes(job?.bytesProcessed ?? 0)}</strong>
          </div>
          <div>
            <Ban />
            <span>Parse failures</span>
            <strong>{formatCount(job?.parseFailures ?? 0)}</strong>
          </div>
        </div>

        <div className="pipeline">
          <span className="active">stream reader</span>
          <i />
          <span className="active">field parser</span>
          <i />
          <span className={successful ? "active" : ""}>batch writer</span>
        </div>

        <div className="import-actions">
          {!terminal ? (
            <button
              className="button danger-button"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              Cancel import
            </button>
          ) : (
            <>
              <Link className="button" to="/">
                Back to sources
              </Link>
              {successful && sourceId ? (
                <Link
                  className="button button-primary"
                  to={`/sources/${sourceId}`}
                >
                  Open explorer
                </Link>
              ) : null}
            </>
          )}
        </div>
        {jobQuery.isError ? (
          <div className="error-state compact">{jobQuery.error.message}</div>
        ) : null}
      </div>
    </section>
  );
}
