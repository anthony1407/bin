// Package admin implements the console listener: the JSON admin API under
// /api/v1 used by the web panel, and the panel itself (Options.Panel).
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"bin/internal/audit"
	"bin/internal/storage"
)

type importTestRequest struct {
	Endpoint    string `json:"endpoint"`
	AccessKey   string `json:"accessKey"`
	SecretKey   string `json:"secretKey"`
	Region      string `json:"region"`
}

type importTestResponse struct {
	Buckets []remoteBucketInfo `json:"buckets"`
}

type remoteBucketInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Objects  int    `json:"objects"`
}

type importStartRequest struct {
	Endpoint    string   `json:"endpoint"`
	AccessKey   string   `json:"accessKey"`
	SecretKey   string   `json:"secretKey"`
	Region      string   `json:"region"`
	Buckets     []string `json:"buckets"`
}

type importJob struct {
	Bucket string `json:"bucket"`
	JobID  string `json:"jobId"`
}

type importStartResponse struct {
	Jobs []importJob `json:"jobs"`
}

// handleImportTest connects to a remote S3-compatible server and lists its buckets.
func (h *Handler) handleImportTest(w http.ResponseWriter, r *http.Request) {
	var req importTestRequest
	if !readJSON(w, r, &req) {
		return
	}

	if req.Endpoint == "" || req.AccessKey == "" || req.SecretKey == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "endpoint, accessKey, and secretKey are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	buckets, err := h.listRemoteBuckets(ctx, req)
	if err != nil {
		h.opts.Log.Error("import test failed", "err", err)
		writeError(w, http.StatusBadGateway, "remote_error", "Failed to connect to remote server: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, importTestResponse{Buckets: buckets})
}

// handleImportStart starts importing selected buckets from a remote S3 server.
func (h *Handler) handleImportStart(w http.ResponseWriter, r *http.Request) {
	var req importStartRequest
	if !readJSON(w, r, &req) {
		return
	}

	if req.Endpoint == "" || req.AccessKey == "" || req.SecretKey == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "endpoint, accessKey, and secretKey are required")
		return
	}
	if len(req.Buckets) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "at least one bucket must be selected")
		return
	}

	ctx := r.Context()
	jobs := make([]importJob, 0, len(req.Buckets))

	// Get actor from session
	var actor string
	if sess, ok := r.Context().Value(sessionKey{}).(storage.Session); ok {
		actor = sess.User
	}
	if actor == "" {
		actor = audit.Anonymous
	}

	for _, bucketName := range req.Buckets {
		jobID := fmt.Sprintf("import-%s-%d", bucketName, time.Now().UnixMilli())

		// Check if bucket already exists locally
		_, err := h.opts.Store.GetBucket(ctx, bucketName)
		if err == nil {
			// Bucket exists, skip
			continue
		}
		if !errors.Is(err, storage.ErrNoSuchBucket) {
			h.opts.Log.Error("import: checking local bucket", "bucket", bucketName, "err", err)
			continue
		}

		// Create local bucket
		if err := h.opts.Store.CreateBucket(ctx, bucketName); err != nil {
			h.opts.Log.Error("import: creating local bucket", "bucket", bucketName, "err", err)
			continue
		}

		// Start background import
		go h.runImportJob(context.Background(), jobID, req, bucketName)

		jobs = append(jobs, importJob{Bucket: bucketName, JobID: jobID})
	}

	// Audit log
	if h.opts.Audit != nil {
		h.opts.Audit.Record(storage.AuditEntry{
			Source:   audit.Console,
			Actor:    actor,
			Action:   "import_start",
			Resource: strings.Join(req.Buckets, ","),
			Status:   http.StatusOK,
		})
	}

	writeJSON(w, http.StatusOK, importStartResponse{Jobs: jobs})
}

func (h *Handler) listRemoteBuckets(ctx context.Context, req importTestRequest) ([]remoteBucketInfo, error) {
	// Parse endpoint
	endpoint := strings.TrimPrefix(req.Endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	useSSL := strings.HasPrefix(req.Endpoint, "https://")

	// Create MinIO client
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(req.AccessKey, req.SecretKey, ""),
		Secure: useSSL,
		Region: req.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("creating S3 client: %w", err)
	}

	// List buckets
	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing buckets: %w", err)
	}

	var result []remoteBucketInfo
	for _, b := range buckets {
		// Get bucket stats (size and object count)
		size, objects := h.getBucketStats(ctx, client, b.Name)
		result = append(result, remoteBucketInfo{
			Name:    b.Name,
			Size:    size,
			Objects: objects,
		})
	}

	return result, nil
}

func (h *Handler) getBucketStats(ctx context.Context, client *minio.Client, bucketName string) (int64, int) {
	// List objects to count them and sum sizes
	// Use a reasonable limit to avoid timeout on huge buckets
	var totalSize int64
	var count int

	opts := minio.ListObjectsOptions{
		Recursive: true,
		MaxKeys:   10000, // Limit to avoid long operations
	}

	for obj := range client.ListObjects(ctx, bucketName, opts) {
		if obj.Err != nil {
			h.opts.Log.Debug("import: error listing objects", "bucket", bucketName, "err", obj.Err)
			break
		}
		totalSize += obj.Size
		count++
	}

	return totalSize, count
}

func (h *Handler) runImportJob(ctx context.Context, jobID string, req importStartRequest, bucketName string) {
	h.opts.Log.Info("import job started", "jobID", jobID, "bucket", bucketName)

	// Create remote client
	endpoint := strings.TrimPrefix(req.Endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	useSSL := strings.HasPrefix(req.Endpoint, "https://")

	remoteClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(req.AccessKey, req.SecretKey, ""),
		Secure: useSSL,
		Region: req.Region,
	})
	if err != nil {
		h.opts.Log.Error("import: creating remote client", "jobID", jobID, "err", err)
		return
	}

	// List and copy objects
	opts := minio.ListObjectsOptions{
		Recursive: true,
	}

	copied := 0
	failed := 0

	for obj := range remoteClient.ListObjects(ctx, bucketName, opts) {
		if obj.Err != nil {
			h.opts.Log.Error("import: list error", "jobID", jobID, "err", obj.Err)
			break
		}

		// Download from remote
		remoteObj, err := remoteClient.GetObject(ctx, bucketName, obj.Key, minio.GetObjectOptions{})
		if err != nil {
			h.opts.Log.Error("import: get object failed", "jobID", jobID, "key", obj.Key, "err", err)
			failed++
			continue
		}

		// Upload to local storage
		_, err = h.opts.Store.PutObject(ctx, bucketName, obj.Key, remoteObj, obj.Size, storage.PutOptions{
			ContentType: obj.ContentType,
		})
		remoteObj.Close()

		if err != nil {
			h.opts.Log.Error("import: put object failed", "jobID", jobID, "key", obj.Key, "err", err)
			failed++
			continue
		}

		copied++
	}

	h.opts.Log.Info("import job completed", "jobID", jobID, "bucket", bucketName, "copied", copied, "failed", failed)
}