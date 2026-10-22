// Package admin implements the console listener: the JSON admin API under
// /api/v1 and, later, the embedded web panel.
package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"bin/internal/buildinfo"
	"bin/internal/server"
)

// Options configures the console handler.
type Options struct {
	Log *slog.Logger
}

// NewHandler returns the console HTTP handler.
func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", health)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "Unknown API endpoint."})
	})
	mux.HandleFunc("/", placeholder)

	var next http.Handler = mux
	next = server.Recover(opts.Log, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: "Internal server error."})
	}, next)
	next = server.AccessLog(opts.Log, next)
	return server.WithRequestID("X-Request-Id", next)
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": buildinfo.Version})
}

// placeholder stands in for the web panel until it is built.
func placeholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<!doctype html><title>bin</title><p>bin console: the panel is not built yet.</p>"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
