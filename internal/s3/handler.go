// Package s3 implements bin's S3-compatible HTTP API.
package s3

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"bin/internal/s3/s3err"
	"bin/internal/s3/sigv4"
	"bin/internal/server"
	"bin/internal/storage"
)

// Options configures the S3 handler.
type Options struct {
	Store  *storage.Store
	Region string
	Domain string          // virtual-host domain; empty = path style only
	HostID string          // stable instance identifier, sent as x-amz-id-2 and <HostId>
	Owner  Owner           // reported as the owner of every bucket
	Auth   *sigv4.Verifier // authenticates every request
	Log    *slog.Logger
	// LogRequests logs one line per request (server errors are always logged).
	LogRequests bool
}

// Owner identifies the account that owns the buckets.
type Owner struct {
	ID          string
	DisplayName string
}

// opFunc implements one operation. It must either write a complete response
// and return nil, or write nothing and return an error to be rendered as S3 XML.
type opFunc func(w http.ResponseWriter, r *http.Request, req Request) error

// Handler serves the S3 API.
type Handler struct {
	opts Options
	ops  map[Op]opFunc
}

// NewHandler returns the S3 API wrapped in request-ID, access-log and
// panic-recovery middleware.
func NewHandler(opts Options) http.Handler {
	return newHandler(opts).withMiddleware()
}

func newHandler(opts Options) *Handler {
	h := &Handler{opts: opts}
	h.ops = map[Op]opFunc{
		OpListBuckets:         h.listBuckets,
		OpCreateBucket:        h.createBucket,
		OpHeadBucket:          h.headBucket,
		OpDeleteBucket:        h.deleteBucket,
		OpGetBucketLocation:   h.getBucketLocation,
		OpGetBucketVersioning: h.getBucketVersioning,
		OpGetBucketTagging:    h.getBucketTagging,
		OpPutBucketTagging:    h.putBucketTagging,
		OpDeleteBucketTagging: h.deleteBucketTagging,
		OpPutObject:           h.putObject,
		OpGetObject:           h.getObject,
		OpHeadObject:          h.headObject,
		OpDeleteObject:        h.deleteObject,
		OpCopyObject:          h.copyObject,
		OpListObjectsV2:       h.listObjectsV2,
		OpListObjects:         h.listObjectsV1,

		OpCreateMultipartUpload:   h.createMultipartUpload,
		OpUploadPart:              h.uploadPart,
		OpUploadPartCopy:          h.uploadPartCopy,
		OpCompleteMultipartUpload: h.completeMultipartUpload,
		OpAbortMultipartUpload:    h.abortMultipartUpload,
		OpListParts:               h.listParts,
		OpListMultipartUploads:    h.listMultipartUploads,
		OpDeleteObjects:           h.deleteObjects,
	}
	return h
}

func (h *Handler) withMiddleware() http.Handler {
	var next http.Handler = http.HandlerFunc(h.serve)
	next = server.Recover(h.opts.Log, h.panicResponse, next)
	next = server.AccessLog(h.opts.Log, server.LogOptions{Requests: h.opts.LogRequests}, next)
	return server.WithRequestID("X-Amz-Request-Id", next)
}

type authKey struct{}

// AccessKey returns the access key that signed the request, or "" if anonymous.
func AccessKey(ctx context.Context) string {
	key, _ := ctx.Value(authKey{}).(string)
	return key
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "bin")
	w.Header().Set("X-Amz-Id-2", h.opts.HostID)

	// Authenticate before routing, so unauthenticated callers only ever see
	// authentication errors.
	bucket, key := parseTarget(r, h.opts.Domain)
	req := Request{Bucket: bucket, Key: key}
	auth, err := h.opts.Auth.Verify(r)
	if err == nil && auth.Anonymous() {
		err = s3err.ErrAccessDenied // anonymous access rules arrive with bucket policies
	}
	if err == nil {
		r = r.WithContext(context.WithValue(r.Context(), authKey{}, auth.AccessKey))
		server.Annotate(r.Context(), slog.String("access_key", auth.AccessKey))
		req, err = Resolve(r, h.opts.Domain)
	}
	if req.Op != "" {
		server.Annotate(r.Context(), slog.String("op", string(req.Op)))
	}
	if err == nil {
		if fn := h.ops[req.Op]; fn != nil {
			err = fn(w, r, req)
		} else {
			err = s3err.ErrNotImplemented
		}
	}
	if err != nil {
		h.writeError(w, r, req, err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, req Request, err error) {
	var apiErr *s3err.Error
	if errors.As(err, &apiErr) {
		server.Annotate(r.Context(), slog.String("error", apiErr.Code))
	} else {
		server.Annotate(r.Context(), slog.String("error", "InternalError"), slog.String("cause", err.Error()))
	}
	s3err.Write(w, r, err, s3err.Resource{Bucket: req.Bucket, Key: req.Key},
		server.RequestID(r.Context()), h.opts.HostID)
}

func (h *Handler) panicResponse(w http.ResponseWriter, r *http.Request) {
	bucket, key := parseTarget(r, h.opts.Domain)
	s3err.Write(w, r, s3err.ErrInternalError, s3err.Resource{Bucket: bucket, Key: key},
		server.RequestID(r.Context()), h.opts.HostID)
}
