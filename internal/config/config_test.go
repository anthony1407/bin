package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // no bin.env in the current directory
	c, err := Load(envOf(map[string]string{
		"BIN_ROOT_ACCESS_KEY": "admin",
		"BIN_ROOT_SECRET_KEY": "long-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.S3Addr != ":9000" || c.ConsoleAddr != ":9001" || c.Region != "us-east-1" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.LogLevel != slog.LevelInfo || c.LogFormat != "text" {
		t.Errorf("unexpected log defaults: %v %s", c.LogLevel, c.LogFormat)
	}
	if !filepath.IsAbs(c.DataDir) {
		t.Errorf("DataDir should be absolute: %s", c.DataDir)
	}
}

func TestFileAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my.env")
	content := `# comment
BIN_ROOT_ACCESS_KEY=from-file
export BIN_ROOT_SECRET_KEY="secret from file"
BIN_REGION='eu-west-1'
BIN_S3_ADDR=:7000
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(envOf(map[string]string{
		"BIN_CONFIG_FILE": path,
		"BIN_S3_ADDR":     ":8000", // the environment wins over the file
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.RootAccessKey != "from-file" || c.RootSecretKey != "secret from file" {
		t.Errorf("credentials from file not applied: %q %q", c.RootAccessKey, c.RootSecretKey)
	}
	if c.Region != "eu-west-1" {
		t.Errorf("region = %q", c.Region)
	}
	if c.S3Addr != ":8000" {
		t.Errorf("environment should take precedence, S3Addr = %q", c.S3Addr)
	}
}

func TestErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing credentials": {map[string]string{}, "BIN_ROOT_ACCESS_KEY"},
		"short secret": {map[string]string{
			"BIN_ROOT_ACCESS_KEY": "admin", "BIN_ROOT_SECRET_KEY": "short",
		}, "BIN_ROOT_SECRET_KEY"},
		"same address": {map[string]string{
			"BIN_ROOT_ACCESS_KEY": "admin", "BIN_ROOT_SECRET_KEY": "long-secret",
			"BIN_S3_ADDR": ":9000", "BIN_CONSOLE_ADDR": ":9000",
		}, "same address"},
		"bad log level": {map[string]string{
			"BIN_ROOT_ACCESS_KEY": "admin", "BIN_ROOT_SECRET_KEY": "long-secret",
			"BIN_LOG_LEVEL": "noisy",
		}, "BIN_LOG_LEVEL"},
		"missing file": {map[string]string{"BIN_CONFIG_FILE": "missing.env"}, "missing.env"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(envOf(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, expected it to mention %q", err, tc.want)
			}
		})
	}
}

func TestUnknownEnv(t *testing.T) {
	got := UnknownEnv([]string{
		"PATH=/usr/bin",
		"BIN_ROOT_ACCESS_KEY=admin",
		"BIN_ROOT_SECRET=x",    // typo: suggests BIN_ROOT_SECRET_KEY
		"BIN_LOGLEVEL=debug",   // typo: suggests BIN_LOG_LEVEL
		"BIN_SOMETHING_ELSE=1", // no close match
		"BIN_CONFIG_FILE=bin.env",
	})
	want := map[string]string{
		"BIN_ROOT_SECRET":    "BIN_ROOT_SECRET_KEY",
		"BIN_LOGLEVEL":       "BIN_LOG_LEVEL",
		"BIN_SOMETHING_ELSE": "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: suggestion %q, want %q", k, got[k], v)
		}
	}
}

func TestLogRequests(t *testing.T) {
	t.Chdir(t.TempDir())
	base := map[string]string{"BIN_ROOT_ACCESS_KEY": "admin", "BIN_ROOT_SECRET_KEY": "long-secret"}
	c, err := Load(envOf(base))
	if err != nil || !c.LogRequests {
		t.Fatalf("default: %v %v", c, err)
	}
	base["BIN_LOG_REQUESTS"] = "off"
	if c, _ := Load(envOf(base)); c.LogRequests {
		t.Error("off not applied")
	}
	base["BIN_LOG_REQUESTS"] = "maybe"
	if _, err := Load(envOf(base)); err == nil {
		t.Error("invalid value accepted")
	}
}

func TestFileUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin.env")
	if err := os.WriteFile(path, []byte("BIN_TYPO=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(envOf(map[string]string{"BIN_CONFIG_FILE": path}))
	if err == nil || !strings.Contains(err.Error(), "BIN_TYPO") {
		t.Fatalf("error = %v", err)
	}
}
