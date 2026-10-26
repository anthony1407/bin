// Package config loads bin's settings from environment variables and,
// optionally, from a small file of KEY=VALUE lines.
//
// Precedence: environment variable > file > default value.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"bin/internal/server"
)

// Config holds every server setting.
type Config struct {
	DataDir     string // data folder (blobs, metadata, temporary files)
	S3Addr      string // listen address of the S3 API
	ConsoleAddr string // listen address of the panel and the admin API
	Region      string // region advertised and required in SigV4 scopes
	Domain      string // domain for virtual-host style (bucket.domain); empty = path-style only
	PublicURL   string // S3 endpoint as clients reach it; empty = derived from each request
	ConsoleURL  string // console URL as users reach it, shown at startup; empty = the listen addresses

	RootAccessKey string
	RootSecretKey string
	MasterKey     string // key that encrypts secrets at rest; empty = generated inside the data dir

	LogLevel    slog.Level
	LogFormat   string // "text" or "json"
	LogRequests bool   // one log line per request

	// TrustedProxies are reverse proxies whose X-Forwarded-For names the real
	// client; empty = the connection's own address is the client.
	TrustedProxies []netip.Prefix
}

// Recognized variables. Listed here so the config file can be validated.
const (
	envConfigFile     = "BIN_CONFIG_FILE"
	envDataDir        = "BIN_DATA_DIR"
	envS3Addr         = "BIN_S3_ADDR"
	envConsoleAddr    = "BIN_CONSOLE_ADDR"
	envRegion         = "BIN_REGION"
	envDomain         = "BIN_DOMAIN"
	envRootAccessKey  = "BIN_ROOT_ACCESS_KEY"
	envRootSecretKey  = "BIN_ROOT_SECRET_KEY"
	envMasterKey      = "BIN_MASTER_KEY"
	envLogLevel       = "BIN_LOG_LEVEL"
	envLogFormat      = "BIN_LOG_FORMAT"
	envLogRequests    = "BIN_LOG_REQUESTS"
	envPublicURL      = "BIN_PUBLIC_URL"
	envConsoleURL     = "BIN_CONSOLE_URL"
	envTrustedProxies = "BIN_TRUSTED_PROXIES"
)

// Setting documents one configuration variable, for --help and validation.
type Setting struct {
	Name        string
	Aliases     []string // other accepted names, equivalent to Name
	Default     string
	Description string
}

// Settings lists every variable bin reads, in the order --help shows them.
// The root credentials accept the naming styles people bring from other S3
// servers: ROOT_USER/ROOT_PASSWORD and ACCESS_KEY/SECRET_KEY.
var Settings = []Setting{
	{envRootAccessKey, []string{"BIN_ROOT_USER", "BIN_ACCESS_KEY"}, "(required)", "access key of the root account (3+ characters)"},
	{envRootSecretKey, []string{"BIN_ROOT_PASSWORD", "BIN_SECRET_KEY"}, "(required)", "secret key of the root account (8+ characters, 16+ recommended)"},
	{envDataDir, nil, "./data", "directory holding objects and metadata"},
	{envS3Addr, nil, ":9000", "listen address of the S3 API"},
	{envConsoleAddr, nil, ":9001", "listen address of the web console and admin API"},
	{envRegion, nil, "us-east-1", "region reported to clients and required in signatures"},
	{envDomain, nil, "", "domain for virtual-host style requests (bucket.domain); empty = path style only"},
	{envPublicURL, nil, "", "S3 API URL as clients see it (e.g. https://s3.example.com), used in share links"},
	{envConsoleURL, nil, "", "console URL as users see it (e.g. http://localhost:9101), shown at startup"},
	{envTrustedProxies, nil, "", "reverse proxies (IPs or CIDRs, comma separated) whose X-Forwarded-For gives the client IP"},
	{envMasterKey, nil, "(generated)", "key that encrypts secrets at rest; generated in the data directory if unset"},
	{envLogLevel, nil, "info", "debug, info, warn or error"},
	{envLogFormat, nil, "text", "text (readable) or json (for log collectors)"},
	{envLogRequests, nil, "true", "log one line per request"},
	{envConfigFile, nil, "bin.env", "optional KEY=VALUE file read before applying the environment"},
}

// aliases maps every accepted name to its canonical one.
var aliases = func() map[string]string {
	m := map[string]string{}
	for _, s := range Settings {
		if s.Name == envConfigFile {
			continue
		}
		m[s.Name] = s.Name
		for _, a := range s.Aliases {
			m[a] = s.Name
		}
	}
	return m
}()

var known = func() map[string]bool {
	m := map[string]bool{}
	for name := range aliases {
		m[name] = true
	}
	return m
}()

// describe names a setting with its aliases, e.g.
// "BIN_ROOT_ACCESS_KEY (or BIN_ROOT_USER, BIN_ACCESS_KEY)".
func describe(canonical string) string {
	names := namesOf(canonical)
	if len(names) == 1 {
		return canonical
	}
	return names[0] + " (or " + strings.Join(names[1:], ", ") + ")"
}

