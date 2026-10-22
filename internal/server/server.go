// Package server runs bin's HTTP listeners (S3 API and console) and provides
// the middleware they share: request IDs, access logging and panic recovery.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Listener is one HTTP endpoint served by the process.
type Listener struct {
	Name    string // used in logs: "s3", "console"
	Addr    string
	Handler http.Handler
}

// shutdownTimeout bounds how long in-flight requests may run after a stop signal.
const shutdownTimeout = 15 * time.Second

// Run binds every listener, serves until ctx is cancelled or one of them fails,
// then shuts all of them down gracefully. Binding happens before serving starts,
// so a busy port is reported immediately instead of half-starting the process.
func Run(ctx context.Context, log *slog.Logger, listeners ...Listener) error {
	type bound struct {
		Listener
		ln  net.Listener
		srv *http.Server
	}
	var all []bound
	for _, l := range listeners {
		ln, err := net.Listen("tcp", l.Addr)
		if err != nil {
			for _, b := range all {
				b.ln.Close()
			}
			return fmt.Errorf("%s: listen on %s: %w", l.Name, l.Addr, err)
		}
		all = append(all, bound{l, ln, newHTTPServer(l.Handler, log, l.Name)})
	}

	errc := make(chan error, len(all))
	for _, b := range all {
		log.Info("listening", "listener", b.Name, "addr", b.ln.Addr().String())
		go func() {
			if err := b.srv.Serve(b.ln); !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", b.Name, err)
				return
			}
			errc <- nil
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case runErr = <-errc:
		log.Error("listener failed, shutting down", "err", runErr)
	}

	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, b := range all {
		if err := b.srv.Shutdown(sctx); err != nil {
			log.Warn("forced shutdown", "listener", b.Name, "err", err)
			b.srv.Close()
		}
	}
	return runErr
}

func newHTTPServer(h http.Handler, log *slog.Logger, name string) *http.Server {
	return &http.Server{
		Handler: h,
		// No ReadTimeout/WriteTimeout: object bodies can take arbitrarily long
		// to stream. Slow-header clients are still cut off.
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(log.With("listener", name).Handler(), slog.LevelWarn),
	}
}
