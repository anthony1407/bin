// Package admin implements the console listener: the JSON admin API under
// /api/v1 used by the web panel, and the panel itself (Options.Panel).
//
// Every endpoint except health, the sign-in endpoints (root, LDAP, OpenID and
// its /oauth_callback) and the dropzone upload page (which authorizes with
// the link's token) needs a session cookie; requests
// that change state also need the session's CSRF token in X-CSRF-Token and,
// when the browser sends one, an Origin matching the host.
package admin

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"bin/internal/audit"
	"bin/internal/auth"
	"bin/internal/buildinfo"
	"bin/internal/server"
	"bin/internal/storage"
)

// Options configures the console handler.
type Options struct {
	Store   *storage.Store
	Keys    *auth.Keyring
	Audit   *audit.Recorder // records console changes and sign-ins; nil records nothing
	Region  string
	DataDir string
	// S3Addr is the S3 listen address, used to build share links when
	// PublicURL is empty; PublicURL is the S3 endpoint as clients see it.
	S3Addr    string
	PublicURL string
	// SessionTTL is how long a console login lasts (default 12h).
	SessionTTL time.Duration
	Log        *slog.Logger
	// LogRequests logs one line per request; health probes only at debug level.
	LogRequests bool
	// LogLevel and LogFormat are reported by /info, read-only: they come from
	// BIN_LOG_LEVEL and BIN_LOG_FORMAT at startup.
	LogLevel  string
	LogFormat string
	// Panel serves everything outside /api/ (the embedded web panel).
	Panel http.Handler
}

// Handler serves the console.
type Handler struct {
	opts    Options
	started time.Time
	limiter *loginLimiter

	scrubMu sync.Mutex
	scrub   scrubStatus

	imports    *importJobs
	oidcStates *oidcStates
}

// NewHandler returns the console HTTP handler.
func NewHandler(opts Options) http.Handler {
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	h := &Handler{opts: opts, started: time.Now(), limiter: newLoginLimiter(), imports: newImportJobs(),
		oidcStates: newOIDCStates()}

	mux := http.NewServeMux()
	public := func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, h.audited(pattern, fn)) }
	private := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, h.requireSession(h.audited(pattern, fn)))
	}

	public("GET /api/v1/health", h.health)
	public("POST /api/v1/login", h.login)
	public("GET /api/v1/login/providers", h.loginProviders)
	public("POST /api/v1/login/ldap", h.loginLDAP)
	public("GET /api/v1/login/openid", h.loginOpenID)
	public("GET "+oauthCallbackPath, h.oauthCallback)
	private("POST /api/v1/logout", h.logout)
	private("GET /api/v1/session", h.session)
	private("GET /api/v1/info", h.info)

	private("GET /api/v1/buckets", h.listBuckets)
	private("POST /api/v1/buckets", h.createBucket)
	private("GET /api/v1/buckets/{bucket}", h.getBucket)
	private("DELETE /api/v1/buckets/{bucket}", h.deleteBucket)
	private("PUT /api/v1/buckets/{bucket}/tags", h.putTags)
	private("PUT /api/v1/buckets/{bucket}/quota", h.putQuota)
	private("PUT /api/v1/buckets/{bucket}/policy", h.putPolicy)

	private("GET /api/v1/buckets/{bucket}/objects", h.listObjects)
	private("DELETE /api/v1/buckets/{bucket}/objects", h.deleteObjects)
	private("GET /api/v1/buckets/{bucket}/object", h.statObject)
	private("GET /api/v1/buckets/{bucket}/download", h.download)
	private("PUT /api/v1/buckets/{bucket}/upload", h.upload)
	private("POST /api/v1/buckets/{bucket}/folders", h.createFolder)
	private("POST /api/v1/buckets/{bucket}/share", h.share)
	private("GET /api/v1/buckets/{bucket}/shares", h.listShares)
	private("DELETE /api/v1/shares/{id}", h.revokeShare)
	private("POST /api/v1/buckets/{bucket}/uploads", h.createUpload)
	private("PUT /api/v1/buckets/{bucket}/uploads/{id}/parts/{n}", h.uploadPart)
	private("POST /api/v1/buckets/{bucket}/uploads/{id}/complete", h.completeUpload)
	private("DELETE /api/v1/buckets/{bucket}/uploads/{id}", h.abortUpload)

	private("GET /api/v1/access-keys", h.listKeys)
	private("POST /api/v1/access-keys", h.createKey)
	private("PATCH /api/v1/access-keys/{key}", h.updateKey)
	private("DELETE /api/v1/access-keys/{key}", h.deleteKey)

	private("GET /api/v1/groups", h.listGroups)
	private("POST /api/v1/groups", h.createGroup)
	private("PATCH /api/v1/groups/{name}", h.updateGroup)
	private("DELETE /api/v1/groups/{name}", h.deleteGroup)

	private("GET /api/v1/audit", h.listAudit)

	private("GET /api/v1/settings", h.getSettings)
	private("PUT /api/v1/settings", h.putSettings)
	private("POST /api/v1/settings/revoke-share-links", h.revokeShareLinks)
	private("GET /api/v1/scrub", h.scrubStatus)
	private("POST /api/v1/scrub", h.startScrub)

	private("GET /api/v1/speedtest/ping", h.speedtestPing)
	private("GET /api/v1/speedtest/download", h.speedtestDownload)
	private("POST /api/v1/speedtest/upload", h.speedtestUpload)

	private("POST /api/v1/import/test", h.handleImportTest)
	private("POST /api/v1/import/start", h.handleImportStart)
	private("GET /api/v1/import/jobs", h.listImportJobs)
	private("DELETE /api/v1/import/jobs/{id}", h.cancelImportJob)

	private("GET /api/v1/identity/openid", h.getOpenID)
	private("PUT /api/v1/identity/openid", h.putOpenID)
	private("POST /api/v1/identity/openid/test", h.testOpenID)
	private("GET /api/v1/identity/ldap", h.getLDAP)
	private("PUT /api/v1/identity/ldap", h.putLDAP)
	private("POST /api/v1/identity/ldap/test", h.testLDAP)

	private("GET /api/v1/dropzones", h.listDropzones)
	private("POST /api/v1/dropzones", h.createDropzone)
	private("DELETE /api/v1/dropzones/{id}", h.deleteDropzone)
	private("GET /api/v1/dropzones/{id}/uploads", h.listDropzoneUploads)
	// The upload page: no session, the dropzone token authorizes.
	public("GET /api/v1/public/dropzone", h.publicDropzone)
	public("PUT /api/v1/public/dropzone/files", h.publicDropzoneUpload)

	public("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
	})
	if opts.Panel != nil {
		mux.Handle("/", opts.Panel)
	} else {
		public("/", http.NotFound)
	}

	var next http.Handler = mux
	next = server.Recover(opts.Log, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
	}, next)
	next = server.AccessLog(opts.Log, server.LogOptions{
		Requests: opts.LogRequests,
		Quiet:    func(r *http.Request) bool { return r.URL.Path == "/api/v1/health" },
	}, next)
	return server.WithRequestID("X-Request-Id", next)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": buildinfo.Version, "commit": buildinfo.Commit})
}

