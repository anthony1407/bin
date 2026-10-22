// Client for bin's admin API (docs/admin-api.md). Session cookies travel
// automatically; state-changing requests add the CSRF token from the session.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

let csrfToken = "";
let onUnauthorized: () => void = () => {};

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== "GET" && method !== "HEAD") headers["X-CSRF-Token"] = csrfToken;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch("/api/v1" + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && path !== "/login") onUnauthorized();
    throw new ApiError(res.status, data.error ?? "error", data.message ?? res.statusText);
  }
  return data as T;
}

// --- types ----------------------------------------------------------------------

export interface Session {
  user: string;
  csrfToken: string;
  expiresAt: string;
}

export type Access = "readonly" | "writeonly" | "readwrite";
export interface Rule {
  prefix: string;
  access: Access;
}
export interface Policy {
  mode: "private" | "public" | "custom";
  rules?: Rule[];
}

export interface Bucket {
  name: string;
  createdAt: string;
  objects: number;
  size: number;
  quotaBytes: number;
  policy: Policy;
  tags: Record<string, string>;
  versioning: string;
}

export interface ObjectInfo {
  key: string;
  size: number;
  etag: string;
  lastModified: string;
  contentType?: string;
  headers?: Record<string, string>;
  metadata?: Record<string, string>;
  checksums?: Record<string, string>;
  partsCount?: number;
}

export interface ObjectPage {
  objects: ObjectInfo[];
  prefixes: string[];
  truncated: boolean;
  nextAfter?: string;
}

export interface Info {
  version: string;
  commit: string;
  build: string;
  region: string;
  dataDir: string;
  s3Endpoint: string;
  s3Addr?: string;
  publicUrl?: string;
  logRequests?: boolean;
  isTLS?: boolean;
  startedAt: string;
  uptime: number;
  buckets: number;
  objects: number;
  bytes: number;
  disk: { total?: number; free?: number };
}

export interface AccessKey {
  accessKey: string;
  secretKey?: string;
  name: string;
  enabled: boolean;
  createdAt: string;
  expiresAt: string | null;
  lastUsedAt: string | null;
  expired: boolean;
  buckets: string[];
  readOnly: boolean;
  /** When non-empty, these groups decide the key's access instead of buckets/readOnly. */
  groups: string[];
}

export interface Group {
  name: string;
  description: string;
  enabled: boolean;
  createdAt: string;
  buckets: string[];
  readOnly: boolean;
  members: string[];
}

export interface AuditEntry {
  id: number;
  at: string;
  source: "console" | "s3";
  actor: string;
  action: string;
  bucket?: string;
  resource?: string;
  status: number;
  error?: string;
  remote?: string;
  requestId?: string;
}

export interface AuditFilter {
  source?: "" | "console" | "s3";
  q?: string;
  failed?: boolean;
  before?: number;
  limit?: number;
}

export interface Settings {
  shareMaxExpirySeconds: number;
  auditRetentionDays: number;
  publicUrl: string;
  region: string;
}

export interface ScrubStatus {
  state: "idle" | "running" | "done" | "failed";
  buckets?: string[]; // missing: all buckets
  startedAt?: string;
  finishedAt?: string;
  objects: number;
  bytes: number;
  corrupt: { bucket: string; key: string }[];
  error?: string;
}

// --- endpoints --------------------------------------------------------------------

const enc = encodeURIComponent;

