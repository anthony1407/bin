package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestRunServesAndShutsDown(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, discard, nil, Listener{Name: "t", Addr: addr, Handler: http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })})
	}()

	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		if resp, err = http.Get("http://" + addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunReportsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	free := freeAddr(t)

	err = Run(context.Background(), discard, nil,
		Listener{Name: "first", Addr: free, Handler: http.NotFoundHandler()},
		Listener{Name: "second", Addr: ln.Addr().String(), Handler: http.NotFoundHandler()},
	)
	if err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("err = %v", err)
	}
	// The first listener must have been released.
	l2, err := net.Listen("tcp", free)
	if err != nil {
		t.Fatalf("first listener leaked: %v", err)
	}
	l2.Close()
}

func TestMiddlewareChain(t *testing.T) {
	var seenID string
	h := WithRequestID("X-Test-Id", AccessLog(discard, LogOptions{Requests: true}, Recover(discard,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(599) },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenID = RequestID(r.Context())
			panic("boom")
		}))))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 599 {
		t.Errorf("panic not recovered, status %d", rec.Code)
	}
	if got := rec.Header().Get("X-Test-Id"); got == "" || got != seenID {
		t.Errorf("request id header %q, context %q", got, seenID)
	}
}

func TestAccessLogLine(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := WithRequestID("X-Id", AccessLog(log, LogOptions{
		Requests: true,
		Quiet:    func(r *http.Request) bool { return r.URL.Path == "/health" },
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/abort":
			w.Write([]byte("partial"))
			panic(http.ErrAbortHandler)
		case "/fail":
			w.WriteHeader(500)
		default:
			Annotate(r.Context(), slog.String("op", "GetObject"))
			w.Write([]byte("hello"))
		}
	})))
	serve := func(target string) {
		defer func() { recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", target, nil))
	}
	serve("/b/k?X-Amz-Signature=secret")
	serve("/health")
	serve("/fail")
	serve("/abort")
	out := buf.String()
	for _, want := range []string{
		`msg="GET /b/k?X-Amz-Signature=REDACTED -> 200 (sent 5 B, `,
		`op=GetObject id=`,
		`level=ERROR msg="GET /fail -> 500 (`,
		`level=ERROR msg="GET /abort -> 200 (sent 7 B, `,
		`ABORTED: connection cut before the response completed`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/health") || strings.Contains(out, "secret") {
		t.Errorf("quiet request or secret logged:\n%s", out)
	}
}

func TestRedactQuery(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"list-type=2&prefix=a%2F": "list-type=2&prefix=a%2F",
		"X-Amz-Credential=ak%2F20261022&X-Amz-Signature=abc123&X-Amz-Expires=300": "X-Amz-Credential=ak%2F20261022&X-Amz-Signature=REDACTED&X-Amz-Expires=300",
		"x-amz-signature=abc&X-Amz-Security-Token=tok":                            "x-amz-signature=REDACTED&X-Amz-Security-Token=REDACTED",
	}
	for in, want := range cases {
		if got := redactQuery(in); got != want {
			t.Errorf("redactQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewRequestIDShape(t *testing.T) {
	a, b := NewRequestID(), NewRequestID()
	if len(a) != 16 || strings.ToUpper(a) != a || a == b {
		t.Errorf("ids %q %q", a, b)
	}
}
