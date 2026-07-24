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
  format: "jsonl" | "plain_text";
  status: SourceStatus;
  sizeBytes: number;
  entryCount: number;
  parsedCount: number;
  failedCount: number;
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
  status: "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";
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
        reject(new Error(payload?.error?.message ?? `Upload failed (${xhr.status})`));
      }
    };
    const body = new FormData();
    body.append("file", file);
    xhr.send(body);
  });
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
  const response = await request<{ items: TimelinePoint[] }>(`/sources/${sourceId}/timeline`);
  return response.items;
}

export function listEntries(
  sourceId: string,
  cursor: number,
  filters: EntryFilters,
  limit = 200,
): Promise<{ items: LogEntry[]; page: { nextCursor?: number; hasMore: boolean } }> {
  const params = new URLSearchParams({ cursor: String(cursor), limit: String(limit) });
  if (filters.query) params.set("query", filters.query);
  if (filters.levels?.length) params.set("levels", filters.levels.join(","));
  if (filters.services?.length) params.set("services", filters.services.join(","));
  if (filters.requestId) params.set("requestId", filters.requestId);
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  return request(`/sources/${sourceId}/entries?${params}`);
}

export function importEventsURL(jobId: string): string {
  return `${API_ROOT}/imports/${jobId}/events`;
}

