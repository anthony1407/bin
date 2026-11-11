package admin

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"bin/internal/audit"
	"bin/internal/server"
	"bin/internal/storage"
)

// auditedRoutes names the console actions recorded in the audit log: the
// ones that change something, and signing in. Pieces of a transfer (upload
// parts) and diagnostics (speed tests) are left out.
var auditedRoutes = map[string]string{
	"POST /api/v1/login":                                  "Login",
	"POST /api/v1/login/activate":                         "ActivateConsoleAccess",
	"POST /api/v1/account/password":                       "ChangeOwnPassword",
	"PUT /api/v1/access-keys/{key}/password":              "SetConsolePassword",
	"DELETE /api/v1/access-keys/{key}/password":           "RemoveConsolePassword",
	"DELETE /api/v1/access-keys/{key}/totp":               "RemoveSecondFactor",
	"POST /api/v1/account/totp/enable":                    "EnableSecondFactor",
	"POST /api/v1/account/totp/disable":                   "DisableSecondFactor",
	"DELETE /api/v1/sessions/{id}":                        "EndSession",
	"POST /api/v1/logout":                                 "Logout",
	"POST /api/v1/buckets":                                "CreateBucket",
	"DELETE /api/v1/buckets/{bucket}":                     "DeleteBucket",
	"PUT /api/v1/buckets/{bucket}/tags":                   "PutBucketTags",
	"PUT /api/v1/buckets/{bucket}/quota":                  "PutBucketQuota",
	"PUT /api/v1/buckets/{bucket}/policy":                 "PutBucketPolicy",
	"PUT /api/v1/buckets/{bucket}/versioning":             "PutBucketVersioning",
	"PUT /api/v1/buckets/{bucket}/cors":                   "PutBucketCors",
	"DELETE /api/v1/buckets/{bucket}/versions":            "DeleteObjectVersion",
	"POST /api/v1/buckets/{bucket}/versions/restore":      "RestoreObjectVersion",
	"DELETE /api/v1/buckets/{bucket}/objects":             "DeleteObjects",
	"POST /api/v1/buckets/{bucket}/trash":                 "TrashObjects",
	"POST /api/v1/buckets/{bucket}/trash/restore":         "RestoreObjects",
	"PUT /api/v1/buckets/{bucket}/trash/retention":        "PutTrashRetention",
	"POST /api/v1/buckets/{bucket}/copy":                  "CopyObjects",
	"PUT /api/v1/buckets/{bucket}/upload":                 "PutObject",
	"POST /api/v1/buckets/{bucket}/folders":               "CreateFolder",
	"POST /api/v1/buckets/{bucket}/share":                 "CreateShareLink",
	"POST /api/v1/buckets/{bucket}/uploads/{id}/complete": "CompleteMultipartUpload",
	"POST /api/v1/access-keys":                            "CreateAccessKey",
	"PATCH /api/v1/access-keys/{key}":                     "UpdateAccessKey",
	"DELETE /api/v1/access-keys/{key}":                    "DeleteAccessKey",
	"POST /api/v1/groups":                                 "CreateGroup",
	"PATCH /api/v1/groups/{name}":                         "UpdateGroup",
	"DELETE /api/v1/groups/{name}":                        "DeleteGroup",
	"PUT /api/v1/settings":                                "UpdateSettings",
	"POST /api/v1/settings/revoke-share-links":            "RevokeShareLinks",
	"POST /api/v1/scrub":                                  "StartScrub",
	"POST /api/v1/import/start":                           "ImportBuckets",
	"DELETE /api/v1/import/jobs/{id}":                     "CancelImport",
	"POST /api/v1/import/sources":                         "CreateSavedImport",
	"PUT /api/v1/import/sources/{id}":                     "UpdateSavedImport",
	"DELETE /api/v1/import/sources/{id}":                  "DeleteSavedImport",
	"POST /api/v1/import/sources/{id}/run":                "RunSavedImport",
	"POST /api/v1/dropzones":                              "CreateDropzone",
	"DELETE /api/v1/dropzones/{id}":                       "DeleteDropzone",
	"PUT /api/v1/public/dropzone/files":                   "DropzoneUpload",
	"POST /api/v1/login/ldap":                             "LoginLDAP",
	"GET " + oauthCallbackPath:                            "LoginOpenID",
	"PUT /api/v1/identity/openid":                         "UpdateOpenIDConfig",
	"PUT /api/v1/identity/ldap":                           "UpdateLDAPConfig",
	"DELETE /api/v1/shares/{id}":                          "RevokeShareLink",
	"POST /api/v1/replication/rules":                      "CreateReplicationRule",
	"PUT /api/v1/replication/rules/{id}":                  "UpdateReplicationRule",
	"DELETE /api/v1/replication/rules/{id}":               "DeleteReplicationRule",
	"POST /api/v1/replication/rules/{id}/run":             "RunReplication",
}

// downloadRoutes are recorded only while the downloads setting is on (see
// audit.DownloadsSetting), and only real downloads: inline previews (the
// explorer's thumbnails and viewers) would flood the log.
var downloadRoutes = map[string]string{
	"GET /api/v1/buckets/{bucket}/download": "Download",
	"GET /api/v1/zip/{id}":                  "DownloadZip",
}

