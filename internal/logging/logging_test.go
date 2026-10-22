package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "text", slog.LevelInfo).With("listener", "s3")
	log.Info("GET /photos/a b.jpg -> 200", "op", "GetObject", "took", 3*time.Millisecond, "note", "has space", "empty", "")
	log.Debug("hidden")
	line := buf.String()
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d INFO  GET /photos/a b.jpg -> 200 listener=s3 op=GetObject took=3ms note="has space" empty=""\n$`).MatchString(line) {
		t.Errorf("unexpected line: %q", line)
	}
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "json", slog.LevelInfo).Warn("careful", "count", 2)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["msg"] != "careful" || rec["level"] != "WARN" {
		t.Errorf("json record %s: %v", buf.String(), err)
	}
}

func TestGroups(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "text", slog.LevelInfo).WithGroup("req").Info("x", slog.Group("tls", "v", "1.3"), "id", "7")
	if !strings.Contains(buf.String(), " req.tls.v=1.3 req.id=7") {
		t.Errorf("groups: %q", buf.String())
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 999: "999 B", 1536: "1.5 KB", 41943040: "40.0 MB", 5 << 30: "5.0 GB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}
