package admin

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"bin/internal/logging"
)

func TestServerLogPage(t *testing.T) {
	e := newEnv(t)
	if code := e.call("GET", "/api/v1/logs", nil, nil); code != 401 {
		t.Errorf("without a session: %d", code)
	}
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "logs"}, nil)
	e.call("PUT", "/api/v1/buckets/logs/upload?key=a.txt", "a", nil)
	e.call("POST", "/api/v1/buckets/logs/share", map[string]any{"key": "a.txt", "expiresIn": 60}, nil)

	var page struct {
		Entries []logging.Entry
		Latest  int64
	}
	e.call("GET", "/api/v1/logs", nil, &page)
	found := false
	for _, en := range page.Entries {
		if en.Message == "share link created" && strings.Contains(en.Attrs, "key=a.txt") {
			found = true
		}
	}
	if !found || page.Latest == 0 {
		t.Fatalf("log page: %+v", page)
	}

	// Polling with ?after= returns only newer lines.
	before := page.Latest
	e.call("POST", "/api/v1/buckets/logs/share", map[string]any{"key": "a.txt", "expiresIn": 120}, nil)
	page.Entries = nil
	e.call("GET", "/api/v1/logs?after="+itoa(before), nil, &page)
	if len(page.Entries) == 0 || page.Entries[0].Seq <= before {
		t.Errorf("after %d: %+v", before, page.Entries)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestPollingIsQuiet(t *testing.T) {
	e := newEnv(t)
	e.login()
	for _, p := range []string{"/api/v1/logs", "/api/v1/dropzones", "/api/v1/replication/rules"} {
		if !quietRequest(httptestRequest("GET", p)) {
			t.Errorf("GET %s should be quiet", p)
		}
	}
	if quietRequest(httptestRequest("POST", "/api/v1/dropzones")) {
		t.Error("creating a dropzone must stay in the log")
	}
	if quietRequest(httptestRequest("GET", "/api/v1/buckets")) {
		t.Error("ordinary reads stay in the log")
	}
}

func httptestRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}
