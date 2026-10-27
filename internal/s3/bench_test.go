package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bin/internal/s3/sigv4"
	"bin/internal/storage"
)

// BenchmarkGetObject measures authenticated GETs (presigned URLs, so the
// client does no signing work) over a real HTTP connection.
func BenchmarkGetObject(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 256 << 10, 1 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("size=%dKiB", size>>10), func(b *testing.B) {
			url, client := benchServer(b, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					resp, err := client.Get(url)
					if err != nil {
						b.Fatal(err)
					}
					n, _ := io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK || n != int64(size) {
						b.Fatalf("status %d, %d bytes", resp.StatusCode, n)
					}
				}
			})
		})
	}
}

func benchServer(b *testing.B, size int) (string, *http.Client) {
	b.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(b.TempDir(), log)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.CreateBucket(ctx, "bench"); err != nil {
		b.Fatal(err)
	}
	data := make([]byte, size)
	rand.Read(data)
	if _, err := store.PutObject(ctx, "bench", "obj", bytes.NewReader(data), int64(size), storage.PutOptions{}); err != nil {
		b.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(Options{
		Store:  store,
		Region: "us-east-1",
		Auth:   &sigv4.Verifier{Region: "us-east-1", Store: sigv4.StaticStore{testAK: testSecret}},
		Log:    log,
	}))
	b.Cleanup(srv.Close)
	url, err := sigv4.Presign(http.MethodGet, srv.URL+"/bench/obj", testAK, testSecret, "us-east-1", time.Hour, time.Now())
	if err != nil {
		b.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 256, DisableCompression: true}}
	b.Cleanup(client.CloseIdleConnections)
	return url, client
}
