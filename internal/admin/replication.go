package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"

	"bin/internal/storage"
)

// Replication keeps a local bucket folder in step with one on another S3
// server. A rule pushes (local -> remote) or pulls (remote -> local) every
// interval, and on demand. Each run compares both listings with what the
// rule copied last time (replication_state), copies what is new or changed
// on the source, and, with mirror deletes, removes from the destination what
// it once copied and the source no longer has. It never touches objects it
// did not copy. Runs are periodic rather than per write, so a rule catches up
// by itself after the server or the remote was down.

const (
	minReplicationInterval = time.Minute
	maxReplicationInterval = 24 * time.Hour
	// replicationTick is how often the scheduler looks for rules that are due.
	replicationTick = 30 * time.Second
	// replicationPartSize keeps objects up to this size in one PUT, so the
	// remote ETag is the plain MD5 and comparisons stay cheap.
	replicationPartSize = 64 << 20
)

var errNoRemoteBucket = errors.New("remote bucket does not exist")

// replicator tracks running rules so a rule never runs twice at once and a
// running one can be stopped when it is paused, changed or deleted.
type replicator struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newReplicator() *replicator { return &replicator{running: map[string]context.CancelFunc{}} }

// start marks id running and returns its context, or false if it already runs.
func (rp *replicator) start(id string) (context.Context, bool) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if _, ok := rp.running[id]; ok {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	rp.running[id] = cancel
	return ctx, true
}

func (rp *replicator) done(id string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if cancel, ok := rp.running[id]; ok {
		cancel()
		delete(rp.running, id)
	}
}

func (rp *replicator) stop(id string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if cancel, ok := rp.running[id]; ok {
		cancel()
	}
}

func (rp *replicator) isRunning(id string) bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	_, ok := rp.running[id]
	return ok
}

// runReplicationScheduler starts due rules until the process ends.
func (h *Handler) runReplicationScheduler() {
	t := time.NewTicker(replicationTick)
	defer t.Stop()
	for range t.C {
		rules, err := h.opts.Store.ListReplicationRules(context.Background())
		if err != nil {
			h.opts.Log.Error("replication: listing rules", "err", err)
			continue
		}
		now := time.Now()
		for _, r := range rules {
			if r.Enabled && !r.LastRun.Add(r.Interval).After(now) {
				h.startReplication(r.ID)
			}
		}
	}
}

// startReplication runs a rule in the background unless it is running.
func (h *Handler) startReplication(id string) bool {
	ctx, ok := h.repl.start(id)
	if !ok {
		return false
	}
	go func() {
		defer h.repl.done(id)
		h.runReplication(ctx, id)
	}()
	return true
}

// runReplication does one run of a rule and records the outcome.
func (h *Handler) runReplication(ctx context.Context, id string) {
	rule, err := h.opts.Store.ReplicationRule(ctx, id)
	if err != nil {
		return // deleted meanwhile
	}
	run := storage.ReplicationRun{Status: "ok"}
	started := time.Now()
	h.opts.Log.Info("replication run started", "rule", rule.Name, "direction", rule.Direction,
		"bucket", rule.Bucket, "remote_bucket", rule.RemoteBucket)
	err = h.replicate(ctx, rule, &run)
	run.At = time.Now()
	switch {
	case errors.Is(err, context.Canceled):
		run.Status, run.Error = "error", "Stopped before it finished."
	case errors.Is(err, errNoRemoteBucket):
		run.Status, run.Error = "error", "The remote bucket "+rule.RemoteBucket+" does not exist."
	case err != nil:
		_, run.Error = remoteErrorMessage(err)
		run.Status = "error"
		h.opts.Log.Warn("replication run failed", "rule", rule.Name, "err", err)
	case run.Failed > 0:
		run.Status, run.Error = "error", pluralObjects(run.Failed)+" could not be copied; the server log lists each one."
	}
	if err := h.opts.Store.RecordReplicationRun(context.Background(), id, run); err != nil {
		h.opts.Log.Error("replication: recording run", "rule", rule.Name, "err", err)
	}
	h.opts.Log.Info("replication run finished", "rule", rule.Name, "status", run.Status, "copied", run.Copied,
		"deleted", run.Deleted, "failed", run.Failed, "bytes", run.Bytes, "took", time.Since(started).Round(time.Millisecond))
}

func pluralObjects(n int) string {
	if n == 1 {
		return "1 object"
	}
	return strconv.Itoa(n) + " objects"
}

