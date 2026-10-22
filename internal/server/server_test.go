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
		done <- Run(ctx, discard, Listener{Name: "t", Addr: addr, Handler: http.HandlerFunc(
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

	err = Run(context.Background(), discard,
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
	h := WithRequestID("X-Test-Id", AccessLog(discard, Recover(discard,
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

func TestNewRequestIDShape(t *testing.T) {
	a, b := NewRequestID(), NewRequestID()
	if len(a) != 16 || strings.ToUpper(a) != a || a == b {
		t.Errorf("ids %q %q", a, b)
	}
}
