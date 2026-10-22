package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"bin/internal/logging"
)

type ctxKey struct{}

// requestInfo travels in the request context: the request ID and any
// attributes handlers add for the access log line.
type requestInfo struct {
	id    string
	mu    sync.Mutex
	attrs []slog.Attr
}

func infoFrom(ctx context.Context) *requestInfo {
	ri, _ := ctx.Value(ctxKey{}).(*requestInfo)
	return ri
}

// RequestID returns the ID assigned to the request by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	if ri := infoFrom(ctx); ri != nil {
		return ri.id
	}
	return ""
}

// Annotate adds attributes to the request's access log line, e.g. the S3
// operation or the error code returned. It is a no-op outside a request.
func Annotate(ctx context.Context, attrs ...slog.Attr) {
	if ri := infoFrom(ctx); ri != nil {
		ri.mu.Lock()
		ri.attrs = append(ri.attrs, attrs...)
		ri.mu.Unlock()
	}
}

// NewRequestID returns 16 uppercase hex characters, the same shape AWS uses
// for x-amz-request-id.
func NewRequestID() string {
	var b [8]byte
	rand.Read(b[:])
	return strings.ToUpper(hex.EncodeToString(b[:]))
}

// WithRequestID assigns a request ID, stores it in the context and echoes it
// in the given response header.
func WithRequestID(header string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := &requestInfo{id: NewRequestID()}
		w.Header().Set(header, ri.id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, ri)))
	})
}

// statusWriter records the status code and body size for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and friends.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// LogOptions controls AccessLog.
type LogOptions struct {
	// Requests logs every request; when false only server errors are logged.
	Requests bool
	// Quiet marks routine requests (health probes) that are logged at debug level.
	Quiet func(*http.Request) bool
}

// AccessLog logs one line per request once the handler returns:
//
//	GET /photos/a.jpg -> 200 (sent 1.2 KB, 3ms) op=GetObject id=... remote=...
//
// Server errors are logged at ERROR, everything else at INFO (or DEBUG for
// quiet requests). Requests aborted mid-response are logged as such.
func AccessLog(log *slog.Logger, opts LogOptions, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		aborted := true
		defer func() {
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case aborted || sw.status >= 500:
				level = slog.LevelError
			case !opts.Requests:
				return
			case opts.Quiet != nil && opts.Quiet(r):
				level = slog.LevelDebug
			}
			if !log.Enabled(r.Context(), level) {
				return
			}
			target := r.URL.EscapedPath()
			if q := redactQuery(r.URL.RawQuery); q != "" {
				target += "?" + q
			}
			var detail string
			switch {
			case r.ContentLength > 0:
				detail = "received " + logging.Size(r.ContentLength) + ", "
			case sw.bytes > 0:
				detail = "sent " + logging.Size(sw.bytes) + ", "
			}
			msg := fmt.Sprintf("%s %s -> %d (%s%s)", r.Method, target, sw.status, detail, time.Since(start).Round(time.Millisecond))
			if aborted {
				msg += " ABORTED: connection cut before the response completed"
			}
			attrs := []slog.Attr{slog.String("id", RequestID(r.Context())), slog.String("remote", r.RemoteAddr)}
			if ri := infoFrom(r.Context()); ri != nil {
				ri.mu.Lock()
				attrs = append(ri.attrs, attrs...)
				ri.mu.Unlock()
			}
			log.LogAttrs(r.Context(), level, msg, attrs...)
		}()
		next.ServeHTTP(sw, r)
		aborted = false
	})
}

// secretParams are query parameters whose values must never reach the logs:
// a logged presigned-URL signature can be replayed until the URL expires.
var secretParams = []string{"X-Amz-Signature", "X-Amz-Security-Token"}

// redactQuery replaces the values of secretParams with "REDACTED", leaving the
// rest of the raw query untouched.
func redactQuery(raw string) string {
	if raw == "" {
		return raw
	}
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		name, _, _ := strings.Cut(p, "=")
		for _, s := range secretParams {
			if strings.EqualFold(name, s) {
				parts[i] = name + "=REDACTED"
			}
		}
	}
	return strings.Join(parts, "&")
}

// Recover turns a panic into a logged error and a response rendered by onPanic,
// so a bug in one handler never takes down the process.
func Recover(log *slog.Logger, onPanic http.HandlerFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v) // deliberate connection abort, let net/http handle it
			}
			log.Error("panic in handler", "id", RequestID(r.Context()), "panic", v, "stack", string(debug.Stack()))
			onPanic(w, r)
		}()
		next.ServeHTTP(w, r)
	})
}