// namesOf returns a setting's canonical name followed by its aliases.
func namesOf(canonical string) []string {
	for _, s := range Settings {
		if s.Name == canonical {
			return append([]string{s.Name}, s.Aliases...)
		}
	}
	return []string{canonical}
}

// UnknownEnv returns BIN_* variables in environ (as from os.Environ) that bin
// does not read, each with the closest known name: usually typos.
func UnknownEnv(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "BIN_") || known[name] || name == envConfigFile {
			continue
		}
		best, bestDist := "", 4 // suggest only close matches
		for k := range known {
			d := editDistance(name, k)
			if strings.HasPrefix(k, name) || strings.HasPrefix(name, k) {
				d = min(d, 1) // a missing or extra suffix, like BIN_ROOT_SECRET
			}
			if d < bestDist {
				best, bestDist = k, d
			}
		}
		out[name] = best
	}
	return out
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Load reads the configuration. getenv is usually os.Getenv; it is injected for tests.
func Load(getenv func(string) string) (*Config, error) {
	file := map[string]string{}
	path := getenv(envConfigFile)
	explicit := path != ""
	if !explicit {
		path = "bin.env"
	}
	if m, err := readFile(path); err == nil {
		file = m
	} else if explicit || !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// get resolves a setting by any of its names: the environment first, then
	// the file. Two names of the same setting with different values in the
	// same source is an error, never a silent choice.
	var conflicts []error
	get := func(key, def string) string {
		fromFile := func(k string) string { return file[k] }
		for _, source := range []func(string) string{getenv, fromFile} {
			value, from := "", ""
			for _, name := range namesOf(key) {
				v := source(name)
				switch {
				case v == "":
				case value == "":
					value, from = v, name
				case v != value:
					conflicts = append(conflicts, fmt.Errorf("%s and %s are set to different values; set only one of them", from, name))
				}
			}
			if value != "" {
				return value
			}
		}
		return def
	}

	c := &Config{
		DataDir:       get(envDataDir, "./data"),
		S3Addr:        get(envS3Addr, ":9000"),
		ConsoleAddr:   get(envConsoleAddr, ":9001"),
		Region:        get(envRegion, "us-east-1"),
		Domain:        strings.ToLower(get(envDomain, "")),
		PublicURL:     strings.TrimRight(get(envPublicURL, ""), "/"),
		ConsoleURL:    strings.TrimRight(get(envConsoleURL, ""), "/"),
		RootAccessKey: get(envRootAccessKey, ""),
		RootSecretKey: get(envRootSecretKey, ""),
		MasterKey:     get(envMasterKey, ""),
		LogFormat:     get(envLogFormat, "text"),
	}
	if len(conflicts) > 0 {
		return nil, errors.Join(conflicts...)
	}
	switch v := strings.ToLower(get(envLogRequests, "true")); v {
	case "true", "1", "yes", "on":
		c.LogRequests = true
	case "false", "0", "no", "off":
	default:
		return nil, fmt.Errorf("%s must be true or false, got %q", envLogRequests, v)
	}
	proxies, err := server.ParseTrustedProxies(get(envTrustedProxies, ""))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envTrustedProxies, err)
	}
	c.TrustedProxies = proxies
	if err := c.LogLevel.UnmarshalText([]byte(get(envLogLevel, "info"))); err != nil {
		return nil, fmt.Errorf("%s: %w", envLogLevel, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envDataDir, err)
	}
	c.DataDir = abs
	return c, nil
}

func (c *Config) validate() error {
	var errs []error
	if len(c.RootAccessKey) < 3 {
		errs = append(errs, fmt.Errorf("%s is required and must be at least 3 characters", describe(envRootAccessKey)))
	}
	if len(c.RootSecretKey) < 8 {
		errs = append(errs, fmt.Errorf("%s is required and must be at least 8 characters", describe(envRootSecretKey)))
	}
	if c.Region == "" {
		errs = append(errs, fmt.Errorf("%s must not be empty", envRegion))
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("%s must be \"text\" or \"json\"", envLogFormat))
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "http://") && !strings.HasPrefix(c.PublicURL, "https://") {
		errs = append(errs, fmt.Errorf("%s must start with http:// or https://", envPublicURL))
	}
	if c.ConsoleURL != "" && !strings.HasPrefix(c.ConsoleURL, "http://") && !strings.HasPrefix(c.ConsoleURL, "https://") {
		errs = append(errs, fmt.Errorf("%s must start with http:// or https://", envConsoleURL))
	}
	if c.S3Addr == c.ConsoleAddr {
		errs = append(errs, fmt.Errorf("%s and %s must not be the same address", envS3Addr, envConsoleAddr))
	}
	return errors.Join(errs...)
}

// readFile parses a KEY=VALUE file. It accepts # comments, an optional
// "export " prefix and values wrapped in double or single quotes.
func readFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		if !known[key] {
			return nil, fmt.Errorf("line %d: unknown variable %q", n, key)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		m[key] = val
	}
	return m, sc.Err()
}
