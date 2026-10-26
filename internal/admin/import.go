package admin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"bin/internal/storage"
)

// Import copies buckets from another S3-compatible server into bin. It is a
// one-time copy run in the background; GET /import/jobs reports progress and
// DELETE /import/jobs/{id} cancels a job.

type importRemote struct {
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region"`
}

type importStartRequest struct {
	importRemote
	Buckets []string `json:"buckets"`
	// Settings also copies each bucket's versioning, tags, anonymous access
	// and quota; IAM recreates the remote's users, groups, policies and
	// access keys (MinIO only).
	Settings bool `json:"settings"`
	IAM      bool `json:"iam"`
}

type remoteBucketInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Objects int    `json:"objects"`
	// Partial is set when the bucket was too large to count fully; Size and
	// Objects then cover the first maxStatObjects objects only.
	Partial bool `json:"partial,omitempty"`
}

type importSkipped struct {
	Bucket string `json:"bucket"`
	Reason string `json:"reason"`
}

// maxStatObjects bounds how many objects are counted per bucket when listing
// the remote server, so the connection test answers quickly.
const maxStatObjects = 10000

// maxImportJobs bounds how many buckets are copied at the same time.
const maxImportJobs = 2

// maxImportedMeta matches the S3 API's limit on x-amz-meta-* names and values;
// larger metadata is dropped rather than failing the object.
const maxImportedMeta = 2 << 10

// --- jobs ------------------------------------------------------------------------------

type importJob struct {
	ID         string     `json:"jobId"`
	Bucket     string     `json:"bucket"`
	Endpoint   string     `json:"endpoint"`
	State      string     `json:"state"` // queued, running, done, failed, canceled
	Copied     int        `json:"copied"`
	Failed     int        `json:"failed"`
	Bytes      int64      `json:"bytes"`
	Error      string     `json:"error,omitempty"`
	Settings   []string   `json:"settings,omitempty"` // bucket settings copied
	Notes      []string   `json:"notes,omitempty"`    // settings that could not be
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	cancel context.CancelFunc
}

// importJobs holds the jobs of this process; they are not kept across restarts.
type importJobs struct {
	mu   sync.Mutex
	jobs map[string]*importJob
	sem  chan struct{}
}

func newImportJobs() *importJobs {
	return &importJobs{jobs: map[string]*importJob{}, sem: make(chan struct{}, maxImportJobs)}
}

// update runs fn on the job under the lock.
func (j *importJobs) update(id string, fn func(*importJob)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.jobs[id]; job != nil {
		fn(job)
	}
}

func (j *importJobs) list() []importJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]importJob, 0, len(j.jobs))
	for _, job := range j.jobs {
		out = append(out, *job)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt.After(out[b].StartedAt) })
	return out
}

// --- remote connections --------------------------------------------------------------

var (
	errInvalidEndpoint = errors.New("invalid endpoint")
	errBlockedAddress  = errors.New("remote address not allowed")
)

// blockedMetadata lists cloud metadata addresses outside the link-local range.
var blockedMetadata = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),   // AWS (IPv6)
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
}

// allowedRemoteIP refuses loopback, link-local (which includes the
// 169.254.169.254 metadata service), multicast and unspecified addresses, and
// known metadata endpoints. Private networks stay allowed: importing from a
// server on the LAN is a normal use.
func allowedRemoteIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, m := range blockedMetadata {
		if ip == m {
			return false
		}
	}
	return true
}

// guardDial checks the address actually dialed, after DNS resolution, so a
// name cannot point somewhere else between a check and the connection.
func guardDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddress
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !allowedRemoteIP(ip) {
		return errBlockedAddress
	}
	return nil
}

// literalHostBlocked reports whether an endpoint names a refused address
// directly (e.g. http://127.0.0.1:9000), so a form can say so at once. Host
// names are checked when dialed, which also covers DNS tricks.
func literalHostBlocked(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && dialGuard("tcp", net.JoinHostPort(ip.String(), "1"), nil) != nil
}

// inContainer reports whether bin runs in a Docker or Podman container, where
// "localhost" in an endpoint usually meant the host; the refusal says so.
var inContainer = func() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}()