// replicate copies in the rule's direction, then mirrors deletes if asked.
func (h *Handler) replicate(ctx context.Context, rule storage.ReplicationRule, run *storage.ReplicationRun) error {
	secret, err := h.opts.Keys.Box.Decrypt(rule.SecretEnc)
	if err != nil {
		return err
	}
	client, err := remoteClient(importRemote{Endpoint: rule.Endpoint, AccessKey: rule.AccessKey, SecretKey: secret, Region: rule.Region})
	if err != nil {
		return err
	}
	if ok, err := client.BucketExists(ctx, rule.RemoteBucket); err != nil {
		return err
	} else if !ok {
		return errNoRemoteBucket
	}
	state, err := h.opts.Store.ReplicationState(ctx, rule.ID)
	if err != nil {
		return err
	}
	remote, err := remoteListing(ctx, client, rule.RemoteBucket, rule.RemotePrefix)
	if err != nil {
		return err
	}
	if rule.Direction == "pull" {
		return h.pull(ctx, client, rule, state, remote, run)
	}
	return h.push(ctx, client, rule, state, remote, run)
}

// remoteListing maps relative keys under prefix to their ETags.
func remoteListing(ctx context.Context, client *minio.Client, bucket, prefix string) (map[string]string, error) {
	out := map[string]string{}
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out[strings.TrimPrefix(obj.Key, prefix)] = strings.Trim(obj.ETag, `"`)
	}
	return out, nil
}

// push sends local objects that are new or changed since the last copy.
func (h *Handler) push(ctx context.Context, client *minio.Client, rule storage.ReplicationRule,
	state map[string]storage.ReplicatedObject, remote map[string]string, run *storage.ReplicationRun) error {
	seen := map[string]bool{}
	after := ""
	for {
		page, err := h.opts.Store.ListObjects(ctx, rule.Bucket, storage.ListOptions{Prefix: rule.Prefix, After: after, MaxKeys: 1000})
		if err != nil {
			return err
		}
		for _, o := range page.Objects {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel := strings.TrimPrefix(o.Key, rule.Prefix)
			seen[rel] = true
			last, copied := state[rel]
			if copied && last.LocalETag == o.ETag && remote[rel] == last.RemoteETag {
				continue // unchanged on both sides since the last copy
			}
			etag, n, err := h.pushObject(ctx, client, rule, o.Key, rule.RemotePrefix+rel)
			if err != nil {
				run.Failed++
				h.opts.Log.Warn("replication: object not pushed", "rule", rule.Name, "key", o.Key, "err", err)
				continue
			}
			if err := h.opts.Store.SetReplicatedObject(ctx, rule.ID, rel, storage.ReplicatedObject{LocalETag: o.ETag, RemoteETag: etag}); err != nil {
				return err
			}
			run.Copied++
			run.Bytes += n
		}
		if !page.IsTruncated {
			break
		}
		after = page.NextMarker
	}
	if !rule.MirrorDeletes {
		return nil
	}
	for rel, last := range state {
		if seen[rel] {
			continue
		}
		// Only what this rule wrote and nobody changed since.
		if etag, ok := remote[rel]; ok && etag == last.RemoteETag {
			if err := client.RemoveObject(ctx, rule.RemoteBucket, rule.RemotePrefix+rel, minio.RemoveObjectOptions{}); err != nil {
				run.Failed++
				h.opts.Log.Warn("replication: remote delete failed", "rule", rule.Name, "key", rel, "err", err)
				continue
			}
			run.Deleted++
		}
		if err := h.opts.Store.ForgetReplicatedObject(ctx, rule.ID, rel); err != nil {
			return err
		}
	}
	return nil
}

// pushObject uploads one local object; it returns the remote ETag and size.
func (h *Handler) pushObject(ctx context.Context, client *minio.Client, rule storage.ReplicationRule, key, remoteKey string) (string, int64, error) {
	info, data, err := h.opts.Store.OpenObject(ctx, rule.Bucket, key)
	if err != nil {
		return "", 0, err
	}
	defer data.Close()
	meta := map[string]string{}
	for k, v := range info.UserMeta {
		meta[k] = v
	}
	up, err := client.PutObject(ctx, rule.RemoteBucket, remoteKey, data.Verified(), info.Size, minio.PutObjectOptions{
		ContentType:  info.ContentType,
		UserMetadata: meta,
		PartSize:     replicationPartSize,
	})
	if err != nil {
		return "", 0, err
	}
	return strings.Trim(up.ETag, `"`), info.Size, nil
}

