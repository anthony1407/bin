// Command bin is a single-node, S3-compatible object storage server with an
// embedded web console.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"bin/internal/admin"
	"bin/internal/audit"
	"bin/internal/auth"
	"bin/internal/buildinfo"
	"bin/internal/config"
	"bin/internal/logging"
	"bin/internal/s3"
	"bin/internal/s3/sigv4"
	"bin/internal/secrets"
	"bin/internal/server"
	"bin/internal/storage"
	"bin/internal/webui"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bin:", err)
		os.Exit(1)
	}
}

// dispatch runs the command named by args[0]. Without a command, or when the
// first argument is a server flag or a data directory, it runs the server.
func dispatch(args []string) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			printHelp(os.Stdout)
			return nil
		}
	}
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "server":
		return runServer(args[1:])
	case "version", "--version", "-v":
		fmt.Println("bin version", buildinfo.String())
		return nil
	case "help":
		printHelp(os.Stdout)
		return nil
	case "healthcheck":
		return healthcheck()
	case "completion":
		return runCompletion(args[1:])
	}
	if cmd == "" || strings.HasPrefix(cmd, "-") || strings.ContainsAny(cmd, `/\`) {
		return runServer(args)
	}
	return fmt.Errorf("unknown command %q; run \"bin --help\" for usage", cmd)
}

// serverOverrides parses `bin server [DATA_DIR] [--address ADDR]
// [--console-address ADDR]` into settings that take precedence over the
// environment and bin.env.
func serverOverrides(args []string) (map[string]string, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	address := fs.String("address", "", "listen address of the S3 API")
	console := fs.String("console-address", "", "listen address of the web console")
	// Accept the data directory before or after the flags.
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, fmt.Errorf("%w; run \"bin --help\" for usage", err)
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("expected at most one data directory, got %q", positional)
	}
	out := map[string]string{}
	if len(positional) == 1 {
		out["BIN_DATA_DIR"] = positional[0]
	}
	if *address != "" {
		out["BIN_S3_ADDR"] = *address
	}
	if *console != "" {
		out["BIN_CONSOLE_ADDR"] = *console
	}
	return out, nil
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `bin: single-node, S3-compatible object storage with a built-in web console.

Usage:
  bin [command]
  bin server [DATA_DIR] [--address ADDR] [--console-address ADDR]

Commands:
  server        run the server (the default when no command is given)
  version       print version information (also --version, -v)
  healthcheck   exit 0 if the local server answers its health endpoint
  completion    generate shell completion scripts (bash, zsh, fish)
  help          show this help (also --help, -h)

Server flags (they override the matching settings below):
  DATA_DIR                 same as BIN_DATA_DIR
  --address ADDR           same as BIN_S3_ADDR, e.g. :9000 or 127.0.0.1:9000
  --console-address ADDR   same as BIN_CONSOLE_ADDR, e.g. :9001

Configuration (environment variables, or KEY=VALUE lines in bin.env):
`)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range config.Settings {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", s.Name, s.Default, s.Description)
		if len(s.Aliases) > 0 {
			fmt.Fprintf(tw, "  \t\talso accepted: %s\n", strings.Join(s.Aliases, ", "))
		}
	}
	tw.Flush()
	fmt.Fprint(w, `
Examples:
  BIN_ROOT_ACCESS_KEY=admin BIN_ROOT_SECRET_KEY=change-me-please bin server /data
  bin server /data --address :9100 --console-address :9101
  docker run -p 9000:9000 -p 9001:9001 -v bin-data:/data \
    -e BIN_ROOT_ACCESS_KEY=admin -e BIN_ROOT_SECRET_KEY=change-me-please bin
  aws --endpoint-url http://localhost:9000 s3 ls
`)
}

