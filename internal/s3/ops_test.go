package s3

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"bin/internal/s3/s3err"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		method, target string
		copySource     bool
		want           Request
	}{
		{"GET", "/", false, Request{Op: OpListBuckets}},
		{"PUT", "/b", false, Request{OpCreateBucket, "b", ""}},
		{"PUT", "/b/", false, Request{OpCreateBucket, "b", ""}},
		{"DELETE", "/b", false, Request{OpDeleteBucket, "b", ""}},
		{"HEAD", "/b", false, Request{OpHeadBucket, "b", ""}},
		{"GET", "/b?location", false, Request{OpGetBucketLocation, "b", ""}},
		{"GET", "/b?versioning=", false, Request{OpGetBucketVersioning, "b", ""}},
		{"GET", "/b?list-type=2&prefix=dir%2F&delimiter=%2F&encoding-type=url", false, Request{OpListObjectsV2, "b", ""}},
		{"GET", "/b?delimiter=%2F&encoding-type=url", false, Request{OpListObjects, "b", ""}},
		{"GET", "/b?uploads", false, Request{OpListMultipartUploads, "b", ""}},
		{"POST", "/b?delete", false, Request{OpDeleteObjects, "b", ""}},
		{"PUT", "/b/k.txt", false, Request{OpPutObject, "b", "k.txt"}},
		{"PUT", "/b/k.txt?x-id=PutObject", false, Request{OpPutObject, "b", "k.txt"}},
		{"PUT", "/b/k.txt", true, Request{OpCopyObject, "b", "k.txt"}},
		{"GET", "/b/dir/sub/k.txt", false, Request{OpGetObject, "b", "dir/sub/k.txt"}},
		{"HEAD", "/b/k.txt?partNumber=1", false, Request{OpHeadObject, "b", "k.txt"}},
		{"DELETE", "/b/k.txt", false, Request{OpDeleteObject, "b", "k.txt"}},
		{"POST", "/b/k?uploads", false, Request{OpCreateMultipartUpload, "b", "k"}},
		{"PUT", "/b/k?partNumber=2&uploadId=abc", false, Request{OpUploadPart, "b", "k"}},
		{"PUT", "/b/k?partNumber=2&uploadId=abc", true, Request{OpUploadPartCopy, "b", "k"}},
		{"GET", "/b/k?uploadId=abc", false, Request{OpListParts, "b", "k"}},
		{"POST", "/b/k?uploadId=abc", false, Request{OpCompleteMultipartUpload, "b", "k"}},
		{"DELETE", "/b/k?uploadId=abc", false, Request{OpAbortMultipartUpload, "b", "k"}},
		// Percent-encoded keys are decoded exactly once; "+" stays a literal plus.
		{"GET", "/b/folder%20with%20spaces/%C3%B1and%C3%BA%20%2B1.txt", false,
			Request{OpGetObject, "b", "folder with spaces/ñandú +1.txt"}},
		{"GET", "/b/a+b", false, Request{OpGetObject, "b", "a+b"}},
		// Keys are opaque: no path cleaning.
		{"GET", "/b/a//b/../c", false, Request{OpGetObject, "b", "a//b/../c"}},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.copySource {
				r.Header.Set("X-Amz-Copy-Source", "src/key")
			}
			got, err := Resolve(r, "")
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		method, target string
		want           error
	}{
		{"PATCH", "/b/k", s3err.ErrMethodNotAllowed},
		{"POST", "/b", s3err.ErrMethodNotAllowed},
		{"DELETE", "/", s3err.ErrMethodNotAllowed},
		{"PUT", "/b?policy", s3err.ErrNotImplemented},
		{"PUT", "/b?versioning", s3err.ErrNotImplemented},
		{"GET", "/b?versions", s3err.ErrNotImplemented},
		{"PUT", "/b/k?tagging", s3err.ErrNotImplemented},
		{"GET", "/b/k?acl", s3err.ErrNotImplemented},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			_, err := Resolve(httptest.NewRequest(tc.method, tc.target, nil), "")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveVirtualHost(t *testing.T) {
	cases := []struct {
		host, target string
		want         Request
	}{
		{"photos.s3.example.com", "/2024/a.jpg", Request{OpGetObject, "photos", "2024/a.jpg"}},
		{"photos.s3.example.com:9000", "/", Request{OpListObjects, "photos", ""}},
		{"PHOTOS.S3.Example.com", "/a", Request{OpGetObject, "photos", "a"}},
		// The bare domain and unrelated hosts fall back to path style.
		{"s3.example.com", "/photos/a", Request{OpGetObject, "photos", "a"}},
		{"localhost:9000", "/photos/a", Request{OpGetObject, "photos", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.host+tc.target, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			r.Host = tc.host
			got, err := Resolve(r, "s3.example.com")
			if err != nil || got != tc.want {
				t.Errorf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}
