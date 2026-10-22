// Package audit records who did what, from the console and the S3 API, in
// the metadata database. Entries are written in the background so requests
// never wait for them; old entries are pruned by a retention setting.
package audit

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"bin/internal/storage"
)

// Sources of entries.
const (
	Console = "console"
	S3      = "s3"
)

// Anonymous is the actor of unsigned requests.
const Anonymous = "(anonymous)"

// RetentionSetting holds how many days entries are kept; "0" keeps them forever.
const RetentionSetting = "audit_retention_days"

// DefaultRetentionDays applies until the setting is changed.
const DefaultRetentionDays = 30

const (
	queueSize     = 4096
	batchSize     = 256
	pruneInterval = time.Hour
)

// Recorder queues entries and writes them in batches. A nil *Recorder
// records nothing, so handlers can run without one (e.g. in tests).
type Recorder struct {
	store *storage.Store
	log   *slog.Logger
	queue chan storage.AuditEntry
	flush chan chan struct{}
	stop  context.CancelFunc
	done  chan struct{}

	mu      sync.Mutex
	dropped int64
}

// New starts a recorder writing to store.
func New(store *storage.Store, log *slog.Logger) *Recorder {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Recorder{
		store: store,
		log:   log,
		queue: make(chan storage.AuditEntry, queueSize),
		flush: make(chan chan struct{}),
		stop:  cancel,
		done:  make(chan struct{}),
	}
	go r.run(ctx)
	return r
}

// Record queues an entry. When the queue is full (the database cannot keep
// up) the entry is dropped and counted, rather than slowing requests down.
func (r *Recorder) Record(e storage.AuditEntry) {
	if r == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	select {
	case r.queue <- e:
	default:
		r.mu.Lock()
		r.dropped++
		r.mu.Unlock()
	}
}

// Flush waits until every entry queued so far is stored.
func (r *Recorder) Flush() {
	if r == nil {
		return
	}
	ack := make(chan struct{})
	select {
	case r.flush <- ack:
		<-ack
	case <-r.done:
	}
}

// Close stores what is queued and stops the recorder.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.stop()
	<-r.done
}

func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	prune := time.NewTicker(pruneInterval)
	defer prune.Stop()
	r.prune()
	for {
		select {
		case e := <-r.queue:
			r.write(e)
		case ack := <-r.flush:
			r.drain()
			close(ack)
		case <-prune.C:
			r.prune()
		case <-ctx.Done():
			r.drain()
			return
		}
	}
}

// write stores first plus whatever else is already queued, in batches.
func (r *Recorder) write(first storage.AuditEntry) {
	batch := []storage.AuditEntry{first}
	for len(batch) < batchSize {
		select {
		case e := <-r.queue:
			batch = append(batch, e)
			continue
		default:
		}
		break
	}
	if err := r.store.AddAudit(context.Background(), batch...); err != nil {
		r.log.Error("cannot write the audit log", "entries", len(batch), "err", err)
	}
	r.mu.Lock()
	dropped := r.dropped
	r.dropped = 0
	r.mu.Unlock()
	if dropped > 0 {
		r.log.Warn("audit log queue was full; entries were dropped", "dropped", dropped)
	}
}

func (r *Recorder) drain() {
	for {
		select {
		case e := <-r.queue:
			r.write(e)
		default:
			return
		}
	}
}

// RetentionDays returns the configured retention.
func RetentionDays(ctx context.Context, store *storage.Store) (int, error) {
	v, ok, err := store.Setting(ctx, RetentionSetting)
	if err != nil || !ok {
		return DefaultRetentionDays, err
	}
	days, err := strconv.Atoi(v)
	if err != nil || days < 0 {
		return DefaultRetentionDays, nil
	}
	return days, nil
}

func (r *Recorder) prune() {
	ctx := context.Background()
	days, err := RetentionDays(ctx, r.store)
	if err != nil || days == 0 {
		return
	}
	n, err := r.store.PruneAudit(ctx, time.Now().AddDate(0, 0, -days))
	if err != nil {
		r.log.Error("cannot prune the audit log", "err", err)
	} else if n > 0 {
		r.log.Info("pruned the audit log", "entries", n, "retention_days", days)
	}
}
