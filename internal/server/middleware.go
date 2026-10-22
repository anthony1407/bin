package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type ctxKey struct{}

// RequestID returns the ID assigned to the request by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
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
		id := NewRequestID()
		w.Header().Set(header, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
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

// AccessLog logs one line per request once the handler returns.
func AccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("id", RequestID(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.EscapedPath()),
			slog.String("query", r.URL.RawQuery),
			slog.Int("status", sw.status),
			slog.Int64("bytes", sw.bytes),
			slog.Duration("took", time.Since(start)),
			slog.String("remote", r.RemoteAddr),
		)
	})
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
