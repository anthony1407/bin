// Command bin is a single-node, S3-compatible object storage server with an
// embedded web console.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bin/internal/admin"
	"bin/internal/buildinfo"
	"bin/internal/config"
	"bin/internal/s3"
	"bin/internal/s3/sigv4"
	"bin/internal/server"
	"bin/internal/storage"
)

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "":
		err = run()
	case "version", "--version":
		fmt.Println("bin", buildinfo.Version)
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("unknown command %q (expected: version, healthcheck, or no arguments to serve)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bin:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting bin", "version", buildinfo.Version, "data_dir", cfg.DataDir, "region", cfg.Region)

	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("opening data directory %s: %w", cfg.DataDir, err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return server.Run(ctx, log,
		server.Listener{Name: "s3", Addr: cfg.S3Addr, Handler: s3.NewHandler(s3.Options{
			Store:  store,
			Region: cfg.Region,
			Domain: cfg.Domain,
			HostID: hostID(cfg),
			Owner:  owner(cfg),
			Auth: &sigv4.Verifier{
				Region: cfg.Region,
				Store:  sigv4.StaticStore{cfg.RootAccessKey: cfg.RootSecretKey},
			},
			Log: log.With("listener", "s3"),
		})},
		server.Listener{Name: "console", Addr: cfg.ConsoleAddr, Handler: admin.NewHandler(admin.Options{
			Log: log.With("listener", "console"),
		})},
	)
}

// healthcheck probes the console's health endpoint on the loopback interface.
// It exists so container images need no shell or HTTP client.
func healthcheck() error {
	addr := os.Getenv("BIN_CONSOLE_ADDR")
	if addr == "" {
		addr = ":9001"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("BIN_CONSOLE_ADDR: %w", err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/v1/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// owner is the single account that owns every bucket. Its ID is a stable hash
// of the root access key, shaped like S3's canonical user IDs.
func owner(cfg *config.Config) s3.Owner {
	sum := sha256.Sum256([]byte("bin-owner\x00" + cfg.RootAccessKey))
	return s3.Owner{ID: hex.EncodeToString(sum[:]), DisplayName: "bin"}
}

// hostID derives a stable, opaque instance identifier from the host name and
// data directory, like the x-amz-id-2 value S3 returns.
func hostID(cfg *config.Config) string {
	name, _ := os.Hostname()
	sum := sha256.Sum256([]byte(name + "\x00" + cfg.DataDir))
	return hex.EncodeToString(sum[:])
}
