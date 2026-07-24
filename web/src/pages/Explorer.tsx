import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ArrowLeft,
  Braces,
  ChevronDown,
  CircleAlert,
  Filter,
  Hash,
  Search,
  Settings2,
  X,
} from "lucide-react";
import { Link, useParams } from "react-router-dom";
import {
  getSource,
  getExtractionStatistics,
  getStatistics,
  getTimeline,
  listEntries,
  type EntryFilters,
  type LogEntry,
} from "../api";
import { formatBytes, formatClock, formatCount } from "../lib/format";
import {
  Area,
  AreaChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

const levels = ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL", "UNKNOWN"];

export function Explorer() {
  const { sourceId = "" } = useParams();
  const [draftQuery, setDraftQuery] = useState("");
  const [draftLevel, setDraftLevel] = useState("");
  const [draftService, setDraftService] = useState("");
  const [draftEventType, setDraftEventType] = useState("");
  const [filters, setFilters] = useState<EntryFilters>({});
  const [selected, setSelected] = useState<LogEntry | null>(null);

  const source = useQuery({
    queryKey: ["source", sourceId],
    queryFn: () => getSource(sourceId),
  });
  const stats = useQuery({
    queryKey: ["statistics", sourceId],
    queryFn: () => getStatistics(sourceId),
  });
  const timeline = useQuery({
    queryKey: ["timeline", sourceId],
    queryFn: () => getTimeline(sourceId),
  });
  const extraction = useQuery({
    queryKey: ["extraction-statistics", sourceId],
    queryFn: () => getExtractionStatistics(sourceId),
    enabled: source.data?.extractionStatus !== "not_configured",
  });
  const entries = useInfiniteQuery({
    queryKey: ["entries", sourceId, filters],
    queryFn: ({ pageParam }) => listEntries(sourceId, pageParam, filters),
    initialPageParam: 1,
    getNextPageParam: (page) =>
      page.page.hasMore ? page.page.nextCursor : undefined,
  });

  const flatEntries = useMemo(
    () => entries.data?.pages.flatMap((page) => page.items) ?? [],
    [entries.data],
  );
  const scrollRef = useRef<HTMLDivElement>(null);
  // TanStack Virtual intentionally returns imperative functions; React Compiler
  // must not memoize this hook result because scroll state lives outside React.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: flatEntries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 47,
    overscan: 20,
  });
  const virtualItems = virtualizer.getVirtualItems();
  const lastVirtualIndex = virtualItems.at(-1)?.index ?? 0;

  useEffect(() => {
    if (
      lastVirtualIndex >= flatEntries.length - 30 &&
      entries.hasNextPage &&
      !entries.isFetchingNextPage
    ) {
      void entries.fetchNextPage();
    }
  }, [lastVirtualIndex, flatEntries.length, entries]);

  function applyFilters(event: React.FormEvent) {
    event.preventDefault();
    setSelected(null);
    setFilters({
      query: draftQuery.trim() || undefined,
      levels: draftLevel ? [draftLevel] : undefined,
      services: draftService ? [draftService] : undefined,
      eventTypes: draftEventType ? [draftEventType] : undefined,
    });
  }

  function clearFilters() {
    setDraftQuery("");
    setDraftLevel("");
    setDraftService("");
    setDraftEventType("");
    setFilters({});
  }

  const hasFilters = Boolean(
    filters.query ||
    filters.levels?.length ||
    filters.services?.length ||
    filters.eventTypes?.length,
  );

  if (source.isLoading) {
    return <div className="page loading-page">Opening indexed source…</div>;
  }
  if (source.isError || !source.data) {
    return (
      <div className="page">
        <div className="error-state">
          {source.error?.message ?? "Source not found"}
        </div>
      </div>
    );
  }

  return (
    <section className={`explorer-page ${selected ? "inspector-open" : ""}`}>
      <div className="explorer-main">
        <div className="explorer-titlebar">
          <div className="source-title">
            <Link to="/" className="icon-button" aria-label="Back to sources">
              <ArrowLeft size={17} />
            </Link>
            <div>
              <div className="eyebrow">
                Indexed source ·{" "}
                {source.data.format === "jsonl" ? "JSON Lines" : "Plain text"}
              </div>
              <h1>{source.data.name}</h1>
            </div>
          </div>
          <div className="source-facts">
            <div>
              <span>Parser</span>
              <strong>
                {source.data.parserVersion
                  ? `v${source.data.parserVersion}`
                  : "Raw mode"}
              </strong>
            </div>
            <div>
              <span>Extraction</span>
              <strong>
                {extraction.data
                  ? `${(extraction.data.coverage * 100).toFixed(1)}%`
                  : "—"}
              </strong>
            </div>
            <div>
              <span>Entries</span>
              <strong>{formatCount(source.data.entryCount)}</strong>
            </div>
            <div>
              <span>Raw size</span>
              <strong>{formatBytes(source.data.sizeBytes)}</strong>
            </div>
            <div>
              <span>Parse yield</span>
              <strong>
                {source.data.entryCount
                  ? Math.round(
                      (source.data.parsedCount / source.data.entryCount) * 100,
                    )
                  : 0}
                %
              </strong>
            </div>
            <Link
              to={`/sources/${sourceId}/extraction`}
              className="icon-button"
              title="Extraction settings"
            >
              <Settings2 size={16} />
            </Link>
          </div>
        </div>

        <form className="filter-bar" onSubmit={applyFilters}>
          <label className="search-box">
            <Search size={16} />
            <input
              value={draftQuery}
              onChange={(event) => setDraftQuery(event.target.value)}
              placeholder="Search raw entries…"
            />
          </label>
          <label className="select-box">
            <span>Level</span>
            <select
              value={draftLevel}
              onChange={(event) => setDraftLevel(event.target.value)}
            >
              <option value="">All levels</option>
              {levels.map((level) => (
                <option key={level}>{level}</option>
              ))}
            </select>
            <ChevronDown size={14} />
          </label>
          <label className="select-box">
            <span>Event</span>
            <select
              value={draftEventType}
              onChange={(event) => setDraftEventType(event.target.value)}
            >
              <option value="">All event types</option>
              {Object.keys(extraction.data?.byEventType ?? {}).map(
                (eventType) => (
                  <option key={eventType}>{eventType}</option>
                ),
              )}
            </select>
            <ChevronDown size={14} />
          </label>
          <label className="select-box">
            <span>Service</span>
            <select
              value={draftService}
              onChange={(event) => setDraftService(event.target.value)}
            >
              <option value="">All services</option>
              {Object.keys(stats.data?.byService ?? {}).map((service) => (
                <option key={service}>{service}</option>
              ))}
            </select>
            <ChevronDown size={14} />
          </label>
          <button className="button button-primary filter-submit" type="submit">
            <Filter size={15} /> Apply
          </button>
          {hasFilters ? (
            <button
              className="icon-button"
              type="button"
              onClick={clearFilters}
              title="Clear filters"
            >
              <X size={16} />
            </button>
          ) : null}
        </form>

        <div className="timeline-panel">
          <div className="timeline-heading">
            <div>
              <span>Chronology</span>
              <strong>{timeline.data?.length ?? 0} time buckets</strong>
            </div>
            <div className="timeline-legend">
              <i className="all" /> entries <i className="errors" /> errors
            </div>
          </div>
          <div className="timeline-chart">
            {timeline.data?.length ? (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart
                  data={timeline.data}
                  margin={{ top: 8, right: 8, left: -30, bottom: 0 }}
                >
                  <defs>
                    <linearGradient
                      id="entriesFill"
                      x1="0"
                      y1="0"
                      x2="0"
                      y2="1"
                    >
                      <stop offset="0%" stopColor="#475f8f" stopOpacity={0.3} />
                      <stop
                        offset="100%"
                        stopColor="#475f8f"
                        stopOpacity={0.02}
                      />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="timestamp"
                    tickFormatter={(value) => formatClock(value).slice(0, 5)}
                    minTickGap={50}
                    axisLine={false}
                    tickLine={false}
                    tick={{ fontSize: 10, fill: "#74777a" }}
                  />
                  <YAxis
                    axisLine={false}
                    tickLine={false}
                    tick={{ fontSize: 10, fill: "#74777a" }}
                  />
                  <Tooltip
                    labelFormatter={(value) =>
                      new Date(String(value)).toLocaleString()
                    }
                  />
                  <Area
                    type="monotone"
                    dataKey="count"
                    stroke="#475f8f"
                    fill="url(#entriesFill)"
                    strokeWidth={1.5}
                  />
                  <Area
                    type="monotone"
                    dataKey="errors"
                    stroke="#ae4848"
                    fill="transparent"
                    strokeWidth={1.5}
                  />
                </AreaChart>
              </ResponsiveContainer>
            ) : (
              <div className="timeline-empty">
                No timestamped entries in this source
              </div>
            )}
          </div>
        </div>

        <div className="log-table">
          <div className="log-header">
            <span># / Time</span>
            <span>Level</span>
            <span>Service</span>
            <span>Message</span>
          </div>
          <div ref={scrollRef} className="log-scroll">
            {entries.isLoading ? (
              <div className="table-message">
                Reading the first indexed window…
              </div>
            ) : entries.isError ? (
              <div className="error-state compact">{entries.error.message}</div>
            ) : flatEntries.length === 0 ? (
              <div className="table-message">
                <CircleAlert size={20} /> No entries match this evidence filter.
              </div>
            ) : (
              <div
                style={{
                  height: `${virtualizer.getTotalSize()}px`,
                  position: "relative",
                }}
              >
                {virtualItems.map((virtualRow) => {
                  const entry = flatEntries[virtualRow.index];
                  return (
                    <button
                      key={entry.number}
                      className={`log-row ${selected?.number === entry.number ? "selected" : ""}`}
                      style={{ transform: `translateY(${virtualRow.start}px)` }}
                      onClick={() => setSelected(entry)}
                    >
                      <span className="entry-time">
                        <small>#{entry.number.toLocaleString()}</small>
                        <strong>{formatClock(entry.timestamp)}</strong>
                      </span>
                      <span
                        className={`level level-${entry.level.toLowerCase()}`}
                      >
                        <i />
                        {entry.level}
                      </span>
                      <span className="service-cell">
                        {entry.service || "—"}
                      </span>
                      <span className="message-cell">{entry.message}</span>
                    </button>
                  );
                })}
              </div>
            )}
            {entries.isFetchingNextPage ? (
              <div className="fetching-more">Loading the next window…</div>
            ) : null}
          </div>
          <div className="log-footer">
            <span>
              Rendered {virtualItems.length} rows · buffered{" "}
              {formatCount(flatEntries.length)}
            </span>
            <span>Overscan 20 · page 200</span>
          </div>
        </div>
      </div>

      {selected ? (
        <EntryInspector entry={selected} onClose={() => setSelected(null)} />
      ) : null}
    </section>
  );
}

