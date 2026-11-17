package storage

import (
	"context"
	"encoding/json"
	"time"
)

// TransferJob is one bucket copied by an import or an export, kept so the
// history outlives a restart.
type TransferJob struct {
	ID        string
	Kind      string // "import" or "export"
	Bucket    string
	Endpoint  string
	Source    string // the saved import or export it ran from, if any
	State     string // queued, running, done, failed, canceled
	Copied    int
	Failed    int
	Unchanged int
	Bytes     int64
	Update    bool
	Error     string
	Settings  []string
	Notes     []string
	Started   time.Time
	Finished  time.Time // zero: not finished
}

// MaxTransferJobs is how many jobs of each kind the history keeps.
const MaxTransferJobs = 500

// InterruptedJobError is the error of a job bin stopped before it finished.
const InterruptedJobError = "Interrupted: bin stopped before the job finished. Run it again to continue; what was already copied is kept."

const transferJobColumns = `id, kind, bucket, endpoint, source, state, copied, failed, unchanged, bytes,
	is_update, error, settings, notes, started_at, finished_at`

func encodeList(l []string) string {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// SaveTransferJob records a job as it is now, replacing what was saved before.
func (s *Store) SaveTransferJob(ctx context.Context, j TransferJob) error {
	var finished int64
	if !j.Finished.IsZero() {
		finished = j.Finished.UnixNano()
	}
	_, err := s.exec(ctx, `INSERT INTO transfer_jobs (`+transferJobColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET state = excluded.state, copied = excluded.copied,
			failed = excluded.failed, unchanged = excluded.unchanged, bytes = excluded.bytes,
			is_update = excluded.is_update, error = excluded.error, settings = excluded.settings,
			notes = excluded.notes, finished_at = excluded.finished_at`,
		j.ID, j.Kind, j.Bucket, j.Endpoint, j.Source, j.State, j.Copied, j.Failed, j.Unchanged, j.Bytes,
		j.Update, j.Error, encodeList(j.Settings), encodeList(j.Notes), j.Started.UnixNano(), finished)
	return err
}

// ListTransferJobs returns the jobs of one kind, newest first.
func (s *Store) ListTransferJobs(ctx context.Context, kind string) ([]TransferJob, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+transferJobColumns+` FROM transfer_jobs
		WHERE kind = ? ORDER BY started_at DESC, id LIMIT ?`, kind, MaxTransferJobs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TransferJob
	for rows.Next() {
		var j TransferJob
		var settings, notes string
		var started, finished int64
		if err := rows.Scan(&j.ID, &j.Kind, &j.Bucket, &j.Endpoint, &j.Source, &j.State, &j.Copied, &j.Failed,
			&j.Unchanged, &j.Bytes, &j.Update, &j.Error, &settings, &notes, &started, &finished); err != nil {
			return nil, err
		}
		j.Started, j.Finished = fromNanos(started), timeOrZero(finished)
		if json.Unmarshal([]byte(settings), &j.Settings) != nil || json.Unmarshal([]byte(notes), &j.Notes) != nil {
			j.Settings, j.Notes = nil, nil
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// InterruptTransferJobs marks the jobs left queued or running by a previous
// run of bin as failed, and returns how many there were.
func (s *Store) InterruptTransferJobs(ctx context.Context) (int64, error) {
	res, err := s.exec(ctx, `UPDATE transfer_jobs SET state = 'failed', error = ?, finished_at = ?
		WHERE state IN ('queued', 'running')`, InterruptedJobError, s.nowNanos())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneTransferJobs keeps the newest MaxTransferJobs finished jobs of a kind.
func (s *Store) PruneTransferJobs(ctx context.Context, kind string) error {
	_, err := s.exec(ctx, `DELETE FROM transfer_jobs WHERE kind = ? AND finished_at != 0 AND id NOT IN (
		SELECT id FROM transfer_jobs WHERE kind = ? ORDER BY started_at DESC, id LIMIT ?)`, kind, kind, MaxTransferJobs)
	return err
}
