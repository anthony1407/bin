package s3

import (
	"net/http"
	"net/url"
	"strings"

	"bin/internal/s3/s3err"
)

// Op names an S3 API operation, using the AWS operation names.
type Op string

const (
	OpListBuckets             Op = "ListBuckets"
	OpCreateBucket            Op = "CreateBucket"
	OpDeleteBucket            Op = "DeleteBucket"
	OpHeadBucket              Op = "HeadBucket"
	OpGetBucketLocation       Op = "GetBucketLocation"
	OpGetBucketVersioning     Op = "GetBucketVersioning"
	OpPutBucketVersioning     Op = "PutBucketVersioning"
	OpGetBucketTagging        Op = "GetBucketTagging"
	OpPutBucketTagging        Op = "PutBucketTagging"
	OpDeleteBucketTagging     Op = "DeleteBucketTagging"
	OpGetBucketCors           Op = "GetBucketCors"
	OpPutBucketCors           Op = "PutBucketCors"
	OpDeleteBucketCors        Op = "DeleteBucketCors"
	OpListObjects             Op = "ListObjects"
	OpListObjectsV2           Op = "ListObjectsV2"
	OpListObjectVersions      Op = "ListObjectVersions"
	OpListMultipartUploads    Op = "ListMultipartUploads"
	OpDeleteObjects           Op = "DeleteObjects"
	OpPutObject               Op = "PutObject"
	OpCopyObject              Op = "CopyObject"
	OpGetObject               Op = "GetObject"
	OpHeadObject              Op = "HeadObject"
	OpDeleteObject            Op = "DeleteObject"
	OpCreateMultipartUpload   Op = "CreateMultipartUpload"
	OpUploadPart              Op = "UploadPart"
	OpUploadPartCopy          Op = "UploadPartCopy"
	OpCompleteMultipartUpload Op = "CompleteMultipartUpload"
	OpAbortMultipartUpload    Op = "AbortMultipartUpload"
	OpListParts               Op = "ListParts"
)

// Request is a parsed S3 request target.
type Request struct {
	Op     Op
	Bucket string
	Key    string
}

// unsupported lists S3 sub-resources bin does not implement. Requests that
// carry one get NotImplemented instead of being mistaken for a plain
// GetObject/PutObject (e.g. "PUT /b?policy" must not create a bucket).
var unsupported = []string{
	"accelerate", "acl", "analytics", "attributes", "encryption",
	"intelligent-tiering", "inventory", "legal-hold", "lifecycle", "logging",
	"metrics", "notification", "object-lock", "ownershipControls", "policy",
	"policyStatus", "publicAccessBlock", "replication", "requestPayment",
	"restore", "retention", "select", "torrent", "website",
}

// parseTarget extracts bucket and key from a request. With a configured domain,
// a Host of "<bucket>.<domain>" selects virtual-host style; otherwise the first
// path segment is the bucket (path style).
func parseTarget(r *http.Request, domain string) (bucket, key string) {
	path := r.URL.Path
	if domain != "" {
		host := strings.ToLower(r.Host)
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		if b, ok := strings.CutSuffix(host, "."+domain); ok && b != "" {
			return b, strings.TrimPrefix(path, "/")
		}
	}
	path = strings.TrimPrefix(path, "/")
	bucket, key, _ = strings.Cut(path, "/")
	return bucket, key
}

// Resolve maps a request to an S3 operation. It returns an *s3err.Error for
// requests that are well-formed HTTP but no supported operation.
func Resolve(r *http.Request, domain string) (Request, error) {
	bucket, key := parseTarget(r, domain)
	req := Request{Bucket: bucket, Key: key}
	q := r.URL.Query()

	for _, sub := range unsupported {
		if q.Has(sub) {
			return req, s3err.ErrNotImplemented
		}
	}
	// Versioning, CORS and version listings are bucket sub-resources, and
	// bucket tags are supported but object tags are not (yet).
	for _, sub := range []string{"versioning", "cors", "versions", "tagging"} {
		if q.Has(sub) && key != "" {
			return req, s3err.ErrNotImplemented
		}
	}

	switch {
	case bucket == "":
		if r.Method == http.MethodGet {
			req.Op = OpListBuckets
			return req, nil
		}
	case key == "":
		req.Op = bucketOp(r.Method, q)
	default:
		req.Op = objectOp(r.Method, q, r.Header.Get("X-Amz-Copy-Source") != "")
	}
	if req.Op == "" {
		return req, s3err.ErrMethodNotAllowed
	}
	return req, nil
}

func bucketOp(method string, q url.Values) Op {
	switch method {
	case http.MethodGet:
		switch {
		case q.Has("location"):
			return OpGetBucketLocation
		case q.Has("versioning"):
			return OpGetBucketVersioning
		case q.Has("tagging"):
			return OpGetBucketTagging
		case q.Has("cors"):
			return OpGetBucketCors
		case q.Has("versions"):
			return OpListObjectVersions
		case q.Has("uploads"):
			return OpListMultipartUploads
		case q.Get("list-type") == "2":
			return OpListObjectsV2
		default:
			return OpListObjects
		}
	case http.MethodPut:
		switch {
		case q.Has("versioning"):
			return OpPutBucketVersioning
		case q.Has("tagging"):
			return OpPutBucketTagging
		case q.Has("cors"):
			return OpPutBucketCors
		case q.Has("versions"):
			return ""
		}
		return OpCreateBucket
	case http.MethodDelete:
		switch {
		case q.Has("tagging"):
			return OpDeleteBucketTagging
		case q.Has("cors"):
			return OpDeleteBucketCors
		case q.Has("versioning"), q.Has("versions"):
			return ""
		}
		return OpDeleteBucket
	case http.MethodHead:
		return OpHeadBucket
	case http.MethodPost:
		if q.Has("delete") {
			return OpDeleteObjects
		}
	}
	return ""
}

func objectOp(method string, q url.Values, copySource bool) Op {
	switch method {
	case http.MethodGet:
		if q.Has("uploadId") {
			return OpListParts
		}
		return OpGetObject
	case http.MethodHead:
		return OpHeadObject
	case http.MethodPut:
		switch {
		case q.Has("uploadId") && copySource:
			return OpUploadPartCopy
		case q.Has("uploadId"):
			return OpUploadPart
		case copySource:
			return OpCopyObject
		default:
			return OpPutObject
		}
	case http.MethodPost:
		switch {
		case q.Has("uploads"):
			return OpCreateMultipartUpload
		case q.Has("uploadId"):
			return OpCompleteMultipartUpload
		}
	case http.MethodDelete:
		if q.Has("uploadId") {
			return OpAbortMultipartUpload
		}
		return OpDeleteObject
	}
	return ""
}
