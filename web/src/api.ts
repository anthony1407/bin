// Client for bin's admin API (docs/admin-api.md). Session cookies travel
// automatically; state-changing requests add the CSRF token from the session.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    /** The whole error body, for endpoints that explain more than code and message. */
    public data: Record<string, unknown> = {},
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
    if (res.status === 401 && !path.startsWith("/login")) onUnauthorized();
    throw new ApiError(res.status, data.error ?? "error", data.message ?? res.statusText, data);
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
  /** The rule covers only the object whose key is `prefix`. */
  exact?: boolean;
}
export interface Policy {
  mode: "private" | "public" | "custom";
  rules?: Rule[];
}

export interface CORSRule {
  id?: string;
  allowedOrigins: string[];
  allowedMethods: string[];
  allowedHeaders?: string[];
  exposeHeaders?: string[];
  /** How long browsers may cache a preflight; missing sends no Max-Age. */
  maxAgeSeconds?: number;
}

export interface Bucket {
  name: string;
  createdAt: string;
  /** Stored versions, older ones included, when versioning is on. */
  objects: number;
  size: number;
  quotaBytes: number;
  policy: Policy;
  tags: Record<string, string>;
  /** "" (never versioned), "Enabled" or "Suspended". */
  versioning: "" | "Enabled" | "Suspended";
  cors: CORSRule[];
  /** Days deleted files stay in the trash; 0 keeps them until it is emptied. */
  trashDays: number;
}

export interface ObjectInfo {
  key: string;
  /** Missing for objects of buckets that were never versioned; names the delete marker in a listing of deleted objects. */
  versionId?: string;
  size: number;
  etag: string;
  lastModified: string;
  contentType?: string;
  headers?: Record<string, string>;
  metadata?: Record<string, string>;
  checksums?: Record<string, string>;
  partsCount?: number;
  /** Active share links, and until when the last one works. */
  shares?: number;
  shareExpiresAt?: string;
}

export interface ObjectVersion {
  versionId: string;
  isLatest: boolean;
  deleteMarker: boolean;
  size: number;
  etag?: string;
  lastModified: string;
}

export interface ShareLink {
  id: string;
  url?: string;
  /** Forces "save as" instead of opening in the browser. */
  download: boolean;
  createdBy?: string;
  createdAt: string;
  expiresAt: string;
}

// What a listed folder holds; lastModified is null for an empty folder.
export interface FolderInfo {
  objects: number;
  size: number;
  lastModified: string | null;
}

export interface ObjectPage {
  objects: ObjectInfo[];
  prefixes: string[];
  folders?: Record<string, FolderInfo>; // absent in search results

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
  /** From BIN_LOG_LEVEL / BIN_LOG_FORMAT at startup; empty on older servers. */
  logLevel?: string;
  logFormat?: string;
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
  corrupt: { bucket: string; key: string; versionId?: string }[];
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
  async loginLDAP(username: string, password: string) {
    const s = await request<Session>("POST", "/login/ldap", { username, password });
    csrfToken = s.csrfToken;
    return s;
  },
  loginProviders: () => request<LoginProviders>("GET", "/login/providers"),
  /** Server log lines after sequence number `after` (0: the latest ones). */
  logs: (after: number) => request<{ entries: LogEntry[]; latest: number }>("GET", `/logs?after=${after}&limit=500`),
  /** The browser navigates here to start an OpenID sign-in. */
  openIDLoginURL: (next: string) => `/api/v1/login/openid?next=${enc(next)}`,
  async session() {
    const s = await request<Session>("GET", "/session");
    csrfToken = s.csrfToken;
    return s;
  },

