// Package s3 implements bin's S3-compatible HTTP API.
package s3

import (
	"errors"
	"log/slog"
	"net/http"

	"bin/internal/s3/s3err"
	"bin/internal/server"
)

// Options configures the S3 handler.
type Options struct {
	Domain string // virtual-host domain; empty = path style only
	HostID string // stable instance identifier, sent as x-amz-id-2 and <HostId>
	Log    *slog.Logger
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
	return &Handler{opts: opts, ops: map[Op]opFunc{}}
}

func (h *Handler) withMiddleware() http.Handler {
	var next http.Handler = http.HandlerFunc(h.serve)
	next = server.Recover(h.opts.Log, h.panicResponse, next)
	next = server.AccessLog(h.opts.Log, next)
	return server.WithRequestID("X-Amz-Request-Id", next)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "bin")
	w.Header().Set("X-Amz-Id-2", h.opts.HostID)

	req, err := Resolve(r, h.opts.Domain)
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
	if !errors.As(err, &apiErr) {
		h.opts.Log.Error("internal error", "id", server.RequestID(r.Context()), "op", req.Op, "err", err)
	}
	s3err.Write(w, r, err, s3err.Resource{Bucket: req.Bucket, Key: req.Key},
		server.RequestID(r.Context()), h.opts.HostID)
}

func (h *Handler) panicResponse(w http.ResponseWriter, r *http.Request) {
	bucket, key := parseTarget(r, h.opts.Domain)
	s3err.Write(w, r, s3err.ErrInternalError, s3err.Resource{Bucket: bucket, Key: key},
		server.RequestID(r.Context()), h.opts.HostID)
}
