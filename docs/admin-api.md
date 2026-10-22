# Admin API

The console listener (default `:9001`) serves the web panel and a JSON API
under `/api/v1`. The API is separate from the S3 API and uses its own
authentication: a session cookie obtained by signing in with the root
credentials.

## Authentication

| Step | Request | Notes |
|---|---|---|
| Sign in | `POST /api/v1/login` `{"accessKey","secretKey"}` | Root credentials only. Sets the `bin_session` cookie (HttpOnly, SameSite=Strict, Secure over HTTPS) and returns `{"user","csrfToken","expiresAt"}`. |
| Current session | `GET /api/v1/session` | Returns the same body; use it to recover the CSRF token after a page reload. |
| Sign out | `POST /api/v1/logout` | Ends the session. |

Rules for every other endpoint:

- Without a valid session: `401 {"error":"not_signed_in"}` or `"session_expired"`.
- Requests other than GET/HEAD must send `X-CSRF-Token: <csrfToken>`, or get
  `403 bad_csrf_token`.
- A browser `Origin` header must match the host, or `403 bad_origin`.
- After 10 failed sign-ins from one IP within 15 minutes, `POST /login`
  answers `429` with `Retry-After`.
- Sessions last 12 hours.

Errors always look like `{"error": "<code>", "message": "<human text>"}`.

## Endpoints

### Server

| Method and path | Body / query | Response |
|---|---|---|
| `GET /api/v1/health` | (no session needed) | `{"status":"ok","version","commit"}` |
| `GET /api/v1/info` | | version, region, `s3Endpoint`, uptime, totals (`buckets`, `objects`, `bytes`), `disk {total, free}` |
| `GET /api/v1/settings` | | `{"shareMaxExpirySeconds","auditRetentionDays","publicUrl","region"}` |
| `PUT /api/v1/settings` | any of `{"shareMaxExpirySeconds": 60..604800, "auditRetentionDays": 0..3650}` (0 days: keep forever) | settings |
| `POST /api/v1/settings/revoke-share-links` | | `204`; every share link issued so far stops working |
| `POST /api/v1/scrub` | `{"buckets": [...]}` (empty or missing: all buckets) | starts an integrity scan in the background; `404` for an unknown bucket, `409` if one is running |
| `GET /api/v1/scrub` | | `{"state": idle/running/done/failed, "buckets", "objects", "bytes", "corrupt": [{"bucket","key"}]}` |

### Buckets

| Method and path | Body / query | Response |
|---|---|---|
| `GET /api/v1/buckets` | | `{"buckets": [bucket]}` |
| `POST /api/v1/buckets` | `{"name"}` | `201` bucket |
| `GET /api/v1/buckets/{bucket}` | | bucket |
| `DELETE /api/v1/buckets/{bucket}` | `?force=true` deletes the content first | `204` |
| `PUT /api/v1/buckets/{bucket}/tags` | `{"tags": {"k":"v"}}` (replaces all; `{}` removes) | bucket |
| `PUT /api/v1/buckets/{bucket}/quota` | `{"bytes": n}` (0 = unlimited) | bucket |
| `PUT /api/v1/buckets/{bucket}/policy` | policy (below) | bucket |

A bucket is
`{"name","createdAt","objects","size","quotaBytes","policy","tags","versioning"}`.

A policy controls anonymous (unsigned) S3 access:

```json
{"mode": "private"}
{"mode": "public"}
{"mode": "custom", "rules": [
  {"prefix": "shared/", "access": "readonly"},
  {"prefix": "inbox/",  "access": "writeonly"},
  {"prefix": "tmp/",    "access": "readwrite"}
]}
```

`readonly` allows GET/HEAD of keys under the prefix and listing with that
prefix; `writeonly` allows uploads under it; `readwrite` allows both plus
deletes. `public` is `readonly` on everything. Anonymous callers can never
list buckets, copy, or bulk-delete.

### Objects

Object keys travel in the `key` query parameter (URL-encoded).

