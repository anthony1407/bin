package logging

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is one log line kept for the console.
type Entry struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	// Attrs are the line's key=value pairs, rendered as in the text format.
	Attrs string `json:"attrs,omitempty"`
}

// Ring keeps the latest log lines in memory so the console can show the
// server log live, without access to the process output. It holds at most
// its capacity; older lines are dropped.
type Ring struct {
	mu      sync.Mutex
	entries []Entry
	next    int   // where the next entry goes once full
	seq     int64 // sequence number of the last entry
}

// NewRing returns a ring holding up to capacity lines.
func NewRing(capacity int) *Ring {
	return &Ring{entries: make([]Entry, 0, capacity)}
}

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	if len(r.entries) < cap(r.entries) {
		r.entries = append(r.entries, e)
		return
	}
	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
}

// Since returns up to limit lines after sequence number after, oldest first,
// and the sequence number of the newest line kept. after 0 returns the
// latest limit lines.
func (r *Ring) Since(after int64, limit int) ([]Entry, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ordered := make([]Entry, 0, len(r.entries))
	ordered = append(ordered, r.entries[r.next:]...)
	ordered = append(ordered, r.entries[:r.next]...)
	out := []Entry{}
	for _, e := range ordered {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if after == 0 && len(out) > limit {
		out = out[len(out)-limit:]
	} else if len(out) > limit {
		out = out[:limit]
	}
	return out, r.seq
}

// Tee sends every record to h and keeps a copy in ring. It logs exactly what
// h would: the same level, attributes and groups.
func Tee(h slog.Handler, ring *Ring) slog.Handler {
	return &teeHandler{base: h, ring: ring}
}

type teeHandler struct {
	base   slog.Handler
	ring   *Ring
	prefix string // attributes from With, rendered
	group  string
}

func (h *teeHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.base.Enabled(ctx, l) }

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(h.prefix)
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.group, a)
		return true
	})
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.ring.add(Entry{Time: t.UTC(), Level: r.Level.String(), Message: r.Message, Attrs: strings.TrimSpace(b.String())})
	return h.base.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.prefix)
	for _, a := range attrs {
		writeAttr(&b, h.group, a)
	}
	return &teeHandler{base: h.base.WithAttrs(attrs), ring: h.ring, prefix: b.String(), group: h.group}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &teeHandler{base: h.base.WithGroup(name), ring: h.ring, prefix: h.prefix, group: g}
}