export const api = {
  async login(accessKey: string, secretKey: string) {
    const s = await request<Session>("POST", "/login", { accessKey, secretKey });
    csrfToken = s.csrfToken;
    return s;
  },
  async session() {
    const s = await request<Session>("GET", "/session");
    csrfToken = s.csrfToken;
    return s;
  },
  logout: () => request<void>("POST", "/logout"),
  info: () => request<Info>("GET", "/info"),

  buckets: () => request<{ buckets: Bucket[] }>("GET", "/buckets").then((r) => r.buckets),
  bucket: (name: string) => request<Bucket>("GET", `/buckets/${enc(name)}`),
  createBucket: (name: string) => request<Bucket>("POST", "/buckets", { name }),
  deleteBucket: (name: string, force: boolean) =>
    request<void>("DELETE", `/buckets/${enc(name)}${force ? "?force=true" : ""}`),
  setTags: (name: string, tags: Record<string, string>) => request<Bucket>("PUT", `/buckets/${enc(name)}/tags`, { tags }),
  setQuota: (name: string, bytes: number) => request<Bucket>("PUT", `/buckets/${enc(name)}/quota`, { bytes }),
  setPolicy: (name: string, policy: Policy) => request<Bucket>("PUT", `/buckets/${enc(name)}/policy`, policy),

  objects: (bucket: string, opts: { prefix?: string; after?: string; search?: string; limit?: number }) => {
    const q = new URLSearchParams();
    if (opts.prefix) q.set("prefix", opts.prefix);
    if (opts.after) q.set("after", opts.after);
    if (opts.search) q.set("search", opts.search);
    if (opts.limit) q.set("limit", String(opts.limit));
    return request<ObjectPage>("GET", `/buckets/${enc(bucket)}/objects?${q}`);
  },
  stat: (bucket: string, key: string) => request<ObjectInfo>("GET", `/buckets/${enc(bucket)}/object?key=${enc(key)}`),
  deleteObjects: (bucket: string, keys: string[], prefixes: string[]) =>
    request<{ deleted: number }>("DELETE", `/buckets/${enc(bucket)}/objects`, { keys, prefixes }),
  createFolder: (bucket: string, prefix: string) => request<ObjectInfo>("POST", `/buckets/${enc(bucket)}/folders`, { prefix }),
  share: (bucket: string, key: string, expiresIn: number, download: boolean) =>
    request<{ url: string; expiresAt: string }>("POST", `/buckets/${enc(bucket)}/share`, { key, expiresIn, download }),
  downloadURL: (bucket: string, key: string, inline = false) =>
    `/api/v1/buckets/${enc(bucket)}/download?key=${enc(key)}${inline ? "&inline=1" : ""}`,

  accessKeys: () => request<{ accessKeys: AccessKey[] }>("GET", "/access-keys").then((r) => r.accessKeys),
  createAccessKey: (body: { name: string; expiresAt?: string; buckets?: string[]; readOnly?: boolean; groups?: string[] }) =>
    request<AccessKey>("POST", "/access-keys", body),
  updateAccessKey: (
    key: string,
    body: Partial<{ name: string; enabled: boolean; expiresAt: string; noExpiry: boolean; buckets: string[]; readOnly: boolean; groups: string[] }>,
  ) => request<AccessKey>("PATCH", `/access-keys/${enc(key)}`, body),
  deleteAccessKey: (key: string) => request<void>("DELETE", `/access-keys/${enc(key)}`),

  groups: () => request<{ groups: Group[] }>("GET", "/groups").then((r) => r.groups),
  createGroup: (body: { name: string; description?: string; buckets?: string[]; readOnly?: boolean; members?: string[] }) =>
    request<Group>("POST", "/groups", body),
  updateGroup: (name: string, body: Partial<{ description: string; enabled: boolean; buckets: string[]; readOnly: boolean; members: string[] }>) =>
    request<Group>("PATCH", `/groups/${enc(name)}`, body),
  deleteGroup: (name: string) => request<void>("DELETE", `/groups/${enc(name)}`),

  audit: (f: AuditFilter) => {
    const q = new URLSearchParams();
    if (f.source) q.set("source", f.source);
    if (f.q) q.set("q", f.q);
    if (f.failed) q.set("failed", "true");
    if (f.before) q.set("before", String(f.before));
    if (f.limit) q.set("limit", String(f.limit));
    return request<{ entries: AuditEntry[]; nextBefore?: number }>("GET", `/audit?${q}`);
  },

  settings: () => request<Settings>("GET", "/settings"),
  saveSettings: (shareMaxExpirySeconds: number) => request<Settings>("PUT", "/settings", { shareMaxExpirySeconds }),
  saveAuditRetention: (auditRetentionDays: number) => request<Settings>("PUT", "/settings", { auditRetentionDays }),
  revokeShareLinks: () => request<void>("POST", "/settings/revoke-share-links"),
  scrub: () => request<ScrubStatus>("GET", "/scrub"),
  /** Scans the given buckets, or all of them when the list is empty. */
  startScrub: (buckets: string[] = []) => request<ScrubStatus>("POST", "/scrub", { buckets }),

  speedtestPing: () => request<void>("GET", "/speedtest/ping"),
  speedtestDownloadURL: (sizeBytes: number) => `/api/v1/speedtest/download?size=${sizeBytes}`,
  speedtestUpload: (body: Blob, onProgress?: (sent: number) => void, signal?: AbortSignal) => {
    return new Promise<{ bytes: number; duration: number; mbps: number; mBps: number }>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/v1/speedtest/upload");
      xhr.setRequestHeader("X-CSRF-Token", csrfToken);
      if (onProgress) xhr.upload.onprogress = (e) => onProgress(e.loaded);
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch {
            resolve({ bytes: body.size, duration: 1, mbps: 0, mBps: 0 });
          }
        } else {
          reject(new Error("Speedtest upload failed"));
        }
      };
      xhr.onerror = () => reject(new Error("Network error"));
      if (signal) signal.addEventListener("abort", () => xhr.abort());
      xhr.send(body);
    });
  },
};

// --- downloads ------------------------------------------------------------------------

