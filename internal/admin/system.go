package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

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
		"version":    buildinfo.Version,
		"commit":     buildinfo.Commit,
		"build":      buildinfo.String(),
		"region":     h.opts.Region,
		"dataDir":    h.opts.DataDir,
		"s3Endpoint": h.s3BaseURL(r),
		"startedAt":  h.started.UTC(),
		"uptime":     int64(time.Since(h.started).Seconds()),
		"buckets":    len(usage),
		"objects":    objects,
		"bytes":      bytes,
		"disk":       disk,
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
	ShareMaxExpiry int64  `json:"shareMaxExpirySeconds"`
	PublicURL      string `json:"publicUrl"` // read-only: BIN_PUBLIC_URL
	Region         string `json:"region"`    // read-only: BIN_REGION
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	max, err := h.shareMaxExpiry(r)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON{ShareMaxExpiry: int64(max / time.Second), PublicURL: h.opts.PublicURL, Region: h.opts.Region})
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ShareMaxExpiry int64 `json:"shareMaxExpirySeconds"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.ShareMaxExpiry < 60 || time.Duration(in.ShareMaxExpiry)*time.Second > defaultShareMaxExpiry {
		writeError(w, http.StatusBadRequest, "invalid_share_expiry", "The maximum share link expiry must be between 1 minute and 7 days.")
		return
	}
	if err := h.opts.Store.SetSetting(r.Context(), settingShareMaxExpiry, strconv.FormatInt(in.ShareMaxExpiry, 10)); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("settings updated", "share_max_expiry", (time.Duration(in.ShareMaxExpiry) * time.Second).String())
	h.getSettings(w, r)
}

// revokeShareLinks rotates the internal share key: every link issued so far
// stops working.
func (h *Handler) revokeShareLinks(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.opts.Keys.RotateShareKey(r.Context()); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Warn("all share links revoked from the console")
	w.WriteHeader(http.StatusNoContent)
}

// --- integrity scan ---------------------------------------------------------------------

type corruptJSON struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

type scrubStatus struct {
	State      string        `json:"state"` // idle, running, done, failed
	Bucket     string        `json:"bucket,omitempty"`
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

// startScrub reads every object (of one bucket, or all) in the background,
// verifying its SHA-256; GET /scrub reports progress and corrupt objects.
func (h *Handler) startScrub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Bucket string `json:"bucket"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Bucket != "" {
		if _, err := h.opts.Store.GetBucket(r.Context(), in.Bucket); err != nil {
			h.storageError(w, r, err)
			return
		}
	}
	h.scrubMu.Lock()
	if h.scrub.State == "running" {
		h.scrubMu.Unlock()
		writeError(w, http.StatusConflict, "scrub_running", "An integrity scan is already running.")
		return
	}
	now := time.Now().UTC()
	h.scrub = scrubStatus{State: "running", Bucket: in.Bucket, StartedAt: &now}
	h.scrubMu.Unlock()

	h.opts.Log.Info("integrity scan started", "bucket", in.Bucket)
	go func() {
		res, err := h.opts.Store.Scrub(context.Background(), in.Bucket, func(p storage.ScrubResult) {
			h.scrubMu.Lock()
			h.scrub.Objects, h.scrub.Bytes = p.Objects, p.Bytes
			h.scrubMu.Unlock()
		})
		done := time.Now().UTC()
		h.scrubMu.Lock()
		defer h.scrubMu.Unlock()
		h.scrub.FinishedAt, h.scrub.Objects, h.scrub.Bytes = &done, res.Objects, res.Bytes
		for _, c := range res.Corrupt {
			h.scrub.Corrupt = append(h.scrub.Corrupt, corruptJSON{Bucket: c.Bucket, Key: c.Key})
			h.opts.Log.Error("integrity scan: corrupt object", "bucket", c.Bucket, "key", c.Key)
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
