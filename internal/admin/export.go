package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/tags"

	"bin/internal/auth"
	"bin/internal/storage"
)

// Export sends buckets of bin to another S3-compatible server (MinIO, AWS S3,
// Cloudflare R2, ...), the mirror of import. A saved export names the server
// and the buckets (or all of them); each run copies, per bucket, what is new
// or changed since the remote last got it, creating missing buckets there.
// The console's trash is never sent. Every object is read back against its
// SHA-256 while it is sent, so corrupt data does not leave bin.
//
// Jobs run in the background, like imports: GET /export/jobs reports them and
// DELETE /export/jobs/{id} cancels one. What was sent stays on the remote.

// maxExportJobs bounds how many buckets are sent at the same time.
const maxExportJobs = 2

func newExportJobs() *importJobs {
	return &importJobs{jobs: map[string]*importJob{}, sem: make(chan struct{}, maxExportJobs)}
}

type exportTargetJSON struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Endpoint     string     `json:"endpoint"`
	AccessKey    string     `json:"accessKey"`
	SecretKeySet bool       `json:"secretKeySet"`
	Region       string     `json:"region"`
	Buckets      []string   `json:"buckets"`
	AllBuckets   bool       `json:"allBuckets"`
	Settings     bool       `json:"settings"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastRunAt    *time.Time `json:"lastRunAt"`
	// Jobs are this export's runs since bin last restarted.
	Jobs []importJob `json:"jobs"`
}

func (h *Handler) toExportTargetJSON(t storage.ExportTarget) exportTargetJSON {
	jobs := []importJob{}
	for _, j := range h.exports.list() {
		if j.Source == t.ID {
			jobs = append(jobs, j)
		}
	}
	buckets := t.Buckets
	if buckets == nil {
		buckets = []string{}
	}
	return exportTargetJSON{
		ID: t.ID, Name: t.Name, Endpoint: t.Endpoint, AccessKey: t.AccessKey, SecretKeySet: t.SecretEnc != "",
		Region: t.Region, Buckets: buckets, AllBuckets: t.AllBuckets, Settings: t.Settings,
		CreatedAt: t.Created, LastRunAt: optionalTime(t.LastRun), Jobs: jobs,
	}
}

// readExportTarget validates the body into a target; old is the saved export
// being edited, nil when creating one.
func (h *Handler) readExportTarget(w http.ResponseWriter, r *http.Request, old *storage.ExportTarget) (storage.ExportTarget, bool) {
	var in struct {
		Name       string   `json:"name"`
		Endpoint   string   `json:"endpoint"`
		AccessKey  string   `json:"accessKey"`
		SecretKey  string   `json:"secretKey"` // empty on update: keep the stored one
		Region     string   `json:"region"`
		Buckets    []string `json:"buckets"`
		AllBuckets bool     `json:"allBuckets"`
		Settings   bool     `json:"settings"`
	}
	if !readJSON(w, r, &in) {
		return storage.ExportTarget{}, false
	}
	fail := func(field, msg string) (storage.ExportTarget, bool) {
		writeFieldError(w, "invalid_export", field, msg)
		return storage.ExportTarget{}, false
	}
	t := storage.ExportTarget{
		Name: strings.TrimSpace(in.Name), Endpoint: strings.TrimSpace(in.Endpoint), AccessKey: strings.TrimSpace(in.AccessKey),
		Region: strings.TrimSpace(in.Region), Buckets: in.Buckets, AllBuckets: in.AllBuckets, Settings: in.Settings,
	}
	if old != nil {
		t.ID, t.SecretEnc = old.ID, old.SecretEnc
	}
	if in.SecretKey != "" {
		t.SecretEnc = h.opts.Keys.Box.Encrypt(in.SecretKey)
	}
	if t.AllBuckets {
		t.Buckets = nil
	}
	switch {
	case t.Name == "" || len(t.Name) > 100:
		return fail("name", "Give the export a name of up to 100 characters.")
	case t.AccessKey == "":
		return fail("accessKey", "The access key is required.")
	case t.SecretEnc == "":
		return fail("secretKey", "The secret key is required.")
	case !t.AllBuckets && len(t.Buckets) == 0:
		return fail("buckets", "Select at least one bucket, or all of them.")
	}
	if _, err := parseRemoteEndpoint(t.Endpoint); err != nil {
		_, msg := remoteErrorMessage(err)
		return fail("endpoint", msg)
	}
	if literalHostBlocked(t.Endpoint) {
		_, msg := remoteErrorMessage(errBlockedAddress)
		return fail("endpoint", msg)
	}
	for _, b := range t.Buckets {
		if _, err := h.opts.Store.GetBucket(r.Context(), b); err != nil {
			return fail("buckets", "This bucket does not exist here: "+b)
		}
	}
	return t, true
}

// testExport connects to a remote server and tells, for each local bucket,
// whether a bucket of that name is already there (it would be updated).
func (h *Handler) testExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		importRemote
		// Target is a saved export whose stored secret key is used when
		// SecretKey is empty, so editing one does not ask for it again.
		Target string `json:"target"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	remote := in.importRemote
	if remote.SecretKey == "" && in.Target != "" {
		t, err := h.opts.Store.ExportTarget(r.Context(), in.Target)
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		if remote.SecretKey, err = h.opts.Keys.Box.Decrypt(t.SecretEnc); err != nil {
			writeError(w, http.StatusInternalServerError, "secret_unreadable", "The saved secret key could not be read; enter it again.")
			return
		}
	}
	if !validRemote(w, remote) {
		return
	}
	client, err := remoteClient(remote)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	remoteBuckets, err := client.ListBuckets(ctx)
	if err != nil {
		h.remoteError(w, err)
		return
	}
	there := map[string]bool{}
	for _, b := range remoteBuckets {
		there[b.Name] = true
	}
	local, err := h.opts.Store.ListBuckets(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	usage, err := h.opts.Store.BucketUsage(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	type bucketInfo struct {
		Name    string `json:"name"`
		Objects int64  `json:"objects"`
		Size    int64  `json:"size"`
		Exists  bool   `json:"exists"` // already on the remote: only new and changed files go
	}
	out := make([]bucketInfo, 0, len(local))
	for _, b := range local {
		u := usage[b.Name]
		out = append(out, bucketInfo{Name: b.Name, Objects: u.Objects, Size: u.Bytes, Exists: there[b.Name]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out})
}

func (h *Handler) listExportTargets(w http.ResponseWriter, r *http.Request) {
	list, err := h.opts.Store.ListExportTargets(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]exportTargetJSON, 0, len(list))
	for _, t := range list {
		out = append(out, h.toExportTargetJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"exports": out})
}

func (h *Handler) createExportTarget(w http.ResponseWriter, r *http.Request) {
	t, ok := h.readExportTarget(w, r, nil)
	if !ok {
		return
	}
	t.ID = "exp_" + randomToken()[:12]
	setAuditResource(r, t.Name)
	if err := h.opts.Store.CreateExportTarget(r.Context(), t); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("saved export created", "export", t.ID, "name", t.Name, "endpoint", t.Endpoint)
	created, err := h.opts.Store.ExportTarget(r.Context(), t.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toExportTargetJSON(created))
}

func (h *Handler) updateExportTarget(w http.ResponseWriter, r *http.Request) {
	old, err := h.opts.Store.ExportTarget(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	t, ok := h.readExportTarget(w, r, &old)
	if !ok {
		return
	}
	setAuditResource(r, t.Name)
	if err := h.opts.Store.UpdateExportTarget(r.Context(), t); err != nil {
		h.storageError(w, r, err)
		return
	}
	updated, err := h.opts.Store.ExportTarget(r.Context(), t.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.toExportTargetJSON(updated))
}

func (h *Handler) deleteExportTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	setAuditResource(r, id)
	if err := h.opts.Store.DeleteExportTarget(r.Context(), id); err != nil {
		h.storageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runExportTarget starts a run of a saved export: one job per bucket.
func (h *Handler) runExportTarget(w http.ResponseWriter, r *http.Request) {
	t, err := h.opts.Store.ExportTarget(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	setAuditResource(r, t.Name)
	for _, j := range h.exports.list() {
		if j.Source == t.ID && (j.State == "queued" || j.State == "running") {
			writeError(w, http.StatusConflict, "export_running", "This export is already running.")
			return
		}
	}
	secret, err := h.opts.Keys.Box.Decrypt(t.SecretEnc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "secret_unreadable", "The saved secret key could not be read; edit the export and enter it again.")
		return
	}
	client, err := remoteClient(importRemote{Endpoint: t.Endpoint, AccessKey: t.AccessKey, SecretKey: secret, Region: t.Region})
	if err != nil {
		h.remoteError(w, err)
		return
	}
	buckets := t.Buckets
	if t.AllBuckets {
		all, err := h.opts.Store.ListBuckets(r.Context())
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		buckets = nil
		for _, b := range all {
			buckets = append(buckets, b.Name)
		}
	}
	if err := h.opts.Store.MarkExportTargetRun(r.Context(), t.ID); err != nil {
		h.storageError(w, r, err)
		return
	}
	started := []importJob{}
	for _, b := range buckets {
		if _, err := h.opts.Store.GetBucket(r.Context(), b); err != nil {
			continue // deleted here since the export was saved
		}
		started = append(started, h.startExportJob(client, t, b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": started})
}

func (h *Handler) listExportJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": h.exports.list()})
}

func (h *Handler) cancelExportJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, active := false, false
	h.exports.update(id, func(j *importJob) {
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
		writeError(w, http.StatusNotFound, "no_such_job", "The export job does not exist.")
	case !active:
		writeError(w, http.StatusConflict, "job_finished", "The export job has already finished.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- running -----------------------------------------------------------------------------

func (h *Handler) startExportJob(client *minio.Client, t storage.ExportTarget, bucket string) importJob {
	ctx, cancel := context.WithCancel(context.Background())
	job := &importJob{
		ID: "export-" + randomToken()[:12], Bucket: bucket, Endpoint: t.Endpoint, Source: t.ID,
		State: "queued", StartedAt: time.Now().UTC(), cancel: cancel,
	}
	h.exports.mu.Lock()
	h.exports.jobs[job.ID] = job
	snapshot := *job
	h.exports.mu.Unlock()
	go h.runExportJob(ctx, client, job.ID, bucket, t.Settings, t.Region)
	return snapshot
}

func (h *Handler) runExportJob(ctx context.Context, client *minio.Client, id, bucket string, settings bool, region string) {
	select {
	case h.exports.sem <- struct{}{}:
		defer func() { <-h.exports.sem }()
	case <-ctx.Done():
		h.finishExportJob(id, bucket, ctx.Err())
		return
	}
	h.exports.update(id, func(j *importJob) { j.State = "running" })
	h.opts.Log.Info("export job started", "job", id, "bucket", bucket)

	// The remote bucket: created when missing (its settings then come along),
	// otherwise only new and changed files are sent and its settings are kept.
	exists, err := client.BucketExists(ctx, bucket)
	if err == nil && !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region})
		if err == nil && settings {
			applied, notes := h.exportBucketSettings(ctx, client, bucket)
			h.exports.update(id, func(j *importJob) { j.Settings, j.Notes = applied, notes })
		}
	} else if err == nil {
		h.exports.update(id, func(j *importJob) {
			j.Update = true
			j.Notes = append(j.Notes, "The bucket already exists on the remote: only new and changed files are sent, and its settings are kept.")
		})
	}
	if err != nil {
		h.finishExportJob(id, bucket, err)
		return
	}

	// What the remote has, to send only what is new or changed.
	remote := map[string]minio.ObjectInfo{}
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			h.finishExportJob(id, bucket, obj.Err)
			return
		}
		remote[obj.Key] = obj
	}

	work := make(chan storage.ObjectInfo)
	var wg sync.WaitGroup
	for range transferWorkers {
		wg.Go(func() {
			for o := range work {
				h.exportOne(ctx, client, id, bucket, o, remote)
			}
		})
	}
	var listErr error
	after := ""
list:
	for {
		page, err := h.opts.Store.ListObjects(ctx, bucket, storage.ListOptions{After: after, MaxKeys: 1000})
		if err != nil {
			listErr = err
			break
		}
		for _, o := range page.Objects {
			if inTrash(o.Key) {
				continue // deleted files stay here
			}
			select {
			case work <- o:
			case <-ctx.Done():
				break list
			}
		}
		if !page.IsTruncated {
			break
		}
		after = page.NextMarker
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil {
		listErr = ctx.Err()
	}
	h.finishExportJob(id, bucket, listErr)
}

// exportOne sends one object unless the remote already has it: same size,
// and the same ETag or a copy written after this one last changed (multipart
// ETags differ between servers for the same content).
func (h *Handler) exportOne(ctx context.Context, client *minio.Client, id, bucket string, o storage.ObjectInfo, remote map[string]minio.ObjectInfo) {
	if ctx.Err() != nil {
		return
	}
	if r, ok := remote[o.Key]; ok && r.Size == o.Size &&
		(strings.Trim(r.ETag, `"`) == o.ETag || !r.LastModified.Before(o.ModTime)) {
		h.exports.update(id, func(j *importJob) { j.Unchanged++ })
		return
	}
	n, err := h.sendObject(ctx, client, bucket, o.Key)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		h.opts.Log.Warn("export: object not sent", "job", id, "bucket", bucket, "key", o.Key, "err", err)
	}
	h.exports.update(id, func(j *importJob) {
		if err != nil {
			j.Failed++
		} else {
			j.Copied++
			j.Bytes += n
		}
	})
}

// sendObject uploads one local object under the same key, verified against
// its SHA-256 as it is read, keeping its content type and user metadata.
func (h *Handler) sendObject(ctx context.Context, client *minio.Client, bucket, key string) (int64, error) {
	info, data, err := h.opts.Store.OpenObject(ctx, bucket, key)
	if err != nil {
		return 0, err
	}
	defer data.Close()
	meta := map[string]string{}
	for k, v := range info.UserMeta {
		meta[k] = v
	}
	if _, err := client.PutObject(ctx, bucket, key, data.Verified(), info.Size, minio.PutObjectOptions{
		ContentType: info.ContentType, UserMetadata: meta, PartSize: replicationPartSize,
	}); err != nil {
		return 0, err
	}
	return info.Size, nil
}

// finishExportJob records the outcome of a job.
func (h *Handler) finishExportJob(id, bucket string, err error) {
	var job importJob
	h.exports.update(id, func(j *importJob) {
		now := time.Now().UTC()
		j.FinishedAt = &now
		switch {
		case errors.Is(err, context.Canceled):
			j.State = "canceled"
		case err != nil:
			j.State = "failed"
			_, j.Error = remoteErrorMessage(err)
			if code := minio.ToErrorResponse(err).Code; code == "BucketAlreadyExists" {
				j.Error = "The bucket name is taken on the remote by another account (bucket names are global on AWS S3)."
			}
		case j.Failed > 0 && j.Copied == 0 && j.Unchanged == 0:
			j.State = "failed"
			j.Error = "No object could be sent."
		default:
			j.State = "done"
		}
		j.cancel()
		job = *j
	})
	h.opts.Log.Info("export job finished", "job", id, "bucket", bucket, "state", job.State,
		"sent", job.Copied, "unchanged", job.Unchanged, "failed", job.Failed, "err", err)
}

// exportBucketSettings copies a bucket's versioning, tags and anonymous
// access to its new remote copy. Quotas are not sent (they live in each
// server's own admin API). It returns what was applied and notes on what was not.
func (h *Handler) exportBucketSettings(ctx context.Context, client *minio.Client, bucket string) (applied, notes []string) {
	b, err := h.opts.Store.GetBucket(ctx, bucket)
	if err != nil {
		return nil, []string{"The bucket's settings could not be read here."}
	}
	if b.Versioning == storage.VersioningEnabled || b.Versioning == storage.VersioningSuspended {
		cfg := minio.BucketVersioningConfiguration{Status: b.Versioning}
		if err := client.SetBucketVersioning(ctx, bucket, cfg); err != nil {
			notes = append(notes, "Versioning could not be set on the remote.")
		} else {
			applied = append(applied, "versioning "+strings.ToLower(b.Versioning))
		}
	}
	if m, err := h.opts.Store.BucketTags(ctx, bucket); err == nil && len(m) > 0 {
		t, err := tags.MapToBucketTags(m)
		if err == nil {
			err = client.SetBucketTagging(ctx, bucket, t)
		}
		if err != nil {
			notes = append(notes, "The tags could not be set on the remote.")
		} else {
			applied = append(applied, "tags")
		}
	}
	if p, err := auth.ParsePolicy(b.Policy); err == nil && p.Mode != "private" {
		doc, err := s3BucketPolicy(bucket, p)
		if err == nil {
			err = client.SetBucketPolicy(ctx, bucket, doc)
		}
		if err != nil {
			notes = append(notes, "The anonymous access could not be set on the remote (AWS accounts often block public "+
				"bucket policies); the remote bucket is private.")
		} else {
			applied = append(applied, "anonymous access")
		}
	}
	return applied, notes
}

// s3BucketPolicy writes bin's anonymous access as an S3 bucket policy, the
// form `mc anonymous set` and AWS use: reading a prefix also lists it, as in bin.
func s3BucketPolicy(bucket string, p auth.Policy) (string, error) {
	rules := p.Rules
	if p.Mode == "public" {
		rules = []auth.Rule{{Prefix: "", Access: auth.ReadOnly}}
	}
	type statement struct {
		Effect    string              `json:"Effect"`
		Principal map[string][]string `json:"Principal"`
		Action    []string            `json:"Action"`
		Resource  []string            `json:"Resource"`
		Condition map[string]any      `json:"Condition,omitempty"`
	}
	anyone := map[string][]string{"AWS": {"*"}}
	arn := "arn:aws:s3:::" + bucket
	var st []statement
	for _, r := range rules {
		res := arn + "/" + r.Prefix + "*"
		if r.Exact {
			res = arn + "/" + r.Prefix
		}
		var actions []string
		switch r.Access {
		case auth.ReadOnly:
			actions = []string{"s3:GetObject"}
		case auth.WriteOnly:
			actions = []string{"s3:PutObject"}
		case auth.ReadWrite:
			actions = []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"}
		}
		st = append(st, statement{Effect: "Allow", Principal: anyone, Action: actions, Resource: []string{res}})
		if r.Access != auth.WriteOnly && !r.Exact {
			list := statement{Effect: "Allow", Principal: anyone, Action: []string{"s3:ListBucket"}, Resource: []string{arn}}
			if r.Prefix != "" {
				list.Condition = map[string]any{"StringLike": map[string][]string{"s3:prefix": {r.Prefix + "*"}}}
			}
			st = append(st, list)
		}
	}
	doc, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": st})
	return string(doc), err
}