// Up to this size a download goes through fetch, which reports progress and
// errors (an integrity failure aborts the body). Bigger files use the
// browser's own download, which streams to disk instead of into memory.
const FETCH_DOWNLOAD_MAX = 256 * 1024 * 1024;

function saveAs(href: string, name: string) {
  const a = document.createElement("a");
  a.href = href;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
}

/** Downloads an object to the user's disk, reporting the bytes received. */
export async function downloadObject(bucket: string, object: ObjectInfo, onProgress: (received: number) => void): Promise<void> {
  const url = api.downloadURL(bucket, object.key);
  const name = object.key.slice(object.key.lastIndexOf("/") + 1) || "download";
  if (object.size > FETCH_DOWNLOAD_MAX) {
    saveAs(url, name);
    return;
  }
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    if (res.status === 401) onUnauthorized();
    throw new ApiError(res.status, data.error ?? "error", data.message ?? "Download failed.");
  }
  const reader = res.body!.getReader();
  const chunks: Uint8Array[] = [];
  let received = 0;
  for (;;) {
    let step: ReadableStreamReadResult<Uint8Array>;
    try {
      step = await reader.read();
    } catch {
      throw new ApiError(0, "interrupted", "The download was interrupted; the object may be damaged. Run an integrity scan in Settings.");
    }
    if (step.done) break;
    chunks.push(step.value);
    received += step.value.length;
    onProgress(received);
  }
  const href = URL.createObjectURL(new Blob(chunks as BlobPart[], { type: object.contentType || "application/octet-stream" }));
  saveAs(href, name);
  setTimeout(() => URL.revokeObjectURL(href), 60_000);
}

// --- uploads --------------------------------------------------------------------------

const PART_SIZE = 16 * 1024 * 1024;
const SINGLE_MAX = 64 * 1024 * 1024;

/** Sends one body with upload progress (fetch cannot report it). */
function put(url: string, body: Blob, contentType: string, onProgress: (sent: number) => void, signal: AbortSignal): Promise<any> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    xhr.setRequestHeader("X-CSRF-Token", csrfToken);
    if (contentType) xhr.setRequestHeader("Content-Type", contentType);
    xhr.upload.onprogress = (e) => onProgress(e.loaded);
    xhr.onload = () => {
      const data = (() => {
        try {
          return JSON.parse(xhr.responseText);
        } catch {
          return {};
        }
      })();
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else reject(new ApiError(xhr.status, data.error ?? "error", data.message ?? "Upload failed"));
    };
    xhr.onerror = () => reject(new ApiError(0, "network", "The connection was lost during the upload."));
    xhr.onabort = () => reject(new ApiError(0, "aborted", "Upload cancelled."));
    signal.addEventListener("abort", () => xhr.abort());
    xhr.send(body);
  });
}

/**
 * Uploads a file: one request up to 64 MiB, otherwise 16 MiB parts sent four
 * at a time through the multipart endpoints (needed beyond 5 GiB).
 */
export async function uploadFile(
  bucket: string,
  key: string,
  file: File,
  onProgress: (sent: number) => void,
  signal: AbortSignal,
): Promise<ObjectInfo> {
  const base = `/api/v1/buckets/${enc(bucket)}`;
  if (file.size <= SINGLE_MAX) {
    return put(`${base}/upload?key=${enc(key)}`, file, file.type, onProgress, signal);
  }
  const { uploadId } = await request<{ uploadId: string }>("POST", `/buckets/${enc(bucket)}/uploads?key=${enc(key)}`);
  const count = Math.ceil(file.size / PART_SIZE);
  const sent = new Array(count).fill(0);
  const etags: { number: number; etag: string }[] = [];
  let next = 0;
  const worker = async () => {
    while (next < count && !signal.aborted) {
      const i = next++;
      const blob = file.slice(i * PART_SIZE, Math.min(file.size, (i + 1) * PART_SIZE));
      const res = await put(`${base}/uploads/${enc(uploadId)}/parts/${i + 1}?key=${enc(key)}`, blob, "", (n) => {
        sent[i] = n;
        onProgress(sent.reduce((a, b) => a + b, 0));
      }, signal);
      etags[i] = { number: i + 1, etag: res.etag };
    }
  };
  try {
    await Promise.all(Array.from({ length: Math.min(4, count) }, worker));
    if (signal.aborted) throw new ApiError(0, "aborted", "Upload cancelled.");
    return await request<ObjectInfo>("POST", `/buckets/${enc(bucket)}/uploads/${enc(uploadId)}/complete?key=${enc(key)}`, { parts: etags });
  } catch (e) {
    request("DELETE", `/buckets/${enc(bucket)}/uploads/${enc(uploadId)}?key=${enc(key)}`).catch(() => {});
    throw e;
  }
}
