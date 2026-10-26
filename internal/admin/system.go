package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bin/internal/audit"
	"bin/internal/buildinfo"
	"bin/internal/storage"
)

// --- server information ------------------------------------------------------------

func (h *Handler) info(w http.ResponseWriter, r *http.Request) {
	usage, err := h.opts.Store.BucketUsage(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	var objects, bytes int64
	for _, u := range usage {
		objects += u.Objects
		bytes += u.Bytes
	}
	disk := map[string]any{}
	if total, free, err := diskSpace(h.opts.DataDir); err == nil {
		disk["total"], disk["free"] = total, free
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     buildinfo.Version,
		"commit":      buildinfo.Commit,
		"build":       buildinfo.String(),
		"region":      h.opts.Region,
		"dataDir":     h.opts.DataDir,
		"s3Endpoint":  h.s3BaseURL(r),
		"s3Addr":      h.opts.S3Addr,
		"publicUrl":   h.opts.PublicURL,
		"logRequests": h.opts.LogRequests,
		"logLevel":    h.opts.LogLevel,
		"logFormat":   h.opts.LogFormat,
		"isTLS":       r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		"startedAt":   h.started.UTC(),
		"uptime":      int64(time.Since(h.started).Seconds()),
		"buckets":     len(usage),
		"objects":     objects,
		"bytes":       bytes,
		"disk":        disk,
	})
}

// --- settings ------------------------------------------------------------------------

const (
	settingShareMaxExpiry = "share_max_expiry_seconds"
	defaultShareMaxExpiry = 7 * 24 * time.Hour // also the S3 limit for presigned URLs
)

func (h *Handler) shareMaxExpiry(r *http.Request) (time.Duration, error) {
	v, ok, err := h.opts.Store.Setting(r.Context(), settingShareMaxExpiry)
	if err != nil || !ok {
		return defaultShareMaxExpiry, err
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs <= 0 {
		return defaultShareMaxExpiry, nil
	}
	return time.Duration(secs) * time.Second, nil
}

type settingsJSON struct {
	ShareMaxExpiry     int64  `json:"shareMaxExpirySeconds"`
	AuditRetentionDays int    `json:"auditRetentionDays"` // 0: keep forever
	PublicURL          string `json:"publicUrl"`          // read-only: BIN_PUBLIC_URL
	Region             string `json:"region"`             // read-only: BIN_REGION
}

// maxAuditRetentionDays bounds the audit retention setting (ten years).
const maxAuditRetentionDays = 3650

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	max, err := h.shareMaxExpiry(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	days, err := audit.RetentionDays(r.Context(), h.opts.Store)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON{
		ShareMaxExpiry: int64(max / time.Second), AuditRetentionDays: days,
		PublicURL: h.opts.PublicURL, Region: h.opts.Region,
	})
}

