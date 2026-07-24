import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Braces, CircleStop, Play, RefreshCw } from "lucide-react";
import { Link, useParams } from "react-router-dom";
import {
  assignSourceParser,
  cancelReindex,
  getExtractionStatistics,
  getParserProfile,
  getReindex,
  getSource,
  getSourceFields,
  listParserProfiles,
  startReindex,
} from "../api";
import { formatCount } from "../lib/format";

export function SourceExtraction() {
  const { sourceId = "" } = useParams();
  const client = useQueryClient();
  const [profileOverride, setProfileOverride] = useState<string | null>(null);
  const [versionOverride, setVersionOverride] = useState<number | null>(null);
  const source = useQuery({
    queryKey: ["source", sourceId],
    queryFn: () => getSource(sourceId),
  });
  const profiles = useQuery({
    queryKey: ["parser-profiles"],
    queryFn: listParserProfiles,
  });
  const profileId = profileOverride ?? source.data?.parserProfileId ?? "";
  const selectedProfile = useQuery({
    queryKey: ["parser-profile", profileId],
    queryFn: () => getParserProfile(profileId),
    enabled: Boolean(profileId),
  });
  const version =
    versionOverride ??
    (profileId === source.data?.parserProfileId
      ? source.data?.parserVersion
      : selectedProfile.data?.versions[0]?.version) ??
    0;
  const stats = useQuery({
    queryKey: ["extraction-statistics", sourceId],
    queryFn: () => getExtractionStatistics(sourceId),
    enabled: source.data?.extractionStatus !== "not_configured",
  });
  const fields = useQuery({
    queryKey: ["source-fields", sourceId],
    queryFn: () => getSourceFields(sourceId),
    enabled: source.data?.extractionStatus !== "not_configured",
  });
  const job = useQuery({
    queryKey: ["reindex", sourceId],
    queryFn: () => getReindex(sourceId),
    enabled: source.data?.extractionStatus === "indexing",
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "queued" || status === "running" ? 500 : false;
    },
  });

  const invalidate = () => {
    void client.invalidateQueries({ queryKey: ["source", sourceId] });
    void client.invalidateQueries({
      queryKey: ["extraction-statistics", sourceId],
    });
    void client.invalidateQueries({ queryKey: ["source-fields", sourceId] });
    void client.invalidateQueries({ queryKey: ["entries", sourceId] });
  };
  const assign = useMutation({
    mutationFn: () => assignSourceParser(sourceId, profileId, version),
    onSuccess: invalidate,
  });
  const reindex = useMutation({
    mutationFn: () => startReindex(sourceId),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["reindex", sourceId] });
      invalidate();
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelReindex(sourceId),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: ["reindex", sourceId] }),
  });
  const running =
    job.data?.status === "queued" || job.data?.status === "running";

  if (!source.data) {
    return (
      <div className="page loading-page">
        {source.isError ? source.error.message : "Opening extraction settings…"}
      </div>
    );
  }

  return (
    <section className="page extraction-page">
      <div className="studio-titlebar">
        <Link
          to={`/sources/${sourceId}`}
          className="icon-button"
          aria-label="Back to source"
        >
          <ArrowLeft size={17} />
        </Link>
        <div>
          <div className="eyebrow">Source extraction · v0.4</div>
          <h1>{source.data.name}</h1>
          <p>
            Raw mode stays available while semantic fields are rebuilt from byte
            offsets.
          </p>
        </div>
      </div>

      <div className="extraction-grid">
        <section className="panel extraction-settings">
          <div className="panel-heading">
            <div>
              <h2>Parser assignment</h2>
              <p>Sources are pinned to one immutable parser version.</p>
            </div>
            <Braces size={19} />
          </div>
          <label className="form-field">
            <span>Parser profile</span>
            <select
              value={profileId}
              onChange={(event) => {
                setProfileOverride(event.target.value);
                setVersionOverride(null);
              }}
            >
              <option value="">No extraction profile</option>
              {profiles.data?.map((profile) => (
                <option value={profile.id} key={profile.id}>
                  {profile.name}
                </option>
              ))}
            </select>
          </label>
          <label className="form-field">
            <span>Version</span>
            <select
              value={version}
              onChange={(event) =>
                setVersionOverride(Number(event.target.value))
              }
              disabled={!profileId}
            >
              <option value={0}>Choose version</option>
              {selectedProfile.data?.versions.map((item) => (
                <option value={item.version} key={item.id}>
                  v{item.version} · {item.rules.length} rules ·{" "}
                  {item.fields.length} fields
                </option>
              ))}
            </select>
          </label>
          <button
            className="button"
            disabled={!profileId || !version || assign.isPending || running}
            onClick={() => assign.mutate()}
          >
            <RefreshCw size={15} /> Apply version
          </button>
          <button
            className="button button-primary"
            disabled={
              !source.data.parserProfileId || reindex.isPending || running
            }
            onClick={() => reindex.mutate()}
          >
            <Play size={15} /> Reindex extracted fields
          </button>
          {running ? (
            <button
              className="button danger"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              <CircleStop size={15} /> Cancel reindex
            </button>
          ) : null}
          {assign.isError || reindex.isError || cancel.isError ? (
            <div className="error-state compact">
              {assign.error?.message ??
                reindex.error?.message ??
                cancel.error?.message}
            </div>
          ) : null}
          <div className="assignment-summary">
            <span>Extraction status</span>
            <strong>{source.data.extractionStatus}</strong>
            <span>Assigned version</span>
            <strong>
              {source.data.parserVersion
                ? `v${source.data.parserVersion}`
                : "Raw mode"}
            </strong>
            <span>Reindex required</span>
            <strong>{source.data.reindexRequired ? "yes" : "no"}</strong>
          </div>
          {job.data ? (
            <div className="reindex-progress">
              <div>
                <span>{job.data.status}</span>
                <strong>
                  {formatCount(job.data.entriesProcessed)} /{" "}
                  {formatCount(job.data.totalEntries)}
                </strong>
              </div>
              <progress
                max={Math.max(job.data.totalEntries, 1)}
                value={job.data.entriesProcessed}
              />
            </div>
          ) : null}
        </section>

        <section className="panel extraction-results">
          <div className="panel-heading">
            <div>
              <h2>Extraction statistics</h2>
              <p>Coverage reflects the currently indexed parser version.</p>
            </div>
          </div>
          {source.data.extractionStatus === "not_configured" ? (
            <div className="empty-state">
              <h3>No extraction profile configured</h3>
              <p>
                This source remains available in raw log mode. Configure rules
                to identify events and custom fields.
              </p>
            </div>
          ) : (
            <>
              <div className="preview-metrics">
                <div>
                  <span>Coverage</span>
                  <strong>
                    {((stats.data?.coverage ?? 0) * 100).toFixed(1)}%
                  </strong>
                </div>
                <div>
                  <span>Matched</span>
                  <strong>
                    {formatCount(stats.data?.matchedEntries ?? 0)}
                  </strong>
                </div>
                <div>
                  <span>Unmatched</span>
                  <strong>
                    {formatCount(stats.data?.unmatchedEntries ?? 0)}
                  </strong>
                </div>
                <div>
                  <span>Warnings</span>
                  <strong>
                    {formatCount(stats.data?.warningEntries ?? 0)}
                  </strong>
                </div>
              </div>
              <div className="field-coverage-table">
                <div className="field-coverage-row header">
                  <span>Field</span>
                  <span>Type</span>
                  <span>Coverage</span>
                  <span>Indexed</span>
                </div>
                {fields.data?.map((field) => (
                  <div className="field-coverage-row" key={field.id}>
                    <strong>{field.name}</strong>
                    <span>{field.fieldType}</span>
                    <span>{(field.coverage * 100).toFixed(1)}%</span>
                    <span>{field.indexed ? "yes" : "no"}</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </section>
      </div>
    </section>
  );
}
