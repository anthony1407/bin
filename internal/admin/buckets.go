package admin

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"bin/internal/auth"
	"bin/internal/cors"
	"bin/internal/server"
	"bin/internal/storage"
)

type bucketJSON struct {
	Name       string            `json:"name"`
	CreatedAt  time.Time         `json:"createdAt"`
	Objects    int64             `json:"objects"`
	Size       int64             `json:"size"`
	QuotaBytes int64             `json:"quotaBytes"`
	Policy     auth.Policy       `json:"policy"`
	Tags       map[string]string `json:"tags"`
	Versioning string            `json:"versioning"` // "", "Enabled" or "Suspended"
	CORS       []cors.Rule       `json:"cors"`
}

func (h *Handler) bucketJSON(r *http.Request, b storage.Bucket, u storage.Usage) bucketJSON {
	policy, _ := auth.ParsePolicy(b.Policy)
	tags, err := h.opts.Store.BucketTags(r.Context(), b.Name)
	if err != nil {
		tags = map[string]string{}
	}
	rules, _ := cors.Parse(b.CORS)
	if rules == nil {
		rules = []cors.Rule{}
	}
	return bucketJSON{
		Name: b.Name, CreatedAt: b.CreatedAt, Objects: u.Objects, Size: u.Bytes,
		QuotaBytes: b.QuotaBytes, Policy: policy, Tags: tags, Versioning: b.Versioning, CORS: rules,
	}
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.opts.Store.ListBuckets(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	usage, err := h.opts.Store.BucketUsage(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]bucketJSON, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, h.bucketJSON(r, b, usage[b.Name]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out})
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string       `json:"name"`
		Policy *auth.Policy `json:"policy"` // optional: anonymous access from the start
	}
	if !readJSON(w, r, &in) {
		return
	}
	setAuditBucket(r, in.Name)
	if in.Policy != nil {
		if err := in.Policy.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
			return
		}
	}
	if err := h.opts.Store.CreateBucket(r.Context(), in.Name); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("bucket created from the console", "bucket", in.Name)
	if in.Policy != nil && in.Policy.Mode != "private" {
		if err := h.opts.Store.SetBucketPolicy(r.Context(), in.Name, in.Policy.String()); err != nil {
			h.storageError(w, r, err)
			return
		}
		h.opts.Log.Warn("bucket opened to anonymous access from the console", "bucket", in.Name, "mode", in.Policy.Mode)
	}
	b, _ := h.opts.Store.GetBucket(r.Context(), in.Name)
	writeJSON(w, http.StatusCreated, h.bucketJSON(r, b, storage.Usage{}))
}

func (h *Handler) getBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	b, err := h.opts.Store.GetBucket(r.Context(), name)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	usage, err := h.opts.Store.BucketUsage(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.bucketJSON(r, b, usage[name]))
}

// deleteBucket removes a bucket; with ?force=true it first deletes every
// object in it, older versions and delete markers included.
func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	if r.URL.Query().Get("force") == "true" {
		n, err := h.opts.Store.PurgePrefix(r.Context(), name, "")
		if err != nil {
			h.storageError(w, r, err)
			return
		}
		server.Annotate(r.Context(), slog.Int("objects_deleted", n))
	}
	if err := h.opts.Store.DeleteBucket(r.Context(), name); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("bucket deleted from the console", "bucket", name)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) putTags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tags map[string]string `json:"tags"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Tags) > 50 {
		writeError(w, http.StatusBadRequest, "too_many_tags", "A bucket can have at most 50 tags.")
		return
	}
	for k, v := range in.Tags {
		if k == "" || len(k) > 128 || len(v) > 256 {
			writeError(w, http.StatusBadRequest, "invalid_tag", "Tag keys need 1-128 characters and values at most 256.")
			return
		}
	}
	if err := h.opts.Store.SetBucketTags(r.Context(), r.PathValue("bucket"), in.Tags); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.getBucket(w, r)
}

func (h *Handler) putQuota(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Bytes int64 `json:"bytes"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Bytes < 0 {
		writeError(w, http.StatusBadRequest, "invalid_quota", "The quota must be zero (no limit) or a positive number of bytes.")
		return
	}
	if err := h.opts.Store.SetBucketQuota(r.Context(), r.PathValue("bucket"), in.Bytes); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.getBucket(w, r)
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request) {
	var in auth.Policy
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	name := r.PathValue("bucket")
	if err := h.opts.Store.SetBucketPolicy(r.Context(), name, in.String()); err != nil {
		h.storageError(w, r, err)
		return
	}
	if in.Mode != "private" {
		h.opts.Log.Warn("bucket opened to anonymous access from the console", "bucket", name, "mode", in.Mode)
	}
	h.getBucket(w, r)
}

// putVersioning turns versioning on ("Enabled") or pauses it ("Suspended").
// A versioned bucket can never go back to unversioned, as in S3.
func (h *Handler) putVersioning(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := r.PathValue("bucket")
	if err := h.opts.Store.SetBucketVersioning(r.Context(), name, in.Status); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("bucket versioning changed from the console", "bucket", name, "status", in.Status)
	h.getBucket(w, r)
}

// putCORS replaces the bucket's CORS rules; an empty list removes them.
func (h *Handler) putCORS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules []cors.Rule `json:"rules"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Rules) > 0 {
		if err := cors.Validate(in.Rules); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cors", strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
			return
		}
	}
	name := r.PathValue("bucket")
	if err := h.opts.Store.SetBucketCORS(r.Context(), name, cors.Encode(in.Rules)); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("bucket CORS rules changed from the console", "bucket", name, "rules", len(in.Rules))
	h.getBucket(w, r)
}
