import type { SourceStatus } from "../api";

const labels: Record<SourceStatus, string> = {
  uploaded: "Awaiting index",
  queued: "Queued",
  importing: "Indexing",
  ready: "Ready",
  partially_parsed: "Partial",
  failed: "Failed",
  cancelled: "Cancelled",
};

export function StatusBadge({ status }: { status: SourceStatus }) {
  return (
    <span className={`status-badge status-${status}`}>{labels[status]}</span>
  );
}
