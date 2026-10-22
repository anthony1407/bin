// Command bin is a single-node, S3-compatible object storage server with an
// embedded web console.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"bin/internal/admin"
	"bin/internal/buildinfo"
	"bin/internal/config"
	"bin/internal/logging"
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
	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" {
			cmd = "help"
		}
	}
	var err error
	switch cmd {
	case "", "server":
		err = run()
	case "version", "--version", "-v":
		fmt.Println("bin version", buildinfo.String())
	case "help", "--help", "-h":
		printHelp(os.Stdout)
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("unknown command %q; run \"bin --help\" for usage", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bin:", err)
		os.Exit(1)
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `bin: single-node, S3-compatible object storage with a built-in web console.

Usage:
  bin [command]

Commands:
  server        run the server (the default when no command is given)
  version       print version information (also --version, -v)
  healthcheck   exit 0 if the local server answers its health endpoint
  help          show this help (also --help, -h)

Configuration (environment variables, or KEY=VALUE lines in bin.env):
`)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range config.Settings {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", s.Name, s.Default, s.Description)
	}
	tw.Flush()
	fmt.Fprint(w, `
Examples:
  BIN_ROOT_ACCESS_KEY=admin BIN_ROOT_SECRET_KEY=change-me-please bin
  docker run -p 9000:9000 -p 9001:9001 -v bin-data:/data \
    -e BIN_ROOT_ACCESS_KEY=admin -e BIN_ROOT_SECRET_KEY=change-me-please bin
  aws --endpoint-url http://localhost:9000 s3 ls
`)
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := logging.New(os.Stderr, cfg.LogFormat, cfg.LogLevel)
	startupWarnings(log, cfg)

	store, err := storage.Open(cfg.DataDir, log)
	if err != nil {
		return fmt.Errorf("opening data directory %s: %w", cfg.DataDir, err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go stopOnSignal(log, cancel)

	err = server.Run(ctx, log, func(b []server.Bound) { announce(log, cfg, b) },
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
			Log:         log.With("api", "s3"),
			LogRequests: cfg.LogRequests,
		})},
		server.Listener{Name: "console", Addr: cfg.ConsoleAddr, Handler: admin.NewHandler(admin.Options{
			Log:         log.With("api", "console"),
			LogRequests: cfg.LogRequests,
		})},
	)
	if err == nil {
		log.Info("bin stopped")
	}
	return err
}

// stopOnSignal cancels the server on SIGINT/SIGTERM, recording which signal
// arrived; a second signal exits immediately.
func stopOnSignal(log *slog.Logger, cancel context.CancelCauseFunc) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	sig := <-ch
	name := "SIGTERM"
	if sig == os.Interrupt {
		name = "SIGINT"
	}
	cancel(errors.New("received " + name))
	<-ch
	log.Warn("second signal received, exiting without waiting")
	os.Exit(1)
}

// startupWarnings reports configuration that works but is probably a mistake.
func startupWarnings(log *slog.Logger, cfg *config.Config) {
	unknown := config.UnknownEnv(os.Environ())
	names := make([]string, 0, len(unknown))
	for name := range unknown {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if s := unknown[name]; s != "" {
			log.Warn(fmt.Sprintf("%s is not a bin setting and is ignored; did you mean %s?", name, s))
		} else {
			log.Warn(fmt.Sprintf("%s is not a bin setting and is ignored; see bin --help", name))
		}
	}
	if n := len(cfg.RootSecretKey); n < 16 {
		log.Warn(fmt.Sprintf("BIN_ROOT_SECRET_KEY has only %d characters; use 16 or more", n))
	}
}

// announce prints the startup banner once every listener accepts connections.
func announce(log *slog.Logger, cfg *config.Config, bound []server.Bound) {
	urls := map[string][]string{}
	for _, b := range bound {
		urls[b.Name] = listenURLs(b.Addr)
	}
	if cfg.LogFormat == "json" {
		log.Info("bin started", "version", buildinfo.Version, "commit", buildinfo.Commit,
			"s3_api", urls["s3"], "console", urls["console"], "region", cfg.Region, "data_dir", cfg.DataDir)
		return
	}
	var b strings.Builder
	b.WriteString(`
 _     _
| |__ (_)_ __
| '_ \| | '_ \
| |_) | | | | |
|_.__/|_|_| |_|  S3-compatible object storage

`)
	fmt.Fprintf(&b, "Version:   %s\n", buildinfo.String())
	fmt.Fprintf(&b, "S3 API:    %s\n", strings.Join(urls["s3"], "  "))
	fmt.Fprintf(&b, "Console:   %s\n", strings.Join(urls["console"], "  "))
	fmt.Fprintf(&b, "Region:    %s\n", cfg.Region)
	fmt.Fprintf(&b, "Data:      %s\n", cfg.DataDir)
	fmt.Fprintf(&b, "Help:      bin --help\n\n")
	os.Stderr.WriteString(b.String())
}

// listenURLs lists how a listener can be reached: every non-loopback IPv4
// address and 127.0.0.1 when it listens on all interfaces.
func listenURLs(addr net.Addr) []string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return []string{addr.String()}
	}
	port := strconv.Itoa(tcp.Port)
	if !tcp.IP.IsUnspecified() {
		return []string{"http://" + net.JoinHostPort(tcp.IP.String(), port)}
	}
	var out []string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, "http://"+net.JoinHostPort(ipn.IP.String(), port))
			}
		}
	}
	return append(out, "http://"+net.JoinHostPort("127.0.0.1", port))
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