// putSettings changes the settings present in the body.
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ShareMaxExpiry     *int64 `json:"shareMaxExpirySeconds"`
		AuditRetentionDays *int   `json:"auditRetentionDays"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if v := in.ShareMaxExpiry; v != nil && (*v < 60 || time.Duration(*v)*time.Second > defaultShareMaxExpiry) {
		writeError(w, http.StatusBadRequest, "invalid_share_expiry", "The maximum share link expiry must be between 1 minute and 7 days.")
		return
	}
	if v := in.AuditRetentionDays; v != nil && (*v < 0 || *v > maxAuditRetentionDays) {
		writeError(w, http.StatusBadRequest, "invalid_audit_retention", "The audit retention must be between 0 (forever) and 3650 days.")
		return
	}
	if v := in.ShareMaxExpiry; v != nil {
		if err := h.opts.Store.SetSetting(r.Context(), settingShareMaxExpiry, strconv.FormatInt(*v, 10)); err != nil {
			h.storageError(w, r, err)
			return
		}
		h.opts.Log.Info("settings updated", "share_max_expiry", (time.Duration(*v) * time.Second).String())
	}
	if v := in.AuditRetentionDays; v != nil {
		if err := h.opts.Store.SetSetting(r.Context(), audit.RetentionSetting, strconv.Itoa(*v)); err != nil {
			h.storageError(w, r, err)
			return
		}
		h.opts.Log.Info("settings updated", "audit_retention_days", *v)
	}
	h.getSettings(w, r)
}

// revokeShareLinks rotates the internal share key: every link issued so far
// stops working.
func (h *Handler) revokeShareLinks(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.opts.Keys.RotateShareKey(r.Context()); err != nil {
		h.storageError(w, r, err)
		return
	}
	// The rotation already stops them; the records say so too.
	if err := h.opts.Store.RevokeAllShareLinks(r.Context()); err != nil {
		h.opts.Log.Warn("marking share links revoked", "err", err)
	}
	h.opts.Log.Warn("all share links revoked from the console")
	w.WriteHeader(http.StatusNoContent)
}

// --- integrity scan ---------------------------------------------------------------------

type corruptJSON struct {
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	VersionID string `json:"versionId,omitempty"` // left out for unversioned objects
}

type scrubStatus struct {
	State      string        `json:"state"`             // idle, running, done, failed
	Buckets    []string      `json:"buckets,omitempty"` // empty: all buckets
	StartedAt  *time.Time    `json:"startedAt,omitempty"`
	FinishedAt *time.Time    `json:"finishedAt,omitempty"`
	Objects    int64         `json:"objects"`
	Bytes      int64         `json:"bytes"`
	Corrupt    []corruptJSON `json:"corrupt"`
	Error      string        `json:"error,omitempty"`
}

func (h *Handler) scrubStatus(w http.ResponseWriter, r *http.Request) {
	h.scrubMu.Lock()
	s := h.scrub
	h.scrubMu.Unlock()
	if s.State == "" {
		s.State = "idle"
	}
	if s.Corrupt == nil {
		s.Corrupt = []corruptJSON{}
	}
	writeJSON(w, http.StatusOK, s)
}

// startScrub reads every object (of the chosen buckets, or all) in the
// background, verifying its SHA-256; GET /scrub reports progress and corrupt
// objects.
func (h *Handler) startScrub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Buckets []string `json:"buckets"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var buckets []string
	seen := map[string]bool{}
	for _, b := range in.Buckets {
		if seen[b] {
			continue
		}
		seen[b] = true
		if _, err := h.opts.Store.GetBucket(r.Context(), b); err != nil {
			h.storageError(w, r, err)
			return
		}
		buckets = append(buckets, b)
	}
	h.scrubMu.Lock()
	if h.scrub.State == "running" {
		h.scrubMu.Unlock()
		writeError(w, http.StatusConflict, "scrub_running", "An integrity scan is already running.")
		return
	}
	now := time.Now().UTC()
	h.scrub = scrubStatus{State: "running", Buckets: buckets, StartedAt: &now}
	h.scrubMu.Unlock()

	scope := "all"
	if len(buckets) > 0 {
		scope = strings.Join(buckets, ",")
	}
	h.opts.Log.Info("integrity scan started", "buckets", scope)
	go func() {
		res, err := h.opts.Store.Scrub(context.Background(), buckets, func(p storage.ScrubResult) {
			h.scrubMu.Lock()
			h.scrub.Objects, h.scrub.Bytes = p.Objects, p.Bytes
			h.scrubMu.Unlock()
		})
		done := time.Now().UTC()
		h.scrubMu.Lock()
		defer h.scrubMu.Unlock()
		h.scrub.FinishedAt, h.scrub.Objects, h.scrub.Bytes = &done, res.Objects, res.Bytes
		for _, c := range res.Corrupt {
			cj := corruptJSON{Bucket: c.Bucket, Key: c.Key}
			if c.VersionID != storage.NullVersion {
				cj.VersionID = c.VersionID
			}
			h.scrub.Corrupt = append(h.scrub.Corrupt, cj)
			h.opts.Log.Error("integrity scan: corrupt object", "bucket", c.Bucket, "key", c.Key, "version", c.VersionID)
		}
		if err != nil {
			h.scrub.State, h.scrub.Error = "failed", err.Error()
			h.opts.Log.Error("integrity scan failed", "err", err)
			return
		}
		h.scrub.State = "done"
		h.opts.Log.Info("integrity scan finished", "objects", res.Objects, "corrupt", len(res.Corrupt))
	}()
	h.scrubStatus(w, r)
}

// --- speedtest benchmark -------------------------------------------------------------

func (h *Handler) speedtestPing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// maxSpeedtestBytes bounds both directions of the speed test.
const maxSpeedtestBytes = 250 << 20

func (h *Handler) speedtestDownload(w http.ResponseWriter, r *http.Request) {
	sizeStr := r.URL.Query().Get("size")
	size, _ := strconv.ParseInt(sizeStr, 10, 64)
	if size <= 0 || size > maxSpeedtestBytes {
		size = 10 * 1024 * 1024 // 10 MiB default
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")

	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = byte(i % 256)
	}

	flusher, _ := w.(http.Flusher)
	remaining := size
	for remaining > 0 {
		toWrite := int64(len(buf))
		if toWrite > remaining {
			toWrite = remaining
		}
		n, err := w.Write(buf[:toWrite])
		if err != nil {
			return
		}
		remaining -= int64(n)
		if flusher != nil && remaining%(512*1024) == 0 {
			flusher.Flush()
		}
	}
}

func (h *Handler) speedtestUpload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxSpeedtestBytes))
	if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Speed test uploads are limited to 250 MiB.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload_failed", "The upload was interrupted.")
		return
	}
	elapsed := time.Since(start)
	mbPerSec := 0.0
	if elapsed.Seconds() > 0 {
		mbPerSec = (float64(n) / (1024 * 1024)) / elapsed.Seconds()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bytes":    n,
		"duration": elapsed.Milliseconds(),
		"mbps":     mbPerSec * 8,
		"mBps":     mbPerSec,
	})
}

// --- server log --------------------------------------------------------------------------

// maxLogPage bounds one GET /logs answer.
const maxLogPage = 1000

// listLogs returns the server log lines after ?after= (a sequence number;
// 0 or missing: the latest ones), oldest first. The console polls it.
func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	if h.opts.LogRing == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "latest": 0})
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit := maxLogPage
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v < limit {
		limit = v
	}
	entries, latest := h.opts.LogRing.Since(after, limit)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "latest": latest})
}