// auditInfo lets a handler fill in what the route alone does not tell.
type auditInfo struct {
	actor    string
	bucket   string
	resource string
	size     int64
}

// setAuditSize records how many bytes an upload stored.
func setAuditSize(r *http.Request, n int64) {
	if ai := auditInfoFrom(r); ai != nil {
		ai.size = n
	}
}

type auditKey struct{}

func auditInfoFrom(r *http.Request) *auditInfo {
	ai, _ := r.Context().Value(auditKey{}).(*auditInfo)
	return ai
}

// setAuditActor names who made the request when there is no session yet (sign-in).
func setAuditActor(r *http.Request, actor string) {
	if ai := auditInfoFrom(r); ai != nil {
		ai.actor = actor
	}
}

// setAuditBucket names the bucket when it is in the body rather than the path.
func setAuditBucket(r *http.Request, bucket string) {
	if ai := auditInfoFrom(r); ai != nil {
		ai.bucket = bucket
	}
}

// setAuditResource names what the request acted on, e.g. an access key or a group.
func setAuditResource(r *http.Request, resource string) {
	if ai := auditInfoFrom(r); ai != nil {
		ai.resource = resource
	}
}

// audited records the request in the audit log once it is answered, if its
// route is one of auditedRoutes. It runs inside requireSession, so the
// session user is known.
func (h *Handler) audited(pattern string, next http.HandlerFunc) http.HandlerFunc {
	action, ok := auditedRoutes[pattern]
	dlAction, download := downloadRoutes[pattern]
	if (!ok && !download) || h.opts.Audit == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if download && (r.URL.Query().Get("inline") == "1" || !h.opts.Audit.DownloadsOn(r.Context())) {
			next(w, r)
			return
		}
		ai := &auditInfo{}
		if sess, ok := r.Context().Value(sessionKey{}).(storage.Session); ok {
			ai.actor = sess.User
		}
		r = r.WithContext(context.WithValue(r.Context(), auditKey{}, ai))
		sw := &statusWriter{ResponseWriter: w}
		next(sw, r)
		if download {
			action, ai.size = dlAction, sw.written
		}

		e := storage.AuditEntry{
			Source:    audit.Console,
			Actor:     ai.actor,
			Action:    action,
			Bucket:    r.PathValue("bucket"),
			Resource:  ai.resource,
			Status:    sw.code(),
			Error:     sw.errCode,
			Remote:    clientIP(r),
			RequestID: server.RequestID(r.Context()),
		}
		if e.Actor == "" {
			e.Actor = audit.Anonymous
		}
		if ai.bucket != "" {
			e.Bucket = ai.bucket
		}
		if e.Resource == "" {
			e.Resource = r.URL.Query().Get("key")
		}
		if e.Status < 400 {
			e.Size = ai.size
		}
		h.opts.Audit.Record(e)
	}
}

// statusWriter remembers the status code, through writeError the error code,
// and how many body bytes went out (a download's size).
type statusWriter struct {
	http.ResponseWriter
	status  int
	errCode string
	written int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

// ReadFrom passes zero-copy body writes through; see server.ReadFrom.
func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := server.ReadFrom(w.ResponseWriter, r)
	w.written += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// --- listing ---------------------------------------------------------------------

type auditJSON struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Source    string    `json:"source"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Bucket    string    `json:"bucket,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	Status    int       `json:"status"`
	Error     string    `json:"error,omitempty"`
	Remote    string    `json:"remote,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
	Size      int64     `json:"size,omitempty"` // bytes an upload stored or a download sent
}

const maxAuditPage = 500

// listAudit returns entries newest first. Query: source, bucket, q (search),
// failed=true, before (an entry id, to page back) and limit (default 100).
func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := storage.AuditFilter{
		Source: q.Get("source"),
		Bucket: q.Get("bucket"),
		Actor:  q.Get("actor"),
		Search: q.Get("q"),
		Failed: q.Get("failed") == "true",
		Limit:  100,
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "before must be an entry id.")
			return
		}
		f.Before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > maxAuditPage {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be between 1 and 500.")
			return
		}
		f.Limit = n
	}
	// Show what was just done, even if the recorder has not written it yet.
	h.opts.Audit.Flush()
	// One extra entry tells whether there is another page.
	want := f.Limit
	f.Limit++
	entries, err := h.opts.Store.ListAudit(r.Context(), f)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := map[string]any{}
	if len(entries) > want {
		entries = entries[:want]
		out["nextBefore"] = entries[want-1].ID
	}
	list := make([]auditJSON, 0, len(entries))
	for _, e := range entries {
		list = append(list, auditJSON{
			ID: e.ID, At: e.At, Source: e.Source, Actor: e.Actor, Action: e.Action, Bucket: e.Bucket,
			Resource: e.Resource, Status: e.Status, Error: e.Error, Remote: e.Remote, RequestID: e.RequestID,
			Size: e.Size,
		})
	}
	out["entries"] = list
	writeJSON(w, http.StatusOK, out)
}
