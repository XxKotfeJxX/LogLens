export type SourceStatus =
  | "uploaded"
  | "queued"
  | "importing"
  | "ready"
  | "partially_parsed"
  | "failed"
  | "cancelled";

export interface Source {
  id: string;
  name: string;
  originalName: string;
  sourceType: "uploaded_file" | "server_file" | "watched_file" | "remote_agent";
  format: "jsonl" | "plain_text";
  status: SourceStatus;
  sizeBytes: number;
  entryCount: number;
  parsedCount: number;
  failedCount: number;
  parserProfileId?: string;
  parserVersion?: number;
  extractionStatus:
    | "not_configured"
    | "configured"
    | "indexing"
    | "ready"
    | "partial"
    | "failed";
  reindexRequired: boolean;
  createdAt: string;
  importedAt?: string;
}

export interface LogEntry {
  number: number;
  byteOffset: number;
  byteLength: number;
  timestamp?: string;
  level: string;
  service?: string;
  requestId?: string;
  message: string;
  parseStatus: string;
  raw: string;
  fields?: Record<string, unknown>;
  eventType?: string;
  matchedRuleId?: string;
  parserVersion?: number;
  warningCount: number;
}

export interface Detection {
  format: "jsonl" | "plain_text";
  confidence: number;
  detectedFields: Record<string, string>;
  preview: LogEntry[];
}

export interface ImportJob {
  id: string;
  sourceId: string;
  status:
    "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";
  bytesProcessed: number;
  totalBytes: number;
  entriesFound: number;
  parseFailures: number;
  startedAt?: string;
  completedAt?: string;
  errorCode?: string;
  errorMessage?: string;
}

export interface Statistics {
  totalEntries: number;
  parsedEntries: number;
  malformedEntries: number;
  earliestTimestamp?: string;
  latestTimestamp?: string;
  byLevel: Record<string, number>;
  byService: Record<string, number>;
}

export interface TimelinePoint {
  timestamp: string;
  count: number;
  errors: number;
}

export interface EntryFilters {
  query?: string;
  levels?: string[];
  services?: string[];
  requestId?: string;
  from?: string;
  to?: string;
  eventTypes?: string[];
}

export type FieldType =
  "string" | "integer" | "float" | "boolean" | "datetime" | "duration";

