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

// The root credentials accept three naming styles, from any source.
func TestCredentialAliases(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, pair := range [][2]string{
		{"BIN_ROOT_ACCESS_KEY", "BIN_ROOT_SECRET_KEY"},
		{"BIN_ROOT_USER", "BIN_ROOT_PASSWORD"},
		{"BIN_ACCESS_KEY", "BIN_SECRET_KEY"},
	} {
		c, err := Load(envOf(map[string]string{pair[0]: "admin", pair[1]: "Bin123!-long-secret"}))
		if err != nil || c.RootAccessKey != "admin" || c.RootSecretKey != "Bin123!-long-secret" {
			t.Errorf("%v: %+v, %v", pair, c, err)
		}
	}

	// The same value under two names is fine; different values are rejected.
	_, err := Load(envOf(map[string]string{
		"BIN_ROOT_USER": "admin", "BIN_ACCESS_KEY": "admin", "BIN_ROOT_PASSWORD": "long-secret",
	}))
	if err != nil {
		t.Errorf("same value twice: %v", err)
	}
	_, err = Load(envOf(map[string]string{
		"BIN_ROOT_USER": "alice", "BIN_ACCESS_KEY": "bob", "BIN_ROOT_PASSWORD": "long-secret",
	}))
	if err == nil || !strings.Contains(err.Error(), "BIN_ROOT_USER and BIN_ACCESS_KEY are set to different values") {
		t.Errorf("conflict: %v", err)
	}

	// Aliases work in bin.env too, and the environment still wins.
	path := filepath.Join(t.TempDir(), "bin.env")
	os.WriteFile(path, []byte("BIN_ROOT_USER=fromfile\nBIN_ROOT_PASSWORD=file-secret-long\n"), 0o600)
	c, err := Load(envOf(map[string]string{"BIN_CONFIG_FILE": path, "BIN_ACCESS_KEY": "fromenv"}))
	if err != nil || c.RootAccessKey != "fromenv" || c.RootSecretKey != "file-secret-long" {
		t.Errorf("file + env: %+v, %v", c, err)
	}

	// Aliases are not reported as unknown settings.
	if u := UnknownEnv([]string{"BIN_ROOT_USER=a", "BIN_SECRET_KEY=b"}); len(u) != 0 {
		t.Errorf("aliases flagged as unknown: %v", u)
	}
	_, err = Load(envOf(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "BIN_ROOT_ACCESS_KEY (or BIN_ROOT_USER, BIN_ACCESS_KEY) is required") {
		t.Errorf("missing credentials message: %v", err)
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