  openIDConfig: () => request<OpenIDConfig>("GET", "/identity/openid"),
  saveOpenIDConfig: (c: OpenIDConfig & { clientSecret?: string }) => request<OpenIDConfig>("PUT", "/identity/openid", c),
  testOpenID: (configUrl: string) =>
    request<{ issuer: string; authorizationEndpoint: string; scopesSupported?: string[]; claimsSupported?: string[] }>(
      "POST",
      "/identity/openid/test",
      { configUrl },
    ),
  ldapConfig: () => request<LDAPConfig>("GET", "/identity/ldap"),
  saveLDAPConfig: (c: LDAPConfig & { lookupBindPassword?: string }) => request<LDAPConfig>("PUT", "/identity/ldap", c),
  testLDAP: (c: LDAPConfig & { lookupBindPassword?: string }) => request<{ status: string }>("POST", "/identity/ldap/test", c),
  logout: () => request<void>("POST", "/logout"),
  info: () => request<Info>("GET", "/info"),

  buckets: () => request<{ buckets: Bucket[] }>("GET", "/buckets").then((r) => r.buckets),
  bucket: (name: string) => request<Bucket>("GET", `/buckets/${enc(name)}`),
  createBucket: (name: string, policy?: Policy) => request<Bucket>("POST", "/buckets", { name, policy }),
  deleteBucket: (name: string, force: boolean) =>
    request<void>("DELETE", `/buckets/${enc(name)}${force ? "?force=true" : ""}`),
  setTags: (name: string, tags: Record<string, string>) => request<Bucket>("PUT", `/buckets/${enc(name)}/tags`, { tags }),
  setQuota: (name: string, bytes: number) => request<Bucket>("PUT", `/buckets/${enc(name)}/quota`, { bytes }),
  setPolicy: (name: string, policy: Policy) => request<Bucket>("PUT", `/buckets/${enc(name)}/policy`, policy),
  setVersioning: (name: string, status: "Enabled" | "Suspended") =>
    request<Bucket>("PUT", `/buckets/${enc(name)}/versioning`, { status }),
  /** Replaces the CORS rules; an empty list removes them. */
  setCORS: (name: string, rules: CORSRule[]) => request<Bucket>("PUT", `/buckets/${enc(name)}/cors`, { rules }),

  // delimiter defaults to "/" on the server; "" lists every key under prefix.
  objects: (bucket: string, opts: { prefix?: string; delimiter?: string; after?: string; search?: string; limit?: number; deleted?: boolean }) => {
    const q = new URLSearchParams();
    if (opts.prefix) q.set("prefix", opts.prefix);
    if (opts.delimiter !== undefined) q.set("delimiter", opts.delimiter);
    if (opts.after) q.set("after", opts.after);
    if (opts.search) q.set("search", opts.search);
    if (opts.limit) q.set("limit", String(opts.limit));
    if (opts.deleted) q.set("deleted", "true");
    return request<ObjectPage>("GET", `/buckets/${enc(bucket)}/objects?${q}`);
  },
  stat: (bucket: string, key: string, versionId?: string) =>
    request<ObjectInfo>("GET", `/buckets/${enc(bucket)}/object?key=${enc(key)}${versionId ? `&versionId=${enc(versionId)}` : ""}`),
  versions: (bucket: string, key: string) =>
    request<{ versions: ObjectVersion[]; truncated: boolean }>("GET", `/buckets/${enc(bucket)}/versions?key=${enc(key)}`),
  /** Removes one version for good; removing a delete marker brings the object back. */
  deleteVersion: (bucket: string, key: string, versionId: string) =>
    request<{ versionId: string; deleteMarker: boolean }>("DELETE", `/buckets/${enc(bucket)}/versions?key=${enc(key)}&versionId=${enc(versionId)}`),
  /** Makes an older version current again, as a new version. */
  restoreVersion: (bucket: string, key: string, versionId: string) =>
    request<ObjectInfo>("POST", `/buckets/${enc(bucket)}/versions/restore`, { key, versionId }),
  deleteObjects: (bucket: string, keys: string[], prefixes: string[]) =>
    request<{ deleted: number }>("DELETE", `/buckets/${enc(bucket)}/objects`, { keys, prefixes }),
  /** Moves objects and folders (prefixes) to the bucket's trash, server side. */
  trashObjects: (bucket: string, keys: string[], prefixes: string[]) =>
    request<{ moved: number }>("POST", `/buckets/${enc(bucket)}/trash`, { keys, prefixes }),
  /** Moves trashed objects and folders back to where they were deleted from. */
  restoreObjects: (bucket: string, keys: string[], prefixes: string[]) =>
    request<{ moved: number }>("POST", `/buckets/${enc(bucket)}/trash/restore`, { keys, prefixes }),
  setTrashRetention: (bucket: string, days: number) =>
    request<Bucket>("PUT", `/buckets/${enc(bucket)}/trash/retention`, { days }),
  createFolder: (bucket: string, prefix: string) => request<ObjectInfo>("POST", `/buckets/${enc(bucket)}/folders`, { prefix }),
  share: (bucket: string, key: string, expiresIn: number, download: boolean) =>
    request<{ id: string; url: string; expiresAt: string }>("POST", `/buckets/${enc(bucket)}/share`, { key, expiresIn, download }),
  shareLinks: (bucket: string, key: string) =>
    request<{ links: ShareLink[] }>("GET", `/buckets/${enc(bucket)}/shares?key=${enc(key)}`).then((r) => r.links),
  revokeShare: (id: string) => request<void>("DELETE", `/shares/${enc(id)}`),
  downloadURL: (bucket: string, key: string, inline = false, versionId?: string) =>
    `/api/v1/buckets/${enc(bucket)}/download?key=${enc(key)}${inline ? "&inline=1" : ""}${versionId ? `&versionId=${enc(versionId)}` : ""}`,

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

