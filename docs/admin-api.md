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
| `GET /api/v1/info` | | version, region, `s3Endpoint`, uptime, totals (`buckets`, `objects`, `bytes`), `disk {total, free}`, and the startup log settings `logLevel`, `logFormat`, `logRequests` (read-only) |
| `GET /api/v1/settings` | | `{"shareMaxExpirySeconds","auditRetentionDays","publicUrl","region"}` |
| `PUT /api/v1/settings` | any of `{"shareMaxExpirySeconds": 60..604800, "auditRetentionDays": 0..3650}` (0 days: keep forever) | settings |
| `POST /api/v1/settings/revoke-share-links` | | `204`; every share link issued so far stops working |
| `POST /api/v1/scrub` | `{"buckets": [...]}` (empty or missing: all buckets) | starts an integrity scan in the background; `404` for an unknown bucket, `409` if one is running |
| `GET /api/v1/scrub` | | `{"state": idle/running/done/failed, "buckets", "objects", "bytes", "corrupt": [{"bucket","key"}]}` |

### Buckets

| Method and path | Body / query | Response |
|---|---|---|
| `GET /api/v1/buckets` | | `{"buckets": [bucket]}` |
| `POST /api/v1/buckets` | `{"name", "policy"?}` (policy below; default private) | `201` bucket |
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
  {"prefix": "tmp/",    "access": "readwrite"},
  {"prefix": "img/logo.png", "access": "readonly", "exact": true}
]}
```

`readonly` allows GET/HEAD of keys under the prefix and listing with that
prefix; `writeonly` allows uploads under it; `readwrite` allows both plus
deletes. With `"exact": true` a rule covers only the object whose key equals
`prefix` (not `img/logo.png.bak`), and never allows listing. `public` is
`readonly` on everything. Anonymous callers can never list buckets, copy, or
bulk-delete.

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
| `POST /api/v1/buckets/{bucket}/share` | `{"key", "expiresIn": seconds, "download": bool}` | `{"id", "url", "expiresAt"}` |
| `GET /api/v1/buckets/{bucket}/shares?key=` | | `{"links": [{"id","url","download","createdBy","createdAt","expiresAt"}]}`: the object's live links, longest-lived first |
| `DELETE /api/v1/shares/{id}` | | `204`; that link stops working at once, the others keep working |
| `POST /api/v1/buckets/{bucket}/uploads?key=` | | `{"uploadId"}` (files over 5 GiB) |
| `PUT /api/v1/buckets/{bucket}/uploads/{id}/parts/{n}?key=` | raw body, ≥ 5 MiB except the last | `{"number","etag","size"}` |
| `POST /api/v1/buckets/{bucket}/uploads/{id}/complete?key=` | `{"parts": [{"number","etag"}]}` | object |
| `DELETE /api/v1/buckets/{bucket}/uploads/{id}?key=` | | `204` |

Share links are presigned S3 URLs signed with an internal read-only key
(never the root key). Their base URL is `BIN_PUBLIC_URL`, or else the host
the console was reached on with the S3 port. They last from 1 minute to the
`shareMaxExpirySeconds` setting, which cannot exceed 7 days (the SigV4 limit).

Each link is recorded: object listings include `shares` (live links) and
`shareExpiresAt` (when the last one ends), and the URL is kept encrypted so
the console can show it again. A revoked link's signature is still valid, so
the S3 API checks every presigned request against the revoked records and
answers `403 AccessDenied` ("This share link was revoked."). Links that ended
over a week ago are forgotten.

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

### Identity providers (OpenID Connect and LDAP)

People can sign in to the console with an OpenID Connect provider or an LDAP
directory. Only members of the configured admin group get in, with full
access, like root; root can always sign in, so a broken configuration cannot
lock anyone out. Sessions are named `openid:<preferred_username|email|sub>`
or `ldap:<username>` in the audit log.

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/identity/openid` | | openid config |
| `PUT /api/v1/identity/openid` | openid config, plus `"clientSecret"` (write only; empty keeps the stored one) | openid config |
| `POST /api/v1/identity/openid/test` | `{"configUrl"}` | `{"issuer","authorizationEndpoint","scopesSupported"?,"claimsSupported"?}` |
| `GET /api/v1/identity/ldap` | | ldap config |
| `PUT /api/v1/identity/ldap` | ldap config, plus `"lookupBindPassword"` (write only) | ldap config |
| `POST /api/v1/identity/ldap/test` | ldap config | `{"status":"ok"}`: connected and bound with the lookup account |

An openid config is
`{"enabled","configUrl","clientId","clientSecretSet","claimName","adminValue","displayName","scopes","redirectUri","defaultRedirectUri"}`.
`configUrl` is the provider's `/.well-known/openid-configuration` address.
`claimName` (default `groups`) is read from the ID token, or from UserInfo when
the ID token lacks it; it may be a list or a space or comma separated string,
and must contain `adminValue`. `redirectUri` defaults to
`<console>/oauth_callback` (`defaultRedirectUri`), which must be registered at
the provider.

