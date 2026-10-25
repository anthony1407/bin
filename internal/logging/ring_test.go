package logging

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRingKeepsLatest(t *testing.T) {
	r := NewRing(3)
	for i := 1; i <= 5; i++ {
		r.add(Entry{Message: fmt.Sprint(i)})
	}
	all, latest := r.Since(0, 10)
	if latest != 5 || len(all) != 3 || all[0].Message != "3" || all[2].Message != "5" {
		t.Fatalf("since 0: %v latest %d", all, latest)
	}
	newer, _ := r.Since(4, 10)
	if len(newer) != 1 || newer[0].Seq != 5 {
		t.Errorf("since 4: %v", newer)
	}
	last2, _ := r.Since(0, 2)
	if len(last2) != 2 || last2[0].Message != "4" {
		t.Errorf("latest two: %v", last2)
	}
}

func TestTeeMirrorsTheLog(t *testing.T) {
	var out bytes.Buffer
	ring := NewRing(10)
	log := slog.New(Tee(New(&out, "text", slog.LevelInfo).Handler(), ring)).With("api", "s3")
	log.Debug("hidden")
	log.Info("request done", "status", 200)
	entries, _ := ring.Since(0, 10)
	if len(entries) != 1 {
		t.Fatalf("entries: %v", entries)
	}
	e := entries[0]
	if e.Level != "INFO" || e.Message != "request done" || e.Attrs != "api=s3 status=200" {
		t.Errorf("entry: %+v", e)
	}
	if !strings.Contains(out.String(), "request done api=s3 status=200") {
		t.Errorf("process output changed: %q", out.String())
	}
}
