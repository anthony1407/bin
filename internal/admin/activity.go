package admin

import (
	"net/http"
	"time"

	"bin/internal/audit"
	"bin/internal/storage"
)

// A user's activity, for admins: the buckets they created and what those
// hold, their upload (and, when recorded, download) totals, and their latest
// actions. Everything about actions comes from the audit log, so it covers
// what its retention keeps; the buckets come from the buckets themselves.

type activityBucketJSON struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Objects   int64     `json:"objects"`
	Size      int64     `json:"size"`
	Owned     bool      `json:"owned"` // still theirs (false once the key was recreated, etc.)
}

type activityJSON struct {
	Buckets       []activityBucketJSON `json:"buckets"`
	Uploads       int64                `json:"uploads"`
	UploadBytes   int64                `json:"uploadBytes"`
	Downloads     int64                `json:"downloads"`
	DownloadBytes int64                `json:"downloadBytes"`
	LastActivity  *time.Time           `json:"lastActivity"`
	Recent        []auditJSON          `json:"recent"`
	// What the numbers can cover: the audit retention (0: forever) and
	// whether downloads are recorded at all.
	RetentionDays     int  `json:"retentionDays"`
	DownloadsRecorded bool `json:"downloadsRecorded"`
}

// maxActivityRecent bounds the latest actions shown with a user's activity.
const maxActivityRecent = 50

func (h *Handler) keyActivity(w http.ResponseWriter, r *http.Request) {
	ak := r.PathValue("key")
	if _, err := h.opts.Store.GetAccessKey(r.Context(), ak); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Audit.Flush() // include what was just done
	out := activityJSON{Buckets: []activityBucketJSON{}, Recent: []auditJSON{}}

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
	for _, b := range buckets {
		if b.CreatedBy == ak {
			u := usage[b.Name]
			out.Buckets = append(out.Buckets, activityBucketJSON{
				Name: b.Name, CreatedAt: b.CreatedAt, Objects: u.Objects, Size: u.Bytes, Owned: b.OwnerKey == ak,
			})
		}
	}

	totals, err := h.opts.Store.AuditTotals(r.Context(), ak)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out.Uploads, out.UploadBytes = totals.Uploads, totals.UploadBytes
	out.Downloads, out.DownloadBytes = totals.Downloads, totals.DownloadBytes
	out.LastActivity = optionalTime(totals.LastActivity)

	recent, err := h.opts.Store.ListAudit(r.Context(), storage.AuditFilter{Actor: ak, Limit: maxActivityRecent})
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	for _, e := range recent {
		out.Recent = append(out.Recent, auditJSON{
			ID: e.ID, At: e.At, Source: e.Source, Actor: e.Actor, Action: e.Action, Bucket: e.Bucket,
			Resource: e.Resource, Status: e.Status, Error: e.Error, Remote: e.Remote, Size: e.Size,
		})
	}
	if out.RetentionDays, err = audit.RetentionDays(r.Context(), h.opts.Store); err != nil {
		h.storageError(w, r, err)
		return
	}
	out.DownloadsRecorded = h.opts.Audit.DownloadsOn(r.Context())
	writeJSON(w, http.StatusOK, out)
}
