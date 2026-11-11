package s3

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"

	"bin/internal/audit"
	"bin/internal/s3/s3err"
	"bin/internal/server"
	"bin/internal/storage"
)

// auditedOps are the operations recorded in the audit log: the ones that
// change data. Reads are not recorded, but any refused request (403) is.
var auditedOps = map[Op]bool{
	OpCreateBucket: true, OpDeleteBucket: true, OpPutBucketTagging: true, OpDeleteBucketTagging: true,
	OpPutBucketVersioning: true, OpPutBucketCors: true, OpDeleteBucketCors: true,
	OpPutObject: true, OpCopyObject: true, OpCompleteMultipartUpload: true, OpAbortMultipartUpload: true,
	OpDeleteObject: true, OpDeleteObjects: true,
}

// unauthenticated is the actor of requests whose signature did not verify.
const unauthenticated = "(unauthenticated)"

// auditExtra carries what a handler learns for the audit entry of its
// request: the size of the object it stored.
type auditExtra struct{ size int64 }

type auditExtraKey struct{}

// auditSize records the bytes a write stored, for its audit entry.
func auditSize(ctx context.Context, n int64) {
	if x, ok := ctx.Value(auditExtraKey{}).(*auditExtra); ok {
		x.size = n
	}
}

// statusRecorder remembers the status code written to the client, and how
// many body bytes (a download's size).
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

// ReadFrom passes zero-copy body writes through; see server.ReadFrom.
func (w *statusRecorder) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := server.ReadFrom(w.ResponseWriter, r)
	w.written += n
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and friends.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// record adds the request to the audit log if it is worth keeping: writes,
// refusals, and downloads when the setting asks for them. size is the bytes
// the request stored or sent.
func (h *Handler) record(r *http.Request, req Request, actor string, status int, err error, size int64) {
	download := req.Op == OpGetObject && status < 400
	if h.opts.Audit == nil || !(auditedOps[req.Op] || status == http.StatusForbidden ||
		(download && h.opts.Audit.DownloadsOn(r.Context()))) {
		return
	}
	e := storage.AuditEntry{
		Source:    audit.S3,
		Actor:     actor,
		Action:    string(req.Op),
		Bucket:    req.Bucket,
		Resource:  req.Key,
		Status:    status,
		Remote:    remoteIP(r),
		RequestID: server.RequestID(r.Context()),
	}
	if status < 400 {
		e.Size = size
	}
	if e.Action == "" {
		e.Action = r.Method
	}
	if req.Op == OpCopyObject {
		e.Resource += " (from " + r.Header.Get("X-Amz-Copy-Source") + ")"
	}
	if v := r.URL.Query().Get("versionId"); v != "" && req.Op == OpDeleteObject {
		e.Resource += " (version " + v + ")"
	}
	var apiErr *s3err.Error
	if errors.As(err, &apiErr) {
		e.Error = apiErr.Code
	} else if err != nil {
		e.Error = "InternalError"
	}
	h.opts.Audit.Record(e)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