func runServer(args []string) error {
	overrides, err := serverOverrides(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(func(k string) string {
		if v, ok := overrides[k]; ok {
			return v
		}
		return os.Getenv(k)
	})
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

	keys, err := openKeyring(log, cfg, store)
	if err != nil {
		return err
	}

	// Closed before the store (defers run in reverse), so queued entries are written.
	auditLog := audit.New(store, log.With("component", "audit"))
	defer auditLog.Close()

	panel := webui.New()
	if !panel.Built() {
		log.Warn("this binary was built without the web panel; the console serves only the admin API (run make web, then rebuild)")
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go stopOnSignal(log, cancel)

	ready := func(bound []server.Bound) {
		announce(log, cfg, bound)
		for _, b := range bound {
			if b.Name == "console" {
				recordConsoleAddr(log, b.Addr)
			}
		}
	}
	defer os.Remove(healthAddrFile())
	err = server.Run(ctx, log, ready,
		server.Listener{Name: "s3", Addr: cfg.S3Addr, Handler: s3.NewHandler(s3.Options{
			Store:       store,
			Region:      cfg.Region,
			Domain:      cfg.Domain,
			HostID:      hostID(cfg),
			Owner:       owner(cfg),
			Auth:        &sigv4.Verifier{Region: cfg.Region, Store: keys},
			Keys:        keys,
			Audit:       auditLog,
			Log:         log.With("api", "s3"),
			LogRequests: cfg.LogRequests,
		})},
		server.Listener{Name: "console", Addr: cfg.ConsoleAddr, Handler: admin.NewHandler(admin.Options{
			Store:       store,
			Keys:        keys,
			Audit:       auditLog,
			Region:      cfg.Region,
			DataDir:     cfg.DataDir,
			S3Addr:      cfg.S3Addr,
			PublicURL:   cfg.PublicURL,
			Log:         log.With("api", "console"),
			LogRequests: cfg.LogRequests,
			LogLevel:    strings.ToLower(cfg.LogLevel.String()),
			LogFormat:   cfg.LogFormat,
			Panel:       panel,
		})},
	)
	if err == nil {
		log.Info("bin stopped")
	}
	return err
}

// openKeyring loads the master key (BIN_MASTER_KEY or .bin/master.key),
// checks it against the stored secrets and returns the credential keyring.
func openKeyring(log *slog.Logger, cfg *config.Config, store *storage.Store) (*auth.Keyring, error) {
	keyFile := filepath.Join(cfg.DataDir, ".bin", "master.key")
	key, created, err := secrets.LoadKey(cfg.MasterKey, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the master key: %w", err)
	}
	if created {
		log.Warn("generated a new master key; keep a backup of it together with the data, "+
			"or set BIN_MASTER_KEY to keep it outside", "file", keyFile)
	}
	box, err := secrets.New(key)
	if err != nil {
		return nil, err
	}
	if err := auth.CheckMasterKey(context.Background(), store, box); err != nil {
		return nil, err
	}
	return &auth.Keyring{
		RootAccessKey: cfg.RootAccessKey,
		RootSecretKey: cfg.RootSecretKey,
		Store:         store,
		Box:           box,
		Log:           log,
	}, nil
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

// healthAddrFile is where a running server records its console address, so
// `bin healthcheck` finds it whatever flags or settings chose the port.
func healthAddrFile() string { return filepath.Join(os.TempDir(), "bin-console.addr") }

func recordConsoleAddr(log *slog.Logger, addr net.Addr) {
	if err := os.WriteFile(healthAddrFile(), []byte(addr.String()), 0o644); err != nil {
		log.Debug("cannot record the console address for healthcheck", "err", err)
	}
}

// healthcheck probes the console's health endpoint on the loopback interface.
// It exists so container images need no shell or HTTP client. The port comes
// from the running server's record, else BIN_CONSOLE_ADDR, else 9001.
func healthcheck() error {
	addr := os.Getenv("BIN_CONSOLE_ADDR")
	if recorded, err := os.ReadFile(healthAddrFile()); err == nil {
		addr = strings.TrimSpace(string(recorded))
	}
	if addr == "" {
		addr = ":9001"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("console address %q: %w", addr, err)
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

// runCompletion generates shell completion scripts for bash, zsh, or fish.
func runCompletion(args []string) error {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	var out string
	switch shell {
	case "bash":
		out = bashCompletion
	case "zsh":
		out = zshCompletion
	case "fish":
		out = fishCompletion
	default:
		return fmt.Errorf("unsupported shell %q; supported: bash, zsh, fish", shell)
	}
	fmt.Print(out)
	return nil
}

const bashCompletion = `# bin bash completion
_bin() {
	local cur prev opts
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
	opts="server version healthcheck completion help --version -v --help -h"

	case "${prev}" in
		server)
			COMPREPLY=( $(compgen -W "--address --console-address" -- ${cur}) )
			return 0
			;;
		completion)
			COMPREPLY=( $(compgen -W "bash zsh fish" -- ${cur}) )
			return 0
			;;
		*)
			;;
	esac

	if [[ ${cur} == -* ]]; then
		COMPREPLY=( $(compgen -W "--address --console-address --help -h --version -v" -- ${cur}) )
	else
		COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
	fi
}
complete -F _bin bin
`

const zshCompletion = `# bin zsh completion
#compdef bin

_bin() {
	local -a commands
	local -a options
	commands=(
		'server:run the server'
		'version:print version information'
		'healthcheck:exit 0 if the local server answers its health endpoint'
		'completion:generate shell completion scripts'
		'help:show this help'
	)
	options=(
		'--address[listen address of the S3 API]'
		'--console-address[listen address of the web console]'
		'--help[show help]'
		'--version[print version]'
	)

	_arguments -C \
		'(-h --help)'{-h,--help}'[show help]' \
		'(-v --version)'{-v,--version}'[print version]' \
		'*: :($commands)' \
		'*::options:->subcmds' && return 0

	if (( CURRENT == 1 )); then
		compadd -a commands
		return
	fi

	case "${words[1]}" in
		server)
			_arguments \
				'--address[listen address of the S3 API]' \
				'--console-address[listen address of the web console]' \
				'*:data directory:_files -/'
			;;
		completion)
			compadd bash zsh fish
			;;
	esac
}
_bin
`

const fishCompletion = `# bin fish completion
function __fish_bin_using_command
	set -l cmd (commandline -opc)
	if [ (count $cmd) -eq 1 -a $cmd[1] = 'bin' ]
		return 0
	end
	return 1
end

complete -c bin -f -n '__fish_bin_using_command' -a 'server version healthcheck completion help'

complete -c bin -n '__fish_bin_using_command' -l help -s h -d 'Show help'
complete -c bin -n '__fish_bin_using_command' -l version -s v -d 'Print version'

complete -c bin -n '__fish_seen_subcommand_from server' -l address -d 'Listen address of the S3 API'
complete -c bin -n '__fish_seen_subcommand_from server' -l console-address -d 'Listen address of the web console'
complete -c bin -n '__fish_seen_subcommand_from server' -f -d 'Data directory'

complete -c bin -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`

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