// --- JSON helpers -------------------------------------------------------------

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	if sw, ok := w.(*statusWriter); ok {
		sw.errCode = code // for the audit log
	}
	writeJSON(w, status, apiError{Error: code, Message: message})
}

// maxJSONBody bounds JSON request bodies.
const maxJSONBody = 1 << 20

// readJSON decodes a JSON body into v, rejecting unknown fields.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// storageError writes the response for a storage error and logs unexpected ones.
func (h *Handler) storageError(w http.ResponseWriter, r *http.Request, err error) {
	for _, m := range []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{storage.ErrNoSuchBucket, 404, "no_such_bucket", "The bucket does not exist."},
		{storage.ErrBucketExists, 409, "bucket_exists", "A bucket with this name already exists."},
		{storage.ErrInvalidBucketName, 400, "invalid_bucket_name", "Bucket names use 3-63 lowercase letters, digits, dots and hyphens, and start and end with a letter or digit."},
		{storage.ErrBucketNotEmpty, 409, "bucket_not_empty", "The bucket is not empty."},
		{storage.ErrNoSuchKey, 404, "no_such_key", "The object does not exist."},
		{storage.ErrKeyTooLong, 400, "key_too_long", "Object keys are limited to 1024 bytes."},
		{storage.ErrInvalidKey, 400, "invalid_key", "Object keys must be valid UTF-8."},
		{storage.ErrQuotaExceeded, 400, "quota_exceeded", "The bucket quota would be exceeded."},
		{storage.ErrNoSuchUpload, 404, "no_such_upload", "The upload does not exist."},
		{storage.ErrInvalidPart, 400, "invalid_part", "A listed part is missing or does not match."},
		{storage.ErrInvalidPartOrder, 400, "invalid_part_order", "Parts must be listed in ascending order."},
		{storage.ErrEntityTooSmall, 400, "part_too_small", "Every part but the last must be at least 5 MiB."},
		{storage.ErrIncompleteBody, 400, "incomplete_body", "The upload ended before all the announced bytes arrived."},
		{storage.ErrNoSuchAccessKey, 404, "no_such_access_key", "The access key does not exist."},
		{storage.ErrAccessKeyExists, 409, "access_key_exists", "The access key already exists."},
		{storage.ErrNoSuchGroup, 404, "no_such_group", "The group does not exist."},
		{storage.ErrGroupExists, 409, "group_exists", "A group with this name already exists."},
		{storage.ErrNoSuchDropzone, 404, "no_such_dropzone", "The upload link does not exist."},
		{storage.ErrNoSuchShareLink, 404, "no_such_share_link", "The share link does not exist."},
	} {
		if errors.Is(err, m.err) {
			writeError(w, m.status, m.code, m.message)
			return
		}
	}
	h.opts.Log.Error("console request failed", "id", server.RequestID(r.Context()), "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
}

// sameOrigin reports whether a browser-sent Origin header (if any) matches
// the host the request was sent to.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser cross-origin request
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}