// dialGuard is guardDial; tests that use a remote on 127.0.0.1 replace it.
var dialGuard = guardDial

// importTransport dials only allowed addresses and ignores proxy settings,
// which would otherwise bypass the check.
var importTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: func(n, a string, c syscall.RawConn) error { return dialGuard(n, a, c) }}).DialContext
	// A remote that accepts the connection but never answers must not hold a
	// job (or an import slot) forever.
	t.ResponseHeaderTimeout = time.Minute
	return t
}()

// remoteClient validates the endpoint (http or https URL, no path) and returns
// a client for it.
func remoteClient(in importRemote) (*minio.Client, error) {
	u, err := parseRemoteEndpoint(in.Endpoint)
	if err != nil {
		return nil, err
	}
	return minio.New(u.Host, &minio.Options{
		Creds:     credentials.NewStaticV4(in.AccessKey, in.SecretKey, ""),
		Secure:    u.Scheme == "https",
		Region:    in.Region,
		Transport: importTransport,
	})
}

func parseRemoteEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, errInvalidEndpoint
	}
	return u, nil
}

// remoteErrorMessage turns a remote failure into a message that does not echo
// what the remote side answered: the endpoint is user-chosen, and its replies
// must not turn the console into a probe of the server's network.
func remoteErrorMessage(err error) (code, message string) {
	switch {
	case errors.Is(err, errInvalidEndpoint):
		return "invalid_endpoint", "The endpoint must be an http:// or https:// URL with a host and no path, e.g. https://s3.example.com."
	case errors.Is(err, errBlockedAddress) || strings.Contains(err.Error(), errBlockedAddress.Error()):
		msg := "bin does not connect to loopback, link-local or cloud metadata addresses."
		if inContainer {
			msg += " bin runs in a container, where localhost is the container itself:" +
				" for a server on the Docker host (or an SSH tunnel there), use http://host.docker.internal:<port>."
		}
		return "endpoint_not_allowed", msg
	}
	switch minio.ToErrorResponse(err).Code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "InvalidToken", "ExpiredToken":
		return "remote_auth", "The remote server rejected the access key or secret key."
	case "AuthorizationHeaderMalformed", "InvalidRegion":
		return "remote_region", "The remote server expects a different region."
	case "NoSuchBucket":
		return "remote_no_bucket", "The bucket does not exist on the remote server."
	}
	return "remote_unreachable", "Could not reach the remote S3 server. Check the endpoint and the region."
}

func (h *Handler) remoteError(w http.ResponseWriter, err error) {
	code, message := remoteErrorMessage(err)
	h.opts.Log.Warn("import: remote request failed", "err", err)
	status := http.StatusBadGateway
	if code == "invalid_endpoint" || code == "endpoint_not_allowed" {
		status = http.StatusBadRequest
	}
	writeError(w, status, code, message)
}

func validRemote(w http.ResponseWriter, in importRemote) bool {
	if in.Endpoint == "" || in.AccessKey == "" || in.SecretKey == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "endpoint, accessKey and secretKey are required.")
		return false
	}
	return true
}

// --- handlers --------------------------------------------------------------------------

// handleImportTest connects to a remote S3-compatible server and lists its buckets.
func (h *Handler) handleImportTest(w http.ResponseWriter, r *http.Request) {
	var in importRemote
	if !readJSON(w, r, &in) || !validRemote(w, in) {
		return
	}
	client, err := remoteClient(in)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	out := make([]remoteBucketInfo, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, remoteBucketStats(ctx, client, b.Name))
	}
	// Users and policies can come along when the remote is MinIO and the
	// credentials may read them.
	summary := iamSummary{}
	if adm, err := newMinioAdmin(in); err == nil {
		ictx, icancel := context.WithTimeout(r.Context(), 30*time.Second)
		iam, err := adm.fetchIAM(ictx)
		icancel()
		if err != nil {
			h.opts.Log.Info("import: remote users and policies not readable", "err", err)
			summary.Reason = iamErrorMessage(err)
		} else {
			summary = summarizeIAM(iam)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out, "iam": summary})
}