  // Import from remote S3
  /** With an empty secretKey, source names a saved import whose stored secret is used. */
  importTest: (endpoint: string, accessKey: string, secretKey: string, region: string, source?: string) =>
    request<{ buckets: RemoteBucket[]; iam: ImportIAMSummary }>("POST", "/import/test", {
      endpoint,
      accessKey,
      secretKey,
      region,
      source,
    }),
  importSources: () => request<{ imports: ImportSource[] }>("GET", "/import/sources").then((r) => r.imports),
  createImportSource: (input: ImportSourceInput) => request<ImportSource>("POST", "/import/sources", input),
  /** An empty secretKey keeps the stored one. */
  updateImportSource: (id: string, input: ImportSourceInput) => request<ImportSource>("PUT", `/import/sources/${enc(id)}`, input),
  deleteImportSource: (id: string) => request<void>("DELETE", `/import/sources/${enc(id)}`),
  /** Runs a saved import: new buckets are created, existing ones get what is new. */
  runImportSource: (id: string) =>
    request<{ jobs: ImportJob[]; skipped: ImportSkipped[]; iam?: ImportIAMResult; iamError?: string }>(
      "POST",
      `/import/sources/${enc(id)}/run`,
    ),
  /**
   * settings copies each bucket's configuration; iam recreates the remote's
   * users, groups, policies and access keys (MinIO). Throws ApiError
   * "nothing_imported" (with `skipped` in its data) when no bucket could start.
   */
  importStart: (
    endpoint: string,
    accessKey: string,
    secretKey: string,
    region: string,
    buckets: string[],
    options: { settings: boolean; iam: boolean },
  ) =>
    request<{ jobs: ImportJob[]; skipped: ImportSkipped[]; iam?: ImportIAMResult; iamError?: string }>("POST", "/import/start", {
      endpoint,
      accessKey,
      secretKey,
      region,
      buckets,
      ...options,
    }),
  importJobs: () => request<{ jobs: ImportJob[] }>("GET", "/import/jobs").then((r) => r.jobs),

