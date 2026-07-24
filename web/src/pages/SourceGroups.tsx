import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Clock3,
  GitMerge,
  Link2,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  correlateSourceGroup,
  createSourceGroup,
  deleteCanonicalMapping,
  deleteSourceGroup,
  deleteSourceGroupMember,
  getSourceFields,
  getSourceGroup,
  getSourceGroupTimeline,
  listSourceGroups,
  listSources,
  querySourceGroup,
  upsertCanonicalMapping,
  upsertSourceGroupMember,
  type CorrelatedEntry,
  type TypedFilter,
} from "../api";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

const baseFields = [
  "request_id",
  "event_type",
  "level",
  "service",
  "parse_status",
  "timestamp",
  "entry_number",
];

export function SourceGroups() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const groups = useQuery({
    queryKey: ["source-groups"],
    queryFn: listSourceGroups,
  });
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState("");
  const create = useMutation({
    mutationFn: () => createSourceGroup({ name: name.trim(), description }),
    onSuccess: (group) => {
      void client.invalidateQueries({ queryKey: ["source-groups"] });
      void navigate(`/groups/${group.id}`);
    },
    onError: (failure) => setError(failure.message),
  });

  return (
    <section className="page groups-page">
      <span className="eyebrow">v0.6 · Multi-Source Correlation</span>
      <div className="page-heading-row">
        <div>
          <h1>Join the evidence across clocks and schemas.</h1>
          <p className="lede">
            Source groups normalize fields, correct timestamps, and merge
            related events without changing raw files.
          </p>
        </div>
      </div>
      {error ? <div className="error-state compact">{error}</div> : null}
      <div className="groups-grid">
        <div className="panel group-list-panel">
          <div className="panel-heading">
            <div>
              <h2>Source groups</h2>
              <p>{groups.data?.length ?? 0} correlation workspaces</p>
            </div>
          </div>
          <div className="group-list">
            {groups.data?.map((group) => (
              <Link key={group.id} to={`/groups/${group.id}`}>
                <GitMerge size={18} />
                <span>
                  <strong>{group.name}</strong>
                  <small>
                    {group.memberCount} sources ·{" "}
                    {group.description || "No description"}
                  </small>
                </span>
              </Link>
            ))}
            {!groups.isLoading && !groups.data?.length ? (
              <div className="table-message">
                Create the first source group.
              </div>
            ) : null}
          </div>
        </div>
        <aside className="panel create-group-panel">
          <h2>New group</h2>
          <label className="form-field">
            <span>Name</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Checkout incident"
            />
          </label>
          <label className="form-field">
            <span>Description</span>
            <textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="API, worker, and database evidence"
            />
          </label>
          <button
            className="button button-primary"
            disabled={!name.trim() || create.isPending}
            onClick={() => create.mutate()}
          >
            <Plus size={15} /> Create group
          </button>
        </aside>
      </div>
    </section>
  );
}

