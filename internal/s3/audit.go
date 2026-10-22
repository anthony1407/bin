package s3

import (
	"errors"
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
	OpPutObject: true, OpCopyObject: true, OpCompleteMultipartUpload: true, OpAbortMultipartUpload: true,
	OpDeleteObject: true, OpDeleteObjects: true,
}

// unauthenticated is the actor of requests whose signature did not verify.
const unauthenticated = "(unauthenticated)"

// statusRecorder remembers the status code written to the client.
type statusRecorder struct {
	http.ResponseWriter
	status int
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
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach Flush and friends.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// record adds the request to the audit log if it is worth keeping.
func (h *Handler) record(r *http.Request, req Request, actor string, status int, err error) {
	if h.opts.Audit == nil || !(auditedOps[req.Op] || status == http.StatusForbidden) {
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
	if e.Action == "" {
		e.Action = r.Method
	}
	if req.Op == OpCopyObject {
		e.Resource += " (from " + r.Header.Get("X-Amz-Copy-Source") + ")"
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