  replicationRules: () => request<{ rules: ReplicationRule[] }>("GET", "/replication/rules").then((r) => r.rules),
  createReplicationRule: (rule: ReplicationRuleInput) => request<ReplicationRule>("POST", "/replication/rules", rule),
  updateReplicationRule: (id: string, rule: ReplicationRuleInput) =>
    request<ReplicationRule>("PUT", `/replication/rules/${enc(id)}`, rule),
  deleteReplicationRule: (id: string) => request<void>("DELETE", `/replication/rules/${enc(id)}`),
  runReplicationRule: (id: string) => request<void>("POST", `/replication/rules/${enc(id)}/run`),
  cancelImport: (jobId: string) => request<void>("DELETE", `/import/jobs/${enc(jobId)}`),

  dropzones: () => request<{ dropzones: Dropzone[] }>("GET", "/dropzones").then((r) => r.dropzones),
  createDropzone: (body: { bucket: string; prefix: string; note: string; expiresIn: number; maxBytes: number }) =>
    request<Dropzone>("POST", "/dropzones", body),
  deleteDropzone: (id: string) => request<void>("DELETE", `/dropzones/${enc(id)}`),
  dropzoneUploads: (id: string) =>
    request<{ uploads: DropzoneUpload[] }>("GET", `/dropzones/${enc(id)}/uploads`).then((r) => r.uploads),
  /** What a dropzone link allows, for its public upload page (no session). */
  publicDropzone: async (token: string): Promise<PublicDropzone> => {
    const res = await fetch("/api/v1/public/dropzone", { headers: { "X-Dropzone-Token": token } });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new ApiError(res.status, data.error ?? "error", data.message ?? res.statusText, data);
    return data;
  },
};

export interface LogEntry {
  seq: number;
  time: string;
  level: "DEBUG" | "INFO" | "WARN" | "ERROR" | string;
  message: string;
  /** The line's key=value pairs, as in the text log. */
  attrs?: string;
}

export interface LoginProviders {
  openid: { displayName: string } | null;
  ldap: boolean;
}

export interface OpenIDConfig {
  enabled: boolean;
  configUrl: string;
  clientId: string;
  /** Read only: whether a client secret is stored (it is never sent back). */
  clientSecretSet?: boolean;
  claimName: string;
  adminValue: string;
  displayName: string;
  scopes: string;
  redirectUri: string;
  /** Read only: what to register at the provider when redirectUri is empty. */
  defaultRedirectUri?: string;
}

export interface LDAPConfig {
  enabled: boolean;
  serverAddr: string;
  tlsMode: "tls" | "starttls" | "none";
  tlsSkipVerify: boolean;
  lookupBindDn: string;
  /** Read only: whether a lookup password is stored (it is never sent back). */
  lookupBindPasswordSet?: boolean;
  userDnSearchBase: string;
  userDnSearchFilter: string;
  groupSearchBase: string;
  groupSearchFilter: string;
  adminGroupDn: string;
}

export interface Dropzone {
  id: string;
  bucket: string;
  prefix: string;
  note: string;
  maxBytes: number;
  createdAt: string;
  expiresAt: string;
  expired: boolean;
  /** The public upload page; the token is after "#". */
  url?: string;
  /** What was uploaded through the link. */
  uploads: number;
  uploadedBytes: number;
  lastUploadAt?: string;
}

export interface DropzoneUpload {
  /** Full object key, folder included. */
  key: string;
  /** The key without the link's folder. */
  name: string;
  size: number;
  uploadedAt: string;
  /** Uploader IP. */
  remote?: string;
  /** Uploader browser, as it identified itself. */
  userAgent?: string;
}

export interface PublicDropzone {
  note: string;
  maxBytes: number;
  expiresAt: string;
}

/** What the replication form sends; secretKey empty keeps the stored one. */
export interface ReplicationRuleInput {
  name: string;
  direction: "push" | "pull";
  bucket: string;
  prefix: string;
  endpoint: string;
  accessKey: string;
  secretKey?: string;
  region: string;
  remoteBucket: string;
  remotePrefix: string;
  intervalSeconds: number;
  mirrorDeletes: boolean;
  enabled: boolean;
}

