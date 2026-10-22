package s3

import (
	"context"
	"net/http"

	"bin/internal/auth"
	"bin/internal/s3/s3err"
)

// opActions maps each operation to the permission it needs.
var opActions = map[Op]auth.Action{
	OpListBuckets:             auth.ListBuckets,
	OpCreateBucket:            auth.ManageBucket,
	OpDeleteBucket:            auth.ManageBucket,
	OpPutBucketTagging:        auth.ManageBucket,
	OpDeleteBucketTagging:     auth.ManageBucket,
	OpHeadBucket:              auth.BucketInfo,
	OpGetBucketLocation:       auth.BucketInfo,
	OpGetBucketVersioning:     auth.BucketInfo,
	OpGetBucketTagging:        auth.BucketInfo,
	OpListObjects:             auth.ListBucket,
	OpListObjectsV2:           auth.ListBucket,
	OpListMultipartUploads:    auth.ListBucket,
	OpGetObject:               auth.ReadObject,
	OpHeadObject:              auth.ReadObject,
	OpPutObject:               auth.WriteObject,
	OpCopyObject:              auth.WriteObject,
	OpCreateMultipartUpload:   auth.WriteObject,
	OpUploadPart:              auth.WriteObject,
	OpUploadPartCopy:          auth.WriteObject,
	OpCompleteMultipartUpload: auth.WriteObject,
	OpAbortMultipartUpload:    auth.WriteObject,
	OpListParts:               auth.WriteObject,
	OpDeleteObject:            auth.DeleteObject,
	OpDeleteObjects:           auth.DeleteObject,
}

// anonymousNever lists operations no bucket policy opens to anonymous users:
// copies need read access to a second resource, and bulk deletes and bucket
// listings are not covered by per-prefix rules.
var anonymousNever = map[Op]bool{
	OpListBuckets: true, OpCopyObject: true, OpUploadPartCopy: true, OpDeleteObjects: true,
	OpListMultipartUploads: true,
}

type scopeKey struct{}

// scopeFrom returns the scope of the signed request (full access if unknown).
func scopeFrom(ctx context.Context) auth.Scope {
	s, _ := ctx.Value(scopeKey{}).(auth.Scope)
	return s
}

// authorize decides whether the authenticated (or anonymous) caller may
// perform req, and returns the request context to continue with.
func (h *Handler) authorize(r *http.Request, req Request, accessKey string) (context.Context, error) {
	ctx := r.Context()
	action, ok := opActions[req.Op]
	if !ok {
		return ctx, nil // unimplemented operations fail later with NotImplemented
	}

	if accessKey == "" {
		if anonymousNever[req.Op] || h.opts.Store == nil {
			return ctx, s3err.ErrAccessDenied
		}
		b, err := h.opts.Store.GetBucket(ctx, req.Bucket)
		if err != nil {
			return ctx, s3err.ErrAccessDenied // do not reveal whether the bucket exists
		}
		policy, err := auth.ParsePolicy(b.Policy)
		if err != nil {
			return ctx, s3err.ErrAccessDenied
		}
		resource := req.Key
		if action == auth.ListBucket {
			resource = r.URL.Query().Get("prefix")
		}
		if !policy.AllowsAnonymous(action, resource) {
			return ctx, s3err.ErrAccessDenied
		}
		return ctx, nil
	}

	if h.opts.Keys == nil {
		return ctx, nil
	}
	scope, err := h.opts.Keys.Scope(ctx, accessKey)
	if err != nil {
		return ctx, s3err.ErrAccessDenied
	}
	if !scope.Allows(action, req.Bucket) {
		return ctx, s3err.ErrAccessDenied
	}
	if req.Op == OpCopyObject || req.Op == OpUploadPartCopy {
		if src, _, err := parseCopySource(r.Header.Get("X-Amz-Copy-Source")); err == nil && !scope.Allows(auth.ReadObject, src) {
			return ctx, s3err.ErrAccessDenied
		}
	}
	return context.WithValue(ctx, scopeKey{}, scope), nil
}