export function SourceGroupDetail() {
  const { groupId = "" } = useParams();
  const client = useQueryClient();
  const navigate = useNavigate();
  const group = useQuery({
    queryKey: ["source-group", groupId],
    queryFn: () => getSourceGroup(groupId),
  });
  const sources = useQuery({ queryKey: ["sources"], queryFn: listSources });
  const timeline = useQuery({
    queryKey: ["source-group-timeline", groupId],
    queryFn: () => getSourceGroupTimeline(groupId),
    enabled: Boolean(group.data?.memberCount),
  });
  const [memberSource, setMemberSource] = useState("");
  const [timeZone, setTimeZone] = useState("UTC");
  const [clockOffset, setClockOffset] = useState("0");
  const [mappingSource, setMappingSource] = useState("");
  const [canonicalName, setCanonicalName] = useState("correlation_id");
  const [sourceField, setSourceField] = useState("request_id");
  const [rawQuery, setRawQuery] = useState("");
  const [filterField, setFilterField] = useState("");
  const [filterValue, setFilterValue] = useState("");
  const [correlationField, setCorrelationField] = useState("correlation_id");
  const [correlationValue, setCorrelationValue] = useState("");
  const [results, setResults] = useState<CorrelatedEntry[]>([]);
  const [error, setError] = useState("");

  const selectedSource = mappingSource || group.data?.members?.[0]?.sourceId;
  const selectedFields = useQuery({
    queryKey: ["source-fields", selectedSource],
    queryFn: () => getSourceFields(selectedSource!),
    enabled: Boolean(selectedSource),
  });
  const sourceFieldOptions = useMemo(
    () => [
      ...baseFields,
      ...(selectedFields.data ?? []).map((field) => field.name),
    ],
    [selectedFields.data],
  );
  const canonicalOptions = useMemo(
    () =>
      Array.from(
        new Set([
          ...baseFields,
          ...(group.data?.mappings ?? []).map(
            (mapping) => mapping.canonicalName,
          ),
        ]),
      ),
    [group.data?.mappings],
  );

  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["source-group", groupId] });
    void client.invalidateQueries({
      queryKey: ["source-group-timeline", groupId],
    });
    void client.invalidateQueries({ queryKey: ["source-groups"] });
  };
  const addMember = useMutation({
    mutationFn: () =>
      upsertSourceGroupMember(groupId, memberSource, {
        timeZone: timeZone.trim(),
        manualClockOffsetMs: Number(clockOffset),
      }),
    onSuccess: () => {
      setMemberSource("");
      setError("");
      refresh();
    },
    onError: (failure) => setError(failure.message),
  });
  const addMapping = useMutation({
    mutationFn: () =>
      upsertCanonicalMapping(groupId, {
        sourceId: mappingSource,
        canonicalName: canonicalName.trim(),
        sourceField,
      }),
    onSuccess: () => {
      setError("");
      refresh();
    },
    onError: (failure) => setError(failure.message),
  });
  const search = useMutation({
    mutationFn: () => {
      const filters: TypedFilter[] =
        filterField && filterValue
          ? [
              {
                field: filterField,
                operator: "eq",
                value: parseValue(filterValue),
              },
            ]
          : [];
      return querySourceGroup(groupId, {
        query: rawQuery.trim() || undefined,
        filters,
        sort: "asc",
        limit: 500,
      });
    },
    onSuccess: (response) => {
      setResults(response.items);
      setError("");
    },
    onError: (failure) => setError(failure.message),
  });
  const correlate = useMutation({
    mutationFn: () =>
      correlateSourceGroup(groupId, {
        field: correlationField,
        value: parseValue(correlationValue),
        limit: 500,
      }),
    onSuccess: (response) => {
      setResults(response.items);
      setError("");
    },
    onError: (failure) => setError(failure.message),
  });

  if (group.isLoading)
    return <div className="page loading-page">Opening source group…</div>;
  if (!group.data)
    return (
      <div className="page">
        <div className="error-state">Source group not found</div>
      </div>
    );

  return (
    <section className="page group-detail-page">
      <div className="studio-titlebar">
        <Link to="/groups" className="icon-button" aria-label="Back to groups">
          <ArrowLeft size={18} />
        </Link>
        <div>
          <span className="eyebrow">Merged evidence workspace</span>
          <h1>{group.data.name}</h1>
          <p className="lede">{group.data.description}</p>
        </div>
        <button
          className="button danger-button"
          onClick={() =>
            void deleteSourceGroup(groupId).then(() => navigate("/groups"))
          }
        >
          <Trash2 size={14} /> Delete group
        </button>
      </div>
      {error ? <div className="error-state compact">{error}</div> : null}

      <div className="correlation-layout">
        <div className="panel member-panel">
          <div className="panel-heading">
            <div>
              <h2>Sources & clocks</h2>
              <p>Timezone reinterprets wall time; offset applies last.</p>
            </div>
          </div>
          <div className="member-list">
            {group.data.members?.map((member) => (
              <div key={member.sourceId}>
                <Clock3 size={15} />
                <span>
                  <strong>{member.sourceName}</strong>
                  <small>
                    {member.timeZone} · {member.manualClockOffsetMs} ms
                  </small>
                </span>
                <button
                  className="button"
                  onClick={() => {
                    setMemberSource(member.sourceId);
                    setTimeZone(member.timeZone);
                    setClockOffset(String(member.manualClockOffsetMs));
                  }}
                >
                  Edit clock
                </button>
                <button
                  className="icon-button"
                  onClick={() =>
                    void deleteSourceGroupMember(groupId, member.sourceId).then(
                      refresh,
                    )
                  }
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
          </div>
          <div className="member-form">
            <select
              value={memberSource}
              onChange={(event) => setMemberSource(event.target.value)}
            >
              <option value="">Select source</option>
              {sources.data?.map((source) => (
                <option key={source.id} value={source.id}>
                  {source.name}
                  {group.data.members?.some(
                    (member) => member.sourceId === source.id,
                  )
                    ? " (update)"
                    : ""}
                </option>
              ))}
            </select>
            <input
              value={timeZone}
              onChange={(event) => setTimeZone(event.target.value)}
              placeholder="Europe/Kyiv"
            />
            <input
              type="number"
              value={clockOffset}
              onChange={(event) => setClockOffset(event.target.value)}
              title="Manual clock offset in milliseconds"
            />
            <button
              className="button"
              disabled={!memberSource}
              onClick={() => addMember.mutate()}
            >
              Add
            </button>
          </div>
        </div>

        <div className="panel mapping-panel">
          <div className="panel-heading">
            <div>
              <h2>Canonical fields</h2>
              <p>Map differently named fields into one investigation key.</p>
            </div>
          </div>
          <div className="mapping-list">
            {group.data.mappings?.map((mapping) => (
              <div key={mapping.id}>
                <Link2 size={14} />
                <code>
                  {mapping.canonicalName} ← {mapping.sourceField}
                </code>
                <small>
                  {
                    group.data.members?.find(
                      (member) => member.sourceId === mapping.sourceId,
                    )?.sourceName
                  }
                </small>
                <button
                  className="icon-button"
                  onClick={() =>
                    void deleteCanonicalMapping(groupId, mapping.id).then(
                      refresh,
                    )
                  }
                >
                  <Trash2 size={13} />
                </button>
              </div>
            ))}
          </div>
          <div className="mapping-form">
            <select
              value={mappingSource}
              onChange={(event) => setMappingSource(event.target.value)}
            >
              <option value="">Source</option>
              {group.data.members?.map((member) => (
                <option key={member.sourceId} value={member.sourceId}>
                  {member.sourceName}
                </option>
              ))}
            </select>
            <input
              value={canonicalName}
              onChange={(event) => setCanonicalName(event.target.value)}
              placeholder="correlation_id"
            />
            <select
              value={sourceField}
              onChange={(event) => setSourceField(event.target.value)}
            >
              {sourceFieldOptions.map((field) => (
                <option key={field}>{field}</option>
              ))}
            </select>
            <button
              className="button"
              disabled={!mappingSource || !canonicalName.trim()}
              onClick={() => addMapping.mutate()}
            >
              Map
            </button>
          </div>
        </div>

        <div className="panel merged-timeline">
          <div className="panel-heading">
            <div>
              <h2>Merged timeline</h2>
              <p>Corrected timestamps · one-minute buckets</p>
            </div>
          </div>
          <div className="group-timeline-chart">
            {timeline.data?.length ? (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={timeline.data}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis
                    dataKey="timestamp"
                    tickFormatter={(value) =>
                      new Date(String(value)).toLocaleTimeString()
                    }
                  />
                  <YAxis />
                  <Tooltip />
                  <Area dataKey="count" stroke="#475f8f" fill="#e4e8f1" />
                  <Area dataKey="errors" stroke="#ae4848" fill="transparent" />
                </AreaChart>
              </ResponsiveContainer>
            ) : (
              <div className="table-message">
                Add timestamped sources to build the timeline.
              </div>
            )}
          </div>
        </div>

        <div className="panel group-search-panel">
          <div className="panel-heading">
            <div>
              <h2>Cross-source search</h2>
              <p>Raw text plus one canonical typed filter.</p>
            </div>
          </div>
          <div className="group-search-form">
            <input
              value={rawQuery}
              onChange={(event) => setRawQuery(event.target.value)}
              placeholder="Raw text"
            />
            <select
              value={filterField}
              onChange={(event) => setFilterField(event.target.value)}
            >
              <option value="">No canonical filter</option>
              {canonicalOptions.map((field) => (
                <option key={field}>{field}</option>
              ))}
            </select>
            <input
              value={filterValue}
              onChange={(event) => setFilterValue(event.target.value)}
              placeholder="Filter value"
            />
            <button className="button" onClick={() => search.mutate()}>
              <Search size={14} /> Search
            </button>
          </div>
          <div className="correlation-form">
            <select
              value={correlationField}
              onChange={(event) => setCorrelationField(event.target.value)}
            >
              {canonicalOptions.map((field) => (
                <option key={field}>{field}</option>
              ))}
            </select>
            <input
              value={correlationValue}
              onChange={(event) => setCorrelationValue(event.target.value)}
              placeholder="Correlation value"
            />
            <button
              className="button button-primary"
              disabled={!correlationValue}
              onClick={() => correlate.mutate()}
            >
              <GitMerge size={14} /> Correlate
            </button>
          </div>
          <CorrelationResults items={results} />
        </div>
      </div>
    </section>
  );
}

function CorrelationResults({ items }: { items: CorrelatedEntry[] }) {
  if (!items.length)
    return (
      <div className="table-message">
        Search or correlate to merge matching events.
      </div>
    );
  return (
    <div className="correlation-results">
      {items.map((item) => (
        <div key={`${item.sourceId}-${item.entry.number}`}>
          <span>{item.sourceName}</span>
          <time>
            {item.correctedTimestamp
              ? new Date(item.correctedTimestamp).toLocaleString()
              : "No timestamp"}
          </time>
          <strong>{item.entry.eventType || item.entry.level}</strong>
          <code>{item.entry.message}</code>
          <small>{JSON.stringify(item.canonicalFields)}</small>
        </div>
      ))}
    </div>
  );
}

function parseValue(value: string): unknown {
  if (value === "true") return true;
  if (value === "false") return false;
  const number = Number(value);
  return value.trim() && Number.isFinite(number) ? number : value;
}