export interface ParserProfile {
  id: string;
  name: string;
  description?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ExtractionRule {
  id: string;
  parserVersionId: string;
  name: string;
  eventType: string;
  priority: number;
  matcherType: "regex" | "key_value" | "json" | "literal_pattern";
  matcherConfig: Record<string, unknown>;
  enabled: boolean;
}

export interface FieldDefinition {
  id: string;
  parserVersionId: string;
  name: string;
  displayName?: string;
  fieldType: FieldType;
  indexed: boolean;
  aggregatable: boolean;
  nullable: boolean;
}

export interface ParserProfileVersion {
  id: string;
  profileId: string;
  version: number;
  recordFormat: string;
  configuration: Record<string, unknown>;
  createdAt: string;
  rules: ExtractionRule[];
  fields: FieldDefinition[];
}

export interface PreviewResult {
  version: number;
  sampleSize: number;
  matched: number;
  unmatched: number;
  coverage: number;
  typeErrors: number;
  items: Array<{
    number: number;
    raw: string;
    eventType?: string;
    matchedRuleId?: string;
    fields: Record<string, unknown>;
    warnings: Array<{ code: string; field?: string; message: string }>;
  }>;
}

export interface ReindexJob {
  id: string;
  sourceId: string;
  parserVersionId: string;
  status:
    "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";
  entriesProcessed: number;
  totalEntries: number;
  warnings: number;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
}

export interface ExtractionStatistics {
  totalEntries: number;
  matchedEntries: number;
  unmatchedEntries: number;
  warningEntries: number;
  coverage: number;
  byEventType: Record<string, number>;
}

export interface FieldStatistics extends FieldDefinition {
  presentEntries: number;
  totalEntries: number;
  coverage: number;
}

export interface TypedFilter {
  field: string;
  operator:
    "eq" | "ne" | "gt" | "gte" | "lt" | "lte" | "in" | "between" | "contains";
  value: unknown;
}

export interface StructuredQuery {
  filters: TypedFilter[];
  sort?: Array<{
    field: "timestamp" | "entry_number";
    direction: "asc" | "desc";
  }>;
  limit?: number;
}

export interface AggregationRequest {
  operation: "count" | "sum" | "avg" | "min" | "max";
  field?: string;
  groupBy?: string;
  filters: TypedFilter[];
}

export interface AggregationResult {
  operation: string;
  field?: string;
  groupBy?: string;
  buckets: Array<{ group?: unknown; value: number }>;
}

export interface DerivedField {
  id: string;
  parserProfileId: string;
  name: string;
  resultType: FieldType;
  expression: string;
  createdAt: string;
}

export interface SavedAnalysis {
  id: string;
  name: string;
  sourceId: string;
  configuration: Record<string, unknown>;
  chartType: "table" | "bar" | "line";
  createdAt: string;
  updatedAt: string;
}

interface APIErrorShape {
  error?: { message?: string };
}

const API_ROOT = import.meta.env.VITE_API_URL ?? "/api/v1";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_ROOT}${path}`, init);
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try {
      const payload = (await response.json()) as APIErrorShape;
      message = payload.error?.message ?? message;
    } catch {
      // The status remains useful when a reverse proxy returned non-JSON.
    }
    throw new Error(message);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}

export async function listSources(): Promise<Source[]> {
  const response = await request<{ items: Source[] }>("/sources/");
  return response.items;
}

export function getSource(id: string): Promise<Source> {
  return request(`/sources/${id}`);
}

export async function uploadSource(
  file: File,
  onProgress?: (fraction: number) => void,
): Promise<{ source: Source; detection: Detection }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_ROOT}/sources/upload`);
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress?.(event.loaded / event.total);
    };
    xhr.onerror = () => reject(new Error("The upload was interrupted"));
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(xhr.response as { source: Source; detection: Detection });
      } else {
        const payload = xhr.response as APIErrorShape | null;
        reject(
          new Error(payload?.error?.message ?? `Upload failed (${xhr.status})`),
        );
      }
    };
    const body = new FormData();
    body.append("file", file);
    xhr.send(body);
  });
}

export function importServerSource(relativePath: string): Promise<{
  source: Source;
  detection: Detection;
}> {
  return jsonRequest("/sources/import", "POST", { relativePath });
}

export function startImport(sourceId: string): Promise<ImportJob> {
  return request(`/sources/${sourceId}/imports`, { method: "POST" });
}

export function getImport(jobId: string): Promise<ImportJob> {
  return request(`/imports/${jobId}`);
}

export function cancelImport(jobId: string): Promise<void> {
  return request(`/imports/${jobId}/cancel`, { method: "POST" });
}

export function deleteSource(sourceId: string): Promise<void> {
  return request(`/sources/${sourceId}`, { method: "DELETE" });
}

export function getStatistics(sourceId: string): Promise<Statistics> {
  return request(`/sources/${sourceId}/statistics`);
}

export async function getTimeline(sourceId: string): Promise<TimelinePoint[]> {
  const response = await request<{ items: TimelinePoint[] }>(
    `/sources/${sourceId}/timeline`,
  );
  return response.items;
}

export function listEntries(
  sourceId: string,
  cursor: number,
  filters: EntryFilters,
  limit = 200,
): Promise<{
  items: LogEntry[];
  page: { nextCursor?: number; hasMore: boolean };
}> {
  const params = new URLSearchParams({
    cursor: String(cursor),
    limit: String(limit),
  });
  if (filters.query) params.set("query", filters.query);
  if (filters.levels?.length) params.set("levels", filters.levels.join(","));
  if (filters.services?.length)
    params.set("services", filters.services.join(","));
  if (filters.requestId) params.set("requestId", filters.requestId);
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  if (filters.eventTypes?.length)
    params.set("eventType", filters.eventTypes.join(","));
  return request(`/sources/${sourceId}/entries?${params}`);
}

export function importEventsURL(jobId: string): string {
  return `${API_ROOT}/imports/${jobId}/events`;
}

