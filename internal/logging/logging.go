// Package logging builds bin's logger: a compact, human-readable format for
// terminals and `docker logs`, or JSON for log collectors.
//
// Text lines look like:
//
//	2026-10-22 16:43:20 INFO  GET /photos/a.jpg -> 200 (1.2 KB in 3ms) op=GetObject id=18E0DF7A072D60C1
package logging

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// New returns a logger writing to w in format "text" or "json".
func New(w io.Writer, format string, level slog.Level) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(&textHandler{w: w, level: level, mu: &sync.Mutex{}})
}

type textHandler struct {
	w      io.Writer
	level  slog.Level
	mu     *sync.Mutex
	prefix string // pre-rendered attributes from With
	group  string
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	b.WriteString(t.UTC().Format("2006-01-02 15:04:05"))
	b.WriteByte(' ')
	lvl := r.Level.String()
	b.WriteString(lvl)
	b.WriteString(strings.Repeat(" ", max(1, 6-len(lvl))))
	b.WriteString(r.Message)
	b.WriteString(h.prefix)
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.prefix)
	for _, a := range attrs {
		writeAttr(&b, h.group, a)
	}
	c := *h
	c.prefix = b.String()
	return &c
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	c := *h
	if c.group != "" {
		c.group += "."
	}
	c.group += name
	return &c
}

func writeAttr(b *strings.Builder, group string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		g := a.Key
		if group != "" {
			g = group + "." + g
		}
		for _, ga := range a.Value.Group() {
			writeAttr(b, g, ga)
		}
		return
	}
	b.WriteByte(' ')
	if group != "" {
		b.WriteString(group)
		b.WriteByte('.')
	}
	b.WriteString(a.Key)
	b.WriteByte('=')
	var v string
	switch a.Value.Kind() {
	case slog.KindDuration:
		v = a.Value.Duration().Round(time.Microsecond).String()
	case slog.KindTime:
		v = a.Value.Time().UTC().Format(time.RFC3339)
	default:
		v = a.Value.String()
	}
	if v == "" || strings.ContainsAny(v, " \t\n\"=") {
		v = strconv.Quote(v)
	}
	b.WriteString(v)
}

// Size renders a byte count for humans: 512 B, 1.2 KB, 3.4 MB...
func Size(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}
