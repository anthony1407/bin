package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("172.16.0.0/12, 10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	h := RealIP(trusted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }))
	for _, tc := range []struct {
		name, remote, xff, realIP, want string
	}{
		{"proxy forwards the client", "172.21.0.1:5000", "203.0.113.7", "", "203.0.113.7:5000"},
		{"client cannot forge left entries", "172.21.0.1:5000", "1.1.1.1, 203.0.113.7", "", "203.0.113.7:5000"},
		{"chained trusted proxies are skipped", "172.21.0.1:5000", "203.0.113.7, 10.0.0.5", "", "203.0.113.7:5000"},
		{"X-Real-IP without X-Forwarded-For", "10.0.0.5:5000", "", "198.51.100.2", "198.51.100.2:5000"},
		{"untrusted peer: headers ignored", "198.51.100.9:5000", "1.1.1.1", "2.2.2.2", "198.51.100.9:5000"},
		{"malformed header: keep the proxy", "172.21.0.1:5000", "not-an-ip", "", "172.21.0.1:5000"},
		{"no header: keep the proxy", "172.21.0.1:5000", "", "", "172.21.0.1:5000"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.realIP != "" {
			r.Header.Set("X-Real-IP", tc.realIP)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if seen != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, seen, tc.want)
		}
	}
}

func TestRealIPDisabled(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if h := RealIP(nil, next); h == nil {
		t.Fatal("nil handler")
	}
	if _, err := ParseTrustedProxies("10.0.0.0/33"); err == nil {
		t.Error("bad CIDR accepted")
	}
	if _, err := ParseTrustedProxies("proxy.example.com"); err == nil {
		t.Error("host name accepted")
	}
}