| Method and path | Body / query | Response |
|---|---|---|
| `GET /api/v1/buckets/{bucket}/objects` | `prefix`, `delimiter` (default `/`), `after`, `limit` (≤1000), or `search` | `{"objects": [object], "prefixes": [...], "truncated", "nextAfter"}` |
| `GET /api/v1/buckets/{bucket}/object?key=` | | object with headers, metadata and checksums |
| `GET /api/v1/buckets/{bucket}/download?key=` | `inline=1` to preview; `Range` supported | the bytes (sandboxed by CSP) |
| `PUT /api/v1/buckets/{bucket}/upload?key=` | raw body, `Content-Length` required, up to 5 GiB | `201` object |
| `POST /api/v1/buckets/{bucket}/folders` | `{"prefix"}` | `201`; creates the empty `prefix/` object |
| `DELETE /api/v1/buckets/{bucket}/objects` | `{"keys": [...], "prefixes": [...]}` | `{"deleted": n}` |
| `POST /api/v1/buckets/{bucket}/share` | `{"key", "expiresIn": seconds, "download": bool}` | `{"url", "expiresAt"}` |
| `POST /api/v1/buckets/{bucket}/uploads?key=` | | `{"uploadId"}` (files over 5 GiB) |
| `PUT /api/v1/buckets/{bucket}/uploads/{id}/parts/{n}?key=` | raw body, ≥ 5 MiB except the last | `{"number","etag","size"}` |
| `POST /api/v1/buckets/{bucket}/uploads/{id}/complete?key=` | `{"parts": [{"number","etag"}]}` | object |
| `DELETE /api/v1/buckets/{bucket}/uploads/{id}?key=` | | `204` |

Share links are presigned S3 URLs signed with an internal read-only key
(never the root key). Their base URL is `BIN_PUBLIC_URL`, or else the host
the console was reached on with the S3 port.

### Access keys

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/access-keys` | | `{"accessKeys": [key]}` (never secrets) |
| `POST /api/v1/access-keys` | `{"name", "expiresAt"?, "buckets"?: [...], "readOnly"?, "groups"?: [...]}` | `201` key **with `secretKey`**, shown only this once |
| `PATCH /api/v1/access-keys/{key}` | any of `name`, `enabled`, `expiresAt`, `noExpiry`, `buckets`, `readOnly`, `groups` (replaces the key's groups) | key |
| `DELETE /api/v1/access-keys/{key}` | | `204` |

A key is
`{"accessKey","name","enabled","createdAt","expiresAt","lastUsedAt","expired","buckets","readOnly","groups"}`.
An empty `buckets` list means every bucket. Secrets are stored encrypted with
the master key (`BIN_MASTER_KEY`, or `.bin/master.key` in the data directory).

### Groups

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/groups` | | `{"groups": [group]}` |
| `POST /api/v1/groups` | `{"name", "description"?, "buckets"?: [...], "readOnly"?, "members"?: [access keys]}` | `201` group |
| `PATCH /api/v1/groups/{name}` | any of `description`, `enabled`, `buckets`, `readOnly`, `members` (replaces the list) | group |
| `DELETE /api/v1/groups/{name}` | | `204` |

A group is
`{"name","description","enabled","createdAt","buckets","readOnly","members"}`.
Names use 1-64 letters, digits, `.`, `_` and `-`.

A key that belongs to at least one group gets its access from its groups,
not from its own `buckets`/`readOnly`: it may do what any one of its
**enabled** groups allows. A key whose groups are all disabled has no access.
Leaving every group (or deleting the group) restores the key's own scope.

### Audit log

| Method and path | Query | Response |
|---|---|---|
| `GET /api/v1/audit` | `source` (`console`/`s3`), `bucket`, `q` (search actor, action, bucket, resource), `failed=true`, `before` (entry id), `limit` (1..500, default 100) | `{"entries": [entry], "nextBefore"?}` newest first; pass `nextBefore` as `before` for the next page |

An entry is
`{"id","at","source","actor","action","bucket","resource","status","error","remote","requestId"}`.

Recorded: console sign-ins (the typed access key only when it is root's) and
every console change; S3 operations that change data (CreateBucket,
DeleteBucket, bucket tagging, PutObject, CopyObject, CompleteMultipartUpload,
AbortMultipartUpload, DeleteObject, DeleteObjects) and every request refused
with 403. Reads are not recorded. Entries older than `auditRetentionDays`
(default 30) are deleted hourly.