// pull fetches remote objects that are new or changed since the last copy.
func (h *Handler) pull(ctx context.Context, client *minio.Client, rule storage.ReplicationRule,
	state map[string]storage.ReplicatedObject, remote map[string]string, run *storage.ReplicationRun) error {
	for rel, remoteETag := range remote {
		if err := ctx.Err(); err != nil {
			return err
		}
		localKey := rule.Prefix + rel
		last, copied := state[rel]
		if copied && last.RemoteETag == remoteETag {
			if cur, err := h.opts.Store.StatObject(ctx, rule.Bucket, localKey); err == nil && cur.ETag == last.LocalETag {
				continue // unchanged on both sides since the last copy
			}
		}
		info, etag, err := h.copyFromRemote(ctx, client, rule.RemoteBucket, rule.RemotePrefix+rel, rule.Bucket, localKey)
		if err != nil {
			run.Failed++
			h.opts.Log.Warn("replication: object not pulled", "rule", rule.Name, "key", rel, "err", err)
			continue
		}
		if err := h.opts.Store.SetReplicatedObject(ctx, rule.ID, rel, storage.ReplicatedObject{LocalETag: info.ETag, RemoteETag: strings.Trim(etag, `"`)}); err != nil {
			return err
		}
		run.Copied++
		run.Bytes += info.Size
	}
	if !rule.MirrorDeletes {
		return nil
	}
	for rel, last := range state {
		if _, ok := remote[rel]; ok {
			continue
		}
		localKey := rule.Prefix + rel
		// Only what this rule wrote and nobody changed since.
		if cur, err := h.opts.Store.StatObject(ctx, rule.Bucket, localKey); err == nil && cur.ETag == last.LocalETag {
			if err := h.opts.Store.DeleteObject(ctx, rule.Bucket, localKey); err != nil {
				run.Failed++
				h.opts.Log.Warn("replication: local delete failed", "rule", rule.Name, "key", localKey, "err", err)
				continue
			}
			run.Deleted++
		}
		if err := h.opts.Store.ForgetReplicatedObject(ctx, rule.ID, rel); err != nil {
			return err
		}
	}
	return nil
}

// --- API -----------------------------------------------------------------------------------

type replicationRuleJSON struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Direction       string     `json:"direction"`
	Bucket          string     `json:"bucket"`
	Prefix          string     `json:"prefix"`
	Endpoint        string     `json:"endpoint"`
	AccessKey       string     `json:"accessKey"`
	SecretKey       string     `json:"secretKey,omitempty"` // write only; empty keeps the stored one
	SecretKeySet    bool       `json:"secretKeySet"`
	Region          string     `json:"region"`
	RemoteBucket    string     `json:"remoteBucket"`
	RemotePrefix    string     `json:"remotePrefix"`
	IntervalSeconds int64      `json:"intervalSeconds"`
	MirrorDeletes   bool       `json:"mirrorDeletes"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"createdAt"`
	Running         bool       `json:"running"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastStatus      string     `json:"lastStatus,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	LastCopied      int        `json:"lastCopied"`
	LastDeleted     int        `json:"lastDeleted"`
	LastFailed      int        `json:"lastFailed"`
	LastBytes       int64      `json:"lastBytes"`
	TotalCopied     int64      `json:"totalCopied"`
	TotalBytes      int64      `json:"totalBytes"`
}

func (h *Handler) toReplicationJSON(r storage.ReplicationRule) replicationRuleJSON {
	j := replicationRuleJSON{
		ID: r.ID, Name: r.Name, Direction: r.Direction, Bucket: r.Bucket, Prefix: r.Prefix, Endpoint: r.Endpoint,
		AccessKey: r.AccessKey, SecretKeySet: r.SecretEnc != "", Region: r.Region, RemoteBucket: r.RemoteBucket,
		RemotePrefix: r.RemotePrefix, IntervalSeconds: int64(r.Interval / time.Second), MirrorDeletes: r.MirrorDeletes,
		Enabled: r.Enabled, CreatedAt: r.Created, Running: h.repl.isRunning(r.ID), LastRunAt: optionalTime(r.LastRun),
		LastStatus: r.LastStatus, LastError: r.LastError, LastCopied: r.LastCopied, LastDeleted: r.LastDeleted,
		LastFailed: r.LastFailed, LastBytes: r.LastBytes, TotalCopied: r.TotalCopied, TotalBytes: r.TotalBytes,
	}
	if r.Enabled && h.opts.RunReplication {
		next := r.LastRun.Add(r.Interval)
		if next.Before(time.Now()) {
			next = time.Now()
		}
		j.NextRunAt = &next
	}
	return j
}