// remoteBucketStats counts up to maxStatObjects objects of a bucket.
func remoteBucketStats(ctx context.Context, client *minio.Client, bucket string) remoteBucketInfo {
	info := remoteBucketInfo{Name: bucket}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the listing goroutine when we stop early
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			info.Partial = true
			break
		}
		if info.Objects == maxStatObjects {
			info.Partial = true
			break
		}
		info.Size += obj.Size
		info.Objects++
	}
	return info
}

// handleImportStart creates each selected bucket locally and copies it in
// the background. Buckets that already exist locally are skipped.
func (h *Handler) handleImportStart(w http.ResponseWriter, r *http.Request) {
	var in importStartRequest
	if !readJSON(w, r, &in) || !validRemote(w, in.importRemote) {
		return
	}
	if len(in.Buckets) == 0 && !in.IAM {
		writeError(w, http.StatusBadRequest, "bad_request", "Select at least one bucket.")
		return
	}
	client, err := remoteClient(in.importRemote)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	adm, err := newMinioAdmin(in.importRemote)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	setAuditResource(r, strings.Join(in.Buckets, ","))

	started := []importJob{}
	skipped := []importSkipped{}
	seen := map[string]bool{}
	for _, bucket := range in.Buckets {
		if seen[bucket] {
			continue
		}
		seen[bucket] = true
		err := h.opts.Store.CreateBucket(r.Context(), bucket)
		switch {
		case errors.Is(err, storage.ErrBucketExists):
			skipped = append(skipped, importSkipped{bucket, "A bucket with this name already exists here."})
			continue
		case errors.Is(err, storage.ErrInvalidBucketName):
			skipped = append(skipped, importSkipped{bucket, "The name is not a valid bucket name here."})
			continue
		case err != nil:
			h.opts.Log.Error("import: creating local bucket", "bucket", bucket, "err", err)
			skipped = append(skipped, importSkipped{bucket, "The local bucket could not be created."})
			continue
		}
		var settings, notes []string
		if in.Settings {
			settings, notes = h.importBucketSettings(r.Context(), client, adm, bucket)
		}
		started = append(started, h.startImportJob(client, in.Endpoint, bucket, settings, notes))
	}

	// Identities come after the buckets, so their access can name them.
	resp := map[string]any{"jobs": started, "skipped": skipped}
	if in.IAM {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		iam, err := adm.fetchIAM(ctx)
		if err == nil {
			var result iamImportResult
			if result, err = h.importIAM(ctx, iam); err == nil {
				resp["iam"] = result
			}
		}
		if err != nil {
			h.opts.Log.Warn("import: users and policies not imported", "err", err)
			resp["iamError"] = iamErrorMessage(err)
		}
	}

	if len(started) == 0 && !in.IAM {
		if sw, ok := w.(*statusWriter); ok {
			sw.errCode = "nothing_imported" // for the audit log
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "nothing_imported", "message": "None of the selected buckets could be imported.",
			"jobs": started, "skipped": skipped,
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) startImportJob(client *minio.Client, endpoint, bucket string, settings, notes []string) importJob {
	ctx, cancel := context.WithCancel(context.Background())
	job := &importJob{
		ID:        "import-" + randomToken()[:12],
		Bucket:    bucket,
		Endpoint:  endpoint,
		Settings:  settings,
		Notes:     notes,
		State:     "queued",
		StartedAt: time.Now().UTC(),
		cancel:    cancel,
	}
	h.imports.mu.Lock()
	h.imports.jobs[job.ID] = job
	snapshot := *job
	h.imports.mu.Unlock()

	go h.runImportJob(ctx, client, job.ID, bucket)
	return snapshot
}

func (h *Handler) runImportJob(ctx context.Context, client *minio.Client, id, bucket string) {
	select {
	case h.imports.sem <- struct{}{}:
		defer func() { <-h.imports.sem }()
	case <-ctx.Done():
		h.finishImportJob(id, bucket, ctx.Err())
		return
	}
	h.imports.update(id, func(j *importJob) { j.State = "running" })
	h.opts.Log.Info("import job started", "job", id, "bucket", bucket)

	var listErr error
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			listErr = obj.Err
			break
		}
		n, err := h.importObject(ctx, client, bucket, obj)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			h.opts.Log.Warn("import: object not copied", "job", id, "bucket", bucket, "key", obj.Key, "err", err)
		}
		h.imports.update(id, func(j *importJob) {
			if err != nil {
				j.Failed++
			} else {
				j.Copied++
				j.Bytes += n
			}
		})
	}
	if ctx.Err() != nil {
		listErr = ctx.Err()
	}
	h.finishImportJob(id, bucket, listErr)
}