function EntryInspector({
  entry,
  onClose,
}: {
  entry: LogEntry;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<"summary" | "raw" | "fields">("summary");
  return (
    <aside className="inspector">
      <div className="inspector-header">
        <div>
          <span className="step-label">Evidence detail</span>
          <h2>Entry #{entry.number.toLocaleString()}</h2>
        </div>
        <button
          className="icon-button"
          onClick={onClose}
          aria-label="Close inspector"
        >
          <X size={18} />
        </button>
      </div>
      <dl className="entry-metadata">
        <div>
          <dt>Timestamp</dt>
          <dd className="mono">{entry.timestamp ?? "Not detected"}</dd>
        </div>
        <div>
          <dt>Level</dt>
          <dd>
            <span className={`level level-${entry.level.toLowerCase()}`}>
              <i />
              {entry.level}
            </span>
          </dd>
        </div>
        <div>
          <dt>Service</dt>
          <dd>{entry.service || "—"}</dd>
        </div>
        <div>
          <dt>Request ID</dt>
          <dd className="mono">{entry.requestId || "—"}</dd>
        </div>
        <div>
          <dt>Event type</dt>
          <dd>{entry.eventType || "Unmatched"}</dd>
        </div>
        <div>
          <dt>Parser version</dt>
          <dd>
            {entry.parserVersion ? `v${entry.parserVersion}` : "Raw mode"}
          </dd>
        </div>
        <div>
          <dt>Byte range</dt>
          <dd className="mono">
            {entry.byteOffset.toLocaleString()} +{" "}
            {formatBytes(entry.byteLength)}
          </dd>
        </div>
        <div>
          <dt>Parse status</dt>
          <dd>{entry.parseStatus}</dd>
        </div>
      </dl>
      <div className="inspector-tabs">
        <button
          className={tab === "summary" ? "active" : ""}
          onClick={() => setTab("summary")}
        >
          Summary
        </button>
        <button
          className={tab === "fields" ? "active" : ""}
          onClick={() => setTab("fields")}
        >
          <Braces size={14} /> Extracted fields
        </button>
        <button
          className={tab === "raw" ? "active" : ""}
          onClick={() => setTab("raw")}
        >
          <Hash size={14} /> Raw
        </button>
      </div>
      {tab === "summary" ? (
        <div className="inspector-summary">
          <strong>{entry.eventType || entry.level}</strong>
          <p>{entry.message}</p>
          {entry.warningCount ? (
            <span>{entry.warningCount} extraction warning(s)</span>
          ) : null}
        </div>
      ) : tab === "raw" ? (
        <pre className="raw-block">{entry.raw}</pre>
      ) : (
        <pre className="raw-block">
          {JSON.stringify(entry.fields ?? {}, null, 2)}
        </pre>
      )}
      <div className="inspector-note">
        Content is rendered as inert text. ANSI control sequences are removed by
        the API.
      </div>
    </aside>
  );
}
