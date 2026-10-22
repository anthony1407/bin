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

// Bound is a listener that is accepting connections.
type Bound struct {
	Name string
	Addr net.Addr
}

// Run binds every listener, calls ready (if not nil) once all of them accept
// connections, serves until ctx is cancelled or one of them fails, then shuts
// all of them down gracefully. Binding happens before serving starts, so a
// busy port is reported immediately instead of half-starting the process.
// The cancellation cause of ctx (context.Cause) is logged as the reason.
func Run(ctx context.Context, log *slog.Logger, ready func([]Bound), listeners ...Listener) error {
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
	var addrs []Bound
	for _, b := range all {
		log.Debug("listening", "listener", b.Name, "addr", b.ln.Addr().String())
		addrs = append(addrs, Bound{Name: b.Name, Addr: b.ln.Addr()})
		go func() {
			if err := b.srv.Serve(b.ln); !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", b.Name, err)
				return
			}
			errc <- nil
		}()
	}
	if ready != nil {
		ready(addrs)
	}

	var runErr error
	select {
	case <-ctx.Done():
		log.Info(fmt.Sprintf("shutting down: %v; waiting up to %s for requests in progress", context.Cause(ctx), shutdownTimeout))
	case runErr = <-errc:
		log.Error("listener failed, shutting down", "err", runErr)
	}

	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, b := range all {
		if err := b.srv.Shutdown(sctx); err != nil {
			log.Warn("requests still running after the grace period were cut", "listener", b.Name, "err", err)
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