// importObject copies one object, keeping its content type and user metadata.
func (h *Handler) importObject(ctx context.Context, client *minio.Client, bucket string, obj minio.ObjectInfo) (int64, error) {
	info, _, err := h.copyFromRemote(ctx, client, bucket, obj.Key, bucket, obj.Key)
	return info.Size, err
}

// copyFromRemote copies remoteBucket/remoteKey into localBucket/localKey,
// keeping the content type and user metadata. It returns the stored object
// and the remote ETag of what it read.
func (h *Handler) copyFromRemote(ctx context.Context, client *minio.Client, remoteBucket, remoteKey, localBucket, localKey string) (storage.ObjectInfo, string, error) {
	src, err := client.GetObject(ctx, remoteBucket, remoteKey, minio.GetObjectOptions{})
	if err != nil {
		return storage.ObjectInfo{}, "", err
	}
	defer src.Close()
	stat, err := src.Stat()
	if err != nil {
		return storage.ObjectInfo{}, "", err
	}
	contentType := stat.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Stored like the S3 API stores x-amz-meta-*: lowercase names, 2 KiB in all.
	meta, total := map[string]string{}, 0
	for k, v := range stat.UserMetadata {
		meta[strings.ToLower(k)] = v
		total += len(k) + len(v)
	}
	if total > maxImportedMeta {
		meta = nil
	}
	info, err := h.opts.Store.PutObject(ctx, localBucket, localKey, src, stat.Size, storage.PutOptions{
		ContentType: contentType,
		UserMeta:    meta,
	})
	if err != nil {
		return storage.ObjectInfo{}, "", err
	}
	return info, stat.ETag, nil
}

// finishImportJob records the outcome. A job that copied nothing removes the
// bucket it created, so a failed import does not leave an empty bucket behind.
func (h *Handler) finishImportJob(id, bucket string, err error) {
	var job importJob
	h.imports.update(id, func(j *importJob) {
		now := time.Now().UTC()
		j.FinishedAt = &now
		switch {
		case errors.Is(err, context.Canceled):
			j.State = "canceled"
		case err != nil:
			j.State = "failed"
			_, j.Error = remoteErrorMessage(err)
		case j.Failed > 0 && j.Copied == 0:
			j.State = "failed"
			j.Error = "No object could be copied."
		default:
			j.State = "done"
		}
		j.cancel()
		job = *j
	})
	// A job that failed or was canceled before copying anything leaves no
	// trace; an empty bucket that imported fine stays, empty like the remote.
	if job.Copied == 0 && job.State != "done" {
		if derr := h.opts.Store.DeleteBucket(context.Background(), bucket); derr != nil && !errors.Is(derr, storage.ErrBucketNotEmpty) {
			h.opts.Log.Warn("import: removing empty bucket", "bucket", bucket, "err", derr)
		}
	}
	h.opts.Log.Info("import job finished", "job", id, "bucket", bucket, "state", job.State,
		"copied", job.Copied, "failed", job.Failed, "err", err)
}

func (h *Handler) listImportJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": h.imports.list()})
}

// cancelImportJob stops a queued or running job. Objects already copied stay.
func (h *Handler) cancelImportJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, active := false, false
	h.imports.update(id, func(j *importJob) {
		found = true
		setAuditBucket(r, j.Bucket)
		if j.State == "queued" || j.State == "running" {
			active = true
			j.cancel()
		}
	})
	setAuditResource(r, id)
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no_such_job", "The import job does not exist.")
	case !active:
		writeError(w, http.StatusConflict, "job_finished", "The import job has already finished.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