An ldap config is
`{"enabled","serverAddr","tlsMode","tlsSkipVerify","lookupBindDn","lookupBindPasswordSet","userDnSearchBase","userDnSearchFilter","groupSearchBase","groupSearchFilter","adminGroupDn"}`.
`tlsMode` is `tls` (ldaps, default), `starttls` or `none`. In the filters,
`%s` is the username and `%d` the user's DN, both escaped. Sign-in binds with
the lookup account, finds exactly one user, binds as that user with the typed
password (an empty password is refused, as it would be an anonymous bind),
then searches the groups as the lookup account; `adminGroupDn` is compared
case-insensitively.

Sign-in endpoints, no session needed:

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/login/providers` | | `{"openid": {"displayName"} \| null, "ldap": bool}` |
| `POST /api/v1/login/ldap` | `{"username","password"}` | session, as `POST /login`; `401` wrong credentials, `403 not_allowed` outside the admin group |
| `GET /api/v1/login/openid?next=/path` | | `302` to the provider |
| `GET /oauth_callback` | | `302` to `next`, or to `/login?error=<reason>` |

OpenID uses the authorization code flow with PKCE (S256), a nonce, and a state
bound to the browser by a cookie; each state works once, for 10 minutes.
LDAP sign-ins share the root login's rate limit per IP.

### Import from another S3 server

| Method and path | Body | Response |
|---|---|---|
| `POST /api/v1/import/test` | `{"endpoint", "accessKey", "secretKey", "region"?}` | `{"buckets": [{"name","size","objects","partial"?}]}` |
| `POST /api/v1/import/start` | same, plus `"buckets": [...]` | `{"jobs": [job], "skipped": [{"bucket","reason"}]}`; `409 nothing_imported` (with `skipped`) when no bucket could start |
| `GET /api/v1/import/jobs` | | `{"jobs": [job]}` newest first |
| `DELETE /api/v1/import/jobs/{id}` | | `204`; objects already copied stay |

`endpoint` is an `http://` or `https://` URL with no path. bin refuses to
connect to loopback, link-local (including the `169.254.169.254` metadata
service) and other cloud metadata addresses, checked on the address actually
dialed; private networks are allowed. Errors from the remote server are not
passed through: the answer says only whether the credentials, the region or
the connection failed.

Each selected bucket is created locally and copied once, two buckets at a
time, keeping content types and user metadata. Buckets that already exist
locally are skipped. A job that copies nothing removes the bucket it
created. A job is
`{"jobId","bucket","endpoint","state","copied","failed","bytes","error"?,"startedAt","finishedAt"?}`
with `state` one of `queued`, `running`, `done`, `failed`, `canceled`. Jobs
are kept in memory and forgotten on restart.

`size` and `objects` from the test count at most 10,000 objects per bucket;
`partial` marks a bucket that has more.

### Dropzones

Upload links for people without an account.

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/dropzones` | | `{"dropzones": [dropzone]}` newest first, expired ones included |
| `POST /api/v1/dropzones` | `{"bucket", "prefix"?, "note"?, "expiresIn": 60..2592000 (seconds), "maxBytes": 1..5 GiB (per file)}` | `201` dropzone |
| `DELETE /api/v1/dropzones/{id}` | | `204`; the link stops working at once |
| `GET /api/v1/dropzones/{id}/uploads` | | `{"uploads": [{"key","name","size","uploadedAt","remote","userAgent"}]}`, newest first, at most 500 |

A dropzone is
`{"id","bucket","prefix","note","maxBytes","createdAt","expiresAt","expired","url","uploads","uploadedBytes","lastUploadAt"?}`.
Every accepted upload is recorded with the uploader's IP (`remote`, the
connection's address: behind a reverse proxy, the proxy's) and User-Agent; the
upload page tells uploaders so. The record goes with its dropzone; the files
stay in the bucket.
`url` is the console's `/upload` page with the token after `#`, so browsers
never send it to the server. The token is stored hashed for lookups and
encrypted with the master key so the console can show the link again.
Deleting a bucket deletes its dropzones.

The upload page uses two endpoints that need no session; the token goes in
the `X-Dropzone-Token` header:

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/public/dropzone` | | `{"note","maxBytes","expiresAt"}` (the bucket and folder are not revealed) |
| `PUT /api/v1/public/dropzone/files?name=…` | raw file, `Content-Length` required | `201 {"name"}`: the name it was stored under |

A missing, unknown or expired token answers `404 no_such_dropzone`. The file
name is reduced to its last path element without control characters. Uploads
never replace a file: a taken name becomes `name (1).ext`, `name (2).ext`, and
so on.

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