func (h *Handler) listReplicationRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.opts.Store.ListReplicationRules(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	out := make([]replicationRuleJSON, 0, len(rules))
	for _, rule := range rules {
		out = append(out, h.toReplicationJSON(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// folder normalizes a prefix: no leading slash, a trailing one if not empty.
func folder(p string) string {
	p = strings.TrimLeft(strings.TrimSpace(p), "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// readReplicationRule reads and validates a rule from the body. old is the
// stored rule when updating (its secret is kept if none is given).
func (h *Handler) readReplicationRule(w http.ResponseWriter, r *http.Request, old *storage.ReplicationRule) (storage.ReplicationRule, bool) {
	var in replicationRuleJSON
	if !readJSON(w, r, &in) {
		return storage.ReplicationRule{}, false
	}
	rule := storage.ReplicationRule{
		Name: strings.TrimSpace(in.Name), Direction: in.Direction, Bucket: in.Bucket, Prefix: folder(in.Prefix),
		Endpoint: strings.TrimSpace(in.Endpoint), AccessKey: strings.TrimSpace(in.AccessKey), Region: strings.TrimSpace(in.Region),
		RemoteBucket: strings.TrimSpace(in.RemoteBucket), RemotePrefix: folder(in.RemotePrefix),
		Interval: time.Duration(in.IntervalSeconds) * time.Second, MirrorDeletes: in.MirrorDeletes, Enabled: in.Enabled,
	}
	if old != nil {
		rule.ID, rule.SecretEnc = old.ID, old.SecretEnc
	}
	if in.SecretKey != "" {
		rule.SecretEnc = h.opts.Keys.Box.Encrypt(in.SecretKey)
	}
	fail := func(field, msg string) (storage.ReplicationRule, bool) {
		writeFieldError(w, "invalid_replication_rule", field, msg)
		return storage.ReplicationRule{}, false
	}
	switch {
	case rule.Name == "":
		return fail("name", "Name is required.")
	case len(rule.Name) > 100:
		return fail("name", "Use at most 100 characters.")
	case rule.Direction != "push" && rule.Direction != "pull":
		return fail("direction", `Choose "push" (bin to remote) or "pull" (remote to bin).`)
	case rule.Bucket == "":
		return fail("bucket", "Local bucket is required.")
	case rule.Endpoint == "":
		return fail("endpoint", "Remote endpoint is required.")
	case rule.AccessKey == "":
		return fail("accessKey", "Remote access key is required.")
	case rule.SecretEnc == "":
		return fail("secretKey", "Remote secret key is required.")
	case rule.RemoteBucket == "":
		return fail("remoteBucket", "Remote bucket is required.")
	case !storage.ValidBucketName(rule.RemoteBucket):
		return fail("remoteBucket", "Not a valid bucket name: 3-63 lowercase letters, digits, dots and hyphens.")
	case rule.Interval < minReplicationInterval || rule.Interval > maxReplicationInterval:
		return fail("intervalSeconds", "Run every 1 minute to 24 hours.")
	}
	if _, err := h.opts.Store.GetBucket(r.Context(), rule.Bucket); err != nil {
		return fail("bucket", "This bucket does not exist.")
	}
	if _, err := remoteClient(importRemote{Endpoint: rule.Endpoint, AccessKey: "a", SecretKey: "b"}); err != nil {
		_, msg := remoteErrorMessage(err)
		return fail("endpoint", msg)
	}
	return rule, true
}

func (h *Handler) createReplicationRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := h.readReplicationRule(w, r, nil)
	if !ok {
		return
	}
	rule.ID = "rr_" + randomToken()[:12]
	setAuditBucket(r, rule.Bucket)
	setAuditResource(r, rule.Name)
	if err := h.opts.Store.CreateReplicationRule(r.Context(), rule); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("replication rule created", "rule", rule.Name, "direction", rule.Direction, "bucket", rule.Bucket,
		"endpoint", rule.Endpoint, "remote_bucket", rule.RemoteBucket, "every", rule.Interval.String())
	if rule.Enabled {
		h.startReplication(rule.ID) // a first run right away
	}
	created, err := h.opts.Store.ReplicationRule(r.Context(), rule.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toReplicationJSON(created))
}

func (h *Handler) updateReplicationRule(w http.ResponseWriter, r *http.Request) {
	old, err := h.opts.Store.ReplicationRule(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	rule, ok := h.readReplicationRule(w, r, &old)
	if !ok {
		return
	}
	setAuditBucket(r, rule.Bucket)
	setAuditResource(r, rule.Name)
	h.repl.stop(rule.ID) // a run in progress would use the old settings
	if err := h.opts.Store.UpdateReplicationRule(r.Context(), rule); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("replication rule updated", "rule", rule.Name, "enabled", rule.Enabled)
	updated, err := h.opts.Store.ReplicationRule(r.Context(), rule.ID)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.toReplicationJSON(updated))
}

func (h *Handler) deleteReplicationRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	setAuditResource(r, id)
	h.repl.stop(id)
	if err := h.opts.Store.DeleteReplicationRule(r.Context(), id); err != nil {
		h.storageError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runReplicationNow starts a run at once ("Sync now").
func (h *Handler) runReplicationNow(w http.ResponseWriter, r *http.Request) {
	rule, err := h.opts.Store.ReplicationRule(r.Context(), r.PathValue("id"))
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	setAuditBucket(r, rule.Bucket)
	setAuditResource(r, rule.Name)
	if !h.startReplication(rule.ID) {
		writeError(w, http.StatusConflict, "already_running", "This rule is running already.")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
