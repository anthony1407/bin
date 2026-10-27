package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrNoSuchReplicationRule = errors.New("no such replication rule")

// ReplicationRule keeps Bucket/Prefix here in step with RemoteBucket/
// RemotePrefix on another S3 server, every Interval.
type ReplicationRule struct {
	ID            string
	Name          string
	Direction     string // "push" (local -> remote) or "pull" (remote -> local)
	Bucket        string
	Prefix        string
	Endpoint      string
	AccessKey     string
	SecretEnc     string // encrypted by the caller
	Region        string
	RemoteBucket  string
	RemotePrefix  string
	Interval      time.Duration
	MirrorDeletes bool
	Enabled       bool
	Created       time.Time

	// The last run, and totals over every run.
	LastRun     time.Time // zero: never
	LastStatus  string    // "", "ok" or "error"
	LastError   string
	LastCopied  int
	LastDeleted int
	LastFailed  int
	LastBytes   int64
	TotalCopied int64
	TotalBytes  int64
}

// ReplicationRun is the outcome of one run.
type ReplicationRun struct {
	At      time.Time
	Status  string
	Error   string
	Copied  int
	Deleted int
	Failed  int
	Bytes   int64
}

// ReplicatedObject is what a rule last copied for one object.
type ReplicatedObject struct {
	LocalETag  string
	RemoteETag string
}

const replicationColumns = `id, name, direction, bucket, prefix, endpoint, access_key, secret_enc, region,
	remote_bucket, remote_prefix, interval_seconds, mirror_deletes, enabled, created_at, last_run_at,
	last_status, last_error, last_copied, last_deleted, last_failed, last_bytes, total_copied, total_bytes`

func scanReplicationRule(scan func(...any) error) (ReplicationRule, error) {
	var r ReplicationRule
	var interval, created, lastRun int64
	err := scan(&r.ID, &r.Name, &r.Direction, &r.Bucket, &r.Prefix, &r.Endpoint, &r.AccessKey, &r.SecretEnc, &r.Region,
		&r.RemoteBucket, &r.RemotePrefix, &interval, &r.MirrorDeletes, &r.Enabled, &created, &lastRun,
		&r.LastStatus, &r.LastError, &r.LastCopied, &r.LastDeleted, &r.LastFailed, &r.LastBytes, &r.TotalCopied, &r.TotalBytes)
	r.Interval = time.Duration(interval) * time.Second
	r.Created, r.LastRun = fromNanos(created), timeOrZero(lastRun)
	return r, err
}

// CreateReplicationRule stores a new rule on an existing bucket.
func (s *Store) CreateReplicationRule(ctx context.Context, r ReplicationRule) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := bucketID(ctx, tx, r.Bucket); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO replication_rules (id, name, direction, bucket, prefix, endpoint,
			access_key, secret_enc, region, remote_bucket, remote_prefix, interval_seconds, mirror_deletes, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, r.Direction, r.Bucket, r.Prefix, r.Endpoint, r.AccessKey, r.SecretEnc, r.Region,
			r.RemoteBucket, r.RemotePrefix, int64(r.Interval/time.Second), r.MirrorDeletes, r.Enabled, s.nowNanos())
		return err
	})
}

// UpdateReplicationRule replaces a rule's settings, keeping its history. A
// change of what is copied (buckets, prefixes, endpoint, direction) forgets
// what was copied before: the next run compares everything again.
func (s *Store) UpdateReplicationRule(ctx context.Context, r ReplicationRule) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := bucketID(ctx, tx, r.Bucket); err != nil {
			return err
		}
		old, err := scanReplicationRule(tx.QueryRowContext(ctx, `SELECT `+replicationColumns+` FROM replication_rules WHERE id = ?`, r.ID).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchReplicationRule
		} else if err != nil {
			return err
		}
		if old.Direction != r.Direction || old.Bucket != r.Bucket || old.Prefix != r.Prefix || old.Endpoint != r.Endpoint ||
			old.RemoteBucket != r.RemoteBucket || old.RemotePrefix != r.RemotePrefix {
			if _, err := tx.ExecContext(ctx, `DELETE FROM replication_state WHERE rule_id = ?`, r.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE replication_rules SET name = ?, direction = ?, bucket = ?, prefix = ?,
			endpoint = ?, access_key = ?, secret_enc = ?, region = ?, remote_bucket = ?, remote_prefix = ?,
			interval_seconds = ?, mirror_deletes = ?, enabled = ? WHERE id = ?`,
			r.Name, r.Direction, r.Bucket, r.Prefix, r.Endpoint, r.AccessKey, r.SecretEnc, r.Region, r.RemoteBucket,
			r.RemotePrefix, int64(r.Interval/time.Second), r.MirrorDeletes, r.Enabled, r.ID)
		return err
	})
}

// ReplicationRule returns one rule.
func (s *Store) ReplicationRule(ctx context.Context, id string) (ReplicationRule, error) {
	r, err := scanReplicationRule(s.read.QueryRowContext(ctx, `SELECT `+replicationColumns+` FROM replication_rules WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ReplicationRule{}, ErrNoSuchReplicationRule
	}
	return r, err
}

// ListReplicationRules returns every rule, oldest first.
func (s *Store) ListReplicationRules(ctx context.Context) ([]ReplicationRule, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+replicationColumns+` FROM replication_rules ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReplicationRule{}
	for rows.Next() {
		r, err := scanReplicationRule(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteReplicationRule removes a rule and what it remembered. Objects it
// copied stay where they are.
func (s *Store) DeleteReplicationRule(ctx context.Context, id string) error {
	res, err := s.exec(ctx, `DELETE FROM replication_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchReplicationRule
	}
	return nil
}

// RecordReplicationRun stores the outcome of a run.
func (s *Store) RecordReplicationRun(ctx context.Context, id string, run ReplicationRun) error {
	_, err := s.exec(ctx, `UPDATE replication_rules SET last_run_at = ?, last_status = ?, last_error = ?,
		last_copied = ?, last_deleted = ?, last_failed = ?, last_bytes = ?,
		total_copied = total_copied + ?, total_bytes = total_bytes + ? WHERE id = ?`,
		run.At.UnixNano(), run.Status, run.Error, run.Copied, run.Deleted, run.Failed, run.Bytes, run.Copied, run.Bytes, id)
	return err
}

// ReplicationState returns what a rule last copied, by relative key.
func (s *Store) ReplicationState(ctx context.Context, id string) (map[string]ReplicatedObject, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT key, local_etag, remote_etag FROM replication_state WHERE rule_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ReplicatedObject{}
	for rows.Next() {
		var key string
		var o ReplicatedObject
		if err := rows.Scan(&key, &o.LocalETag, &o.RemoteETag); err != nil {
			return nil, err
		}
		out[key] = o
	}
	return out, rows.Err()
}

// SetReplicatedObject records that a rule copied one object.
func (s *Store) SetReplicatedObject(ctx context.Context, id, key string, o ReplicatedObject) error {
	_, err := s.exec(ctx, `INSERT INTO replication_state (rule_id, key, local_etag, remote_etag) VALUES (?, ?, ?, ?)
		ON CONFLICT (rule_id, key) DO UPDATE SET local_etag = excluded.local_etag, remote_etag = excluded.remote_etag`,
		id, key, o.LocalETag, o.RemoteETag)
	return err
}

// ForgetReplicatedObject drops a rule's record of one object.
func (s *Store) ForgetReplicatedObject(ctx context.Context, id, key string) error {
	_, err := s.exec(ctx, `DELETE FROM replication_state WHERE rule_id = ? AND key = ?`, id, key)
	return err
}
