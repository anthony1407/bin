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
	"os"
	"path/filepath"
	"strings"
)

// Config holds every server setting.
type Config struct {
	DataDir     string // data folder (blobs, metadata, temporary files)
	S3Addr      string // listen address of the S3 API
	ConsoleAddr string // listen address of the panel and the admin API
	Region      string // region advertised and required in SigV4 scopes
	Domain      string // domain for virtual-host style (bucket.domain); empty = path-style only

	RootAccessKey string
	RootSecretKey string
	MasterKey     string // key that encrypts secrets at rest; empty = generated inside the data dir

	LogLevel  slog.Level
	LogFormat string // "text" or "json"
}

// Recognized variables. Listed here so the config file can be validated.
const (
	envConfigFile    = "BIN_CONFIG_FILE"
	envDataDir       = "BIN_DATA_DIR"
	envS3Addr        = "BIN_S3_ADDR"
	envConsoleAddr   = "BIN_CONSOLE_ADDR"
	envRegion        = "BIN_REGION"
	envDomain        = "BIN_DOMAIN"
	envRootAccessKey = "BIN_ROOT_ACCESS_KEY"
	envRootSecretKey = "BIN_ROOT_SECRET_KEY"
	envMasterKey     = "BIN_MASTER_KEY"
	envLogLevel      = "BIN_LOG_LEVEL"
	envLogFormat     = "BIN_LOG_FORMAT"
)

var known = map[string]bool{
	envDataDir: true, envS3Addr: true, envConsoleAddr: true, envRegion: true, envDomain: true,
	envRootAccessKey: true, envRootSecretKey: true, envMasterKey: true, envLogLevel: true, envLogFormat: true,
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

	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		if v, ok := file[key]; ok && v != "" {
			return v
		}
		return def
	}

	c := &Config{
		DataDir:       get(envDataDir, "./data"),
		S3Addr:        get(envS3Addr, ":9000"),
		ConsoleAddr:   get(envConsoleAddr, ":9001"),
		Region:        get(envRegion, "us-east-1"),
		Domain:        strings.ToLower(get(envDomain, "")),
		RootAccessKey: get(envRootAccessKey, ""),
		RootSecretKey: get(envRootSecretKey, ""),
		MasterKey:     get(envMasterKey, ""),
		LogFormat:     get(envLogFormat, "text"),
	}
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
		errs = append(errs, fmt.Errorf("%s is required and must be at least 3 characters", envRootAccessKey))
	}
	if len(c.RootSecretKey) < 8 {
		errs = append(errs, fmt.Errorf("%s is required and must be at least 8 characters", envRootSecretKey))
	}
	if c.Region == "" {
		errs = append(errs, fmt.Errorf("%s must not be empty", envRegion))
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("%s must be \"text\" or \"json\"", envLogFormat))
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