export interface ReplicationRule extends ReplicationRuleInput {
  id: string;
  secretKeySet: boolean;
  createdAt: string;
  running: boolean;
  lastRunAt?: string;
  nextRunAt?: string;
  lastStatus?: "ok" | "error";
  lastError?: string;
  lastCopied: number;
  lastDeleted: number;
  lastFailed: number;
  lastBytes: number;
  totalCopied: number;
  totalBytes: number;
}

export interface RemoteBucket {
  name: string;
  size: number;
  objects: number;
  /** Too large to count fully: size and objects cover the first objects only. */
  partial?: boolean;
}

export interface ImportJob {
  jobId: string;
  bucket: string;
  endpoint: string;
  /** The saved import it was run from. */
  source?: string;
  state: "queued" | "running" | "done" | "failed" | "canceled";
  copied: number;
  failed: number;
  bytes: number;
  /** The bucket existed here: only new and changed objects are copied. */
  update: boolean;
  unchanged: number;
  error?: string;
  /** Bucket settings copied, and notes on those that could not be. */
  settings?: string[];
  notes?: string[];
  startedAt: string;
  finishedAt?: string;
}

export interface ImportSkipped {
  bucket: string;
  reason: string;
}

/** A saved import: a remote server and what to import from it, run again on demand. */
export interface ImportSource {
  id: string;
  name: string;
  endpoint: string;
  accessKey: string;
  secretKeySet: boolean;
  region: string;
  buckets: string[];
  settings: boolean;
  iam: boolean;
  createdAt: string;
  lastRunAt: string | null;
  /** Its copies started since bin last restarted, newest first. */
  jobs: ImportJob[];
}

export interface ImportSourceInput {
  name: string;
  endpoint: string;
  accessKey: string;
  secretKey: string;
  region: string;
  buckets: string[];
  settings: boolean;
  iam: boolean;
}

/** What the connection test found of the remote's users and policies. */
export interface ImportIAMSummary {
  available: boolean;
  reason?: string;
  users: number;
  groups: number;
  policies: number;
  accessKeys: number;
}

/** An access key created from a MinIO user or access key, with its new secret (shown once). */
export interface ImportedKey {
  accessKey: string;
  secretKey: string;
  kind: "user" | "accessKey";
  parent?: string;
  enabled: boolean;
  groups: string[];
  note?: string;
}

export interface ImportIAMResult {
  keys: ImportedKey[];
  groups: { name: string; from: "policy" | "group"; source: string; buckets: string[]; readOnly: boolean; note?: string }[];
  skipped: { kind: "policy" | "group" | "user" | "accessKey"; name: string; reason: string }[];
}

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

/** Downloads an object (the version it names, if any) to the user's disk, reporting the bytes received. */
export async function downloadObject(bucket: string, object: ObjectInfo, onProgress: (received: number) => void): Promise<void> {
  const url = api.downloadURL(bucket, object.key, false, object.versionId);
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
function put(
  url: string,
  body: Blob,
  contentType: string,
  onProgress: (sent: number) => void,
  signal: AbortSignal,
  headers: Record<string, string> = {},
): Promise<any> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    xhr.setRequestHeader("X-CSRF-Token", csrfToken);
    if (contentType) xhr.setRequestHeader("Content-Type", contentType);
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v);
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
 * Uploads a file through a dropzone link, without a session. Resolves to the
 * name it was stored under, which gets a number when the name was taken.
 */
export async function dropzoneUpload(
  token: string,
  file: File,
  onProgress: (sent: number) => void,
  signal: AbortSignal,
): Promise<string> {
  const res = await put(`/api/v1/public/dropzone/files?name=${enc(file.name)}`, file, file.type, onProgress, signal, {
    "X-Dropzone-Token": token,
  });
  return res.name as string;
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
