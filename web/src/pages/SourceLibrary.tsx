import { useRef, useState, type DragEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  FileJson2,
  FileText,
  FolderOpen,
  LoaderCircle,
  Plus,
  ShieldCheck,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import {
  deleteSource,
  listSources,
  startImport,
  uploadSource,
  type Detection,
  type Source,
} from "../api";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, formatCount, formatTimestamp } from "../lib/format";

interface Uploaded {
  source: Source;
  detection: Detection;
}

export function SourceLibrary() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  const [uploaded, setUploaded] = useState<Uploaded | null>(null);
  const sources = useQuery({ queryKey: ["sources"], queryFn: listSources });

  const upload = useMutation({
    mutationFn: (file: File) => uploadSource(file, setUploadProgress),
    onSuccess: (result) => {
      setUploaded(result);
      void queryClient.invalidateQueries({ queryKey: ["sources"] });
    },
  });
  const begin = useMutation({
    mutationFn: (sourceId: string) => startImport(sourceId),
    onSuccess: (job) => navigate(`/import/${job.id}?sourceId=${job.sourceId}`),
  });
  const remove = useMutation({
    mutationFn: deleteSource,
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ["sources"] }),
  });

  function pickFile(file?: File) {
    if (!file) return;
    setUploaded(null);
    setUploadProgress(0);
    upload.reset();
    upload.mutate(file);
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    setDragging(false);
    pickFile(event.dataTransfer.files[0]);
  }

  function closeWizard() {
    if (!upload.isPending && !begin.isPending) {
      setWizardOpen(false);
      setUploaded(null);
      setUploadProgress(0);
      upload.reset();
    }
  }

  return (
    <>
      <section className="page library-page">
        <div className="eyebrow">Source library</div>
        <div className="page-heading-row">
          <div>
            <h1>Investigate the record, not the file.</h1>
            <p className="lede">
              Stream, index, and inspect multi-gigabyte logs with bounded
              memory.
            </p>
          </div>
          <button
            className="button button-primary"
            onClick={() => setWizardOpen(true)}
          >
            <Plus size={16} /> Import log file
          </button>
        </div>

        <div className="summary-strip">
          <div>
            <span>Sources</span>
            <strong>{sources.data?.length ?? 0}</strong>
          </div>
          <div>
            <span>Indexed entries</span>
            <strong>
              {formatCount(
                sources.data?.reduce(
                  (sum, source) => sum + source.entryCount,
                  0,
                ) ?? 0,
              )}
            </strong>
          </div>
          <div>
            <span>Storage footprint</span>
            <strong>
              {formatBytes(
                sources.data?.reduce(
                  (sum, source) => sum + source.sizeBytes,
                  0,
                ) ?? 0,
              )}
            </strong>
          </div>
          <div className="method-note">
            <ShieldCheck size={17} />
            <span>
              Raw sources stay immutable. The index stores metadata and byte
              offsets.
            </span>
          </div>
        </div>

        <div className="panel source-panel">
          <div className="panel-heading">
            <div>
              <h2>Recent sources</h2>
              <p>Imported evidence and indexing state</p>
            </div>
          </div>
          {sources.isLoading ? (
            <div className="empty-state">
              <LoaderCircle className="spin" /> Reading the archive…
            </div>
          ) : sources.isError ? (
            <div className="error-state">{sources.error.message}</div>
          ) : sources.data?.length ? (
            <div className="source-table-wrap">
              <table className="source-table">
                <thead>
                  <tr>
                    <th>Source</th>
                    <th>Status</th>
                    <th>Format</th>
                    <th>Size</th>
                    <th>Entries</th>
                    <th>Imported</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {sources.data.map((source) => (
                    <tr key={source.id}>
                      <td>
                        <button
                          className="source-name"
                          onClick={() =>
                            source.entryCount > 0 &&
                            navigate(`/sources/${source.id}`)
                          }
                        >
                          <span className="file-icon">
                            {source.format === "jsonl" ? (
                              <FileJson2 size={17} />
                            ) : (
                              <FileText size={17} />
                            )}
                          </span>
                          <span>
                            <strong>{source.name}</strong>
                            <small>{source.id.slice(0, 8)}</small>
                          </span>
                        </button>
                      </td>
                      <td>
                        <StatusBadge status={source.status} />
                      </td>
                      <td>
                        <span className="mono subtle">
                          {source.format === "jsonl" ? "JSONL" : "PLAIN"}
                        </span>
                      </td>
                      <td>{formatBytes(source.sizeBytes)}</td>
                      <td>{formatCount(source.entryCount)}</td>
                      <td>
                        {formatTimestamp(source.importedAt ?? source.createdAt)}
                      </td>
                      <td>
                        <div className="row-actions">
                          {source.status === "uploaded" ||
                          source.status === "failed" ||
                          source.status === "cancelled" ? (
                            <button
                              className="text-button"
                              onClick={() => begin.mutate(source.id)}
                            >
                              Index
                            </button>
                          ) : null}
                          {source.entryCount > 0 ? (
                            <button
                              className="icon-button"
                              title="Open explorer"
                              onClick={() => navigate(`/sources/${source.id}`)}
                            >
                              <ArrowRight size={16} />
                            </button>
                          ) : null}
                          <button
                            className="icon-button danger"
                            title="Delete source"
                            disabled={
                              source.status === "importing" || remove.isPending
                            }
                            onClick={() => {
                              if (
                                window.confirm(
                                  `Delete ${source.name} and its index?`,
                                )
                              )
                                remove.mutate(source.id);
                            }}
                          >
                            <Trash2 size={15} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="empty-state large">
              <FolderOpen size={34} strokeWidth={1.3} />
              <h3>No evidence on the tape yet</h3>
              <p>
                Import a .log, .txt, .jsonl, or .ndjson file to build the first
                index.
              </p>
              <button className="button" onClick={() => setWizardOpen(true)}>
                <UploadCloud size={16} /> Choose a file
              </button>
            </div>
          )}
        </div>
      </section>

      {wizardOpen ? (
        <div
          className="modal-backdrop"
          role="presentation"
          onMouseDown={closeWizard}
        >
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="import-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <div className="modal-header">
              <div>
                <span className="step-label">New source · 01</span>
                <h2 id="import-title">Import log evidence</h2>
              </div>
              <button
                className="icon-button"
                onClick={closeWizard}
                aria-label="Close"
              >
                <X size={18} />
              </button>
            </div>
            {!uploaded ? (
              <>
                <div
                  className={`drop-zone ${dragging ? "is-dragging" : ""}`}
                  onDragOver={(event) => {
                    event.preventDefault();
                    setDragging(true);
                  }}
                  onDragLeave={() => setDragging(false)}
                  onDrop={onDrop}
                >
                  {upload.isPending ? (
                    <>
                      <LoaderCircle className="spin" size={34} />
                      <h3>Copying source to local storage</h3>
                      <p>{Math.round(uploadProgress * 100)}% uploaded</p>
                      <div className="progress-track">
                        <span style={{ width: `${uploadProgress * 100}%` }} />
                      </div>
                    </>
                  ) : (
                    <>
                      <UploadCloud size={36} strokeWidth={1.4} />
                      <h3>Drop a log file here</h3>
                      <p>.log · .txt · .jsonl · .ndjson</p>
                      <button
                        className="button"
                        onClick={() => inputRef.current?.click()}
                      >
                        Browse files
                      </button>
                      <input
                        ref={inputRef}
                        type="file"
                        hidden
                        accept=".log,.txt,.jsonl,.ndjson"
                        onChange={(event) => pickFile(event.target.files?.[0])}
                      />
                    </>
                  )}
                </div>
                {upload.isError ? (
                  <div className="error-state compact">
                    {upload.error.message}
                  </div>
                ) : null}
                <div className="security-note">
                  <ShieldCheck size={16} />
                  <span>
                    The original filename is metadata only. Stored files receive
                    a generated UUID.
                  </span>
                </div>
              </>
            ) : (
              <div className="detection-result">
                <div className="detection-top">
                  <span className="file-icon large">
                    <FileJson2 size={22} />
                  </span>
                  <div>
                    <h3>{uploaded.source.name}</h3>
                    <p>
                      {formatBytes(uploaded.source.sizeBytes)} · safely stored
                    </p>
                  </div>
                  <span className="confidence">
                    {Math.round(uploaded.detection.confidence * 100)}%
                    confidence
                  </span>
                </div>
                <div className="detected-format">
                  <span>Detected format</span>
                  <strong>
                    {uploaded.detection.format === "jsonl"
                      ? "JSON Lines"
                      : "Generic plain text"}
                  </strong>
                </div>
                <div className="field-map">
                  <h4>Extracted field map</h4>
                  {Object.entries(uploaded.detection.detectedFields).length ? (
                    Object.entries(uploaded.detection.detectedFields).map(
                      ([logical, actual]) => (
                        <div key={logical}>
                          <span>{logical}</span>
                          <code>{actual}</code>
                        </div>
                      ),
                    )
                  ) : (
                    <p>
                      Raw text will be indexed with timestamp and level
                      detection where possible.
                    </p>
                  )}
                </div>
                <div className="modal-actions">
                  <button
                    className="button"
                    onClick={() => {
                      setUploaded(null);
                      upload.reset();
                    }}
                  >
                    Choose another
                  </button>
                  <button
                    className="button button-primary"
                    disabled={begin.isPending}
                    onClick={() => begin.mutate(uploaded.source.id)}
                  >
                    {begin.isPending ? (
                      <LoaderCircle className="spin" size={16} />
                    ) : (
                      <ArrowRight size={16} />
                    )}
                    Build index
                  </button>
                </div>
                {begin.isError ? (
                  <div className="error-state compact">
                    {begin.error.message}
                  </div>
                ) : null}
              </div>
            )}
          </section>
        </div>
      ) : null}
    </>
  );
}