function jsonRequest<T>(
  path: string,
  method: "POST" | "PUT",
  body: unknown,
): Promise<T> {
  return request(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export async function listParserProfiles(): Promise<ParserProfile[]> {
  const response = await request<{ items: ParserProfile[] }>(
    "/parser-profiles/",
  );
  return response.items;
}

export function createParserProfile(input: {
  name: string;
  description?: string;
}): Promise<ParserProfile> {
  return jsonRequest("/parser-profiles/", "POST", input);
}

export function getParserProfile(id: string): Promise<{
  profile: ParserProfile;
  versions: ParserProfileVersion[];
}> {
  return request(`/parser-profiles/${id}`);
}

export function createParserVersion(
  profileId: string,
  input: {
    recordFormat: string;
    configuration?: Record<string, unknown>;
    rules: Array<{
      name: string;
      eventType: string;
      priority: number;
      matcherType: string;
      matcherConfig: Record<string, unknown>;
      enabled: boolean;
    }>;
    fields: Array<{
      name: string;
      displayName?: string;
      fieldType: FieldType;
      indexed: boolean;
      aggregatable: boolean;
      nullable: boolean;
    }>;
  },
): Promise<ParserProfileVersion> {
  return jsonRequest(`/parser-profiles/${profileId}/versions`, "POST", input);
}

export function previewParserProfile(
  profileId: string,
  sourceId: string,
  version?: number,
): Promise<PreviewResult> {
  return jsonRequest(`/parser-profiles/${profileId}/preview`, "POST", {
    sourceId,
    version,
    limit: 500,
  });
}

export function assignSourceParser(
  sourceId: string,
  profileId: string,
  version: number,
): Promise<Source> {
  return jsonRequest(`/sources/${sourceId}/parser`, "PUT", {
    profileId,
    version,
  });
}

export function startReindex(sourceId: string): Promise<ReindexJob> {
  return request(`/sources/${sourceId}/reindex`, { method: "POST" });
}

export function getReindex(sourceId: string): Promise<ReindexJob> {
  return request(`/sources/${sourceId}/reindex`);
}

export function cancelReindex(sourceId: string): Promise<void> {
  return request(`/sources/${sourceId}/reindex/cancel`, { method: "POST" });
}

export async function getSourceFields(
  sourceId: string,
): Promise<FieldStatistics[]> {
  const response = await request<{ items: FieldStatistics[] }>(
    `/sources/${sourceId}/fields`,
  );
  return response.items;
}

export function getExtractionStatistics(
  sourceId: string,
): Promise<ExtractionStatistics> {
  return request(`/sources/${sourceId}/extraction-statistics`);
}

export function querySource(
  sourceId: string,
  query: StructuredQuery,
): Promise<{ items: LogEntry[]; count: number }> {
  return jsonRequest(`/sources/${sourceId}/query`, "POST", query);
}

export function aggregateSource(
  sourceId: string,
  aggregation: AggregationRequest,
): Promise<AggregationResult> {
  return jsonRequest(`/sources/${sourceId}/aggregations`, "POST", aggregation);
}

export async function listDerivedFields(
  profileId: string,
): Promise<DerivedField[]> {
  const response = await request<{ items: DerivedField[] }>(
    `/parser-profiles/${profileId}/derived-fields`,
  );
  return response.items;
}

export function createDerivedField(
  profileId: string,
  field: Pick<DerivedField, "name" | "resultType" | "expression">,
): Promise<DerivedField> {
  return jsonRequest(
    `/parser-profiles/${profileId}/derived-fields`,
    "POST",
    field,
  );
}

export function deleteDerivedField(
  profileId: string,
  fieldId: string,
): Promise<void> {
  return request(`/parser-profiles/${profileId}/derived-fields/${fieldId}`, {
    method: "DELETE",
  });
}

export async function listSavedAnalyses(
  sourceId: string,
): Promise<SavedAnalysis[]> {
  const response = await request<{ items: SavedAnalysis[] }>(
    `/sources/${sourceId}/analyses`,
  );
  return response.items;
}

export function createSavedAnalysis(
  sourceId: string,
  analysis: Pick<SavedAnalysis, "name" | "configuration" | "chartType">,
): Promise<SavedAnalysis> {
  return jsonRequest(`/sources/${sourceId}/analyses`, "POST", analysis);
}

export function deleteSavedAnalysis(
  sourceId: string,
  analysisId: string,
): Promise<void> {
  return request(`/sources/${sourceId}/analyses/${analysisId}`, {
    method: "DELETE",
  });
}
