package s3

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"bin/internal/auth"
)

func enableVersioning(t *testing.T, c *awss3.Client, bucket string, status types.BucketVersioningStatus) {
	t.Helper()
	if _, err := c.PutBucketVersioning(context.Background(), &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: status},
	}); err != nil {
		t.Fatal(err)
	}
}

func putBody(t *testing.T, c *awss3.Client, bucket, key, body string) *awss3.PutObjectOutput {
	t.Helper()
	out, err := c.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// httpStatus returns the HTTP status of a failed SDK call, or 0.
func httpStatus(err error) int {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

func TestE2EVersioning(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "docs")

	// Before versioning: no version header, deletes are final.
	if out := putBody(t, c, "docs", "plain", "x"); out.VersionId != nil {
		t.Errorf("unversioned put returned version %q", aws.ToString(out.VersionId))
	}

	enableVersioning(t, c, "docs", types.BucketVersioningStatusEnabled)
	ver, err := c.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("docs")})
	if err != nil || ver.Status != types.BucketVersioningStatusEnabled {
		t.Fatalf("versioning = %q, %v", ver.Status, err)
	}

	v1 := aws.ToString(putBody(t, c, "docs", "report.txt", "draft").VersionId)
	v2 := aws.ToString(putBody(t, c, "docs", "report.txt", "final").VersionId)
	if v1 == "" || v2 == "" || v1 == v2 {
		t.Fatalf("version ids %q %q", v1, v2)
	}
	if out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt")}); body != "final" || aws.ToString(out.VersionId) != v2 {
		t.Errorf("latest = %q (%s)", body, aws.ToString(out.VersionId))
	}
	if out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String(v1)}); body != "draft" || aws.ToString(out.VersionId) != v1 {
		t.Errorf("v1 = %q (%s)", body, aws.ToString(out.VersionId))
	}
	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String(v1)})
	if err != nil || aws.ToInt64(head.ContentLength) != 5 || aws.ToString(head.VersionId) != v1 {
		t.Errorf("head v1 = %+v, %v", head, err)
	}

	// Delete: a marker hides the object.
	del, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt")})
	if err != nil || !aws.ToBool(del.DeleteMarker) || aws.ToString(del.VersionId) == "" {
		t.Fatalf("delete = %+v, %v", del, err)
	}
	marker := aws.ToString(del.VersionId)
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt")})
	if apiCode(err) != "NoSuchKey" {
		t.Errorf("get deleted: %v", err)
	}
	_, err = c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String(marker)})
	if httpStatus(err) != http.StatusMethodNotAllowed {
		t.Errorf("head marker version: %v", err)
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String("00000000-0000-4000-8000-000000000000")})
	if apiCode(err) != "NoSuchVersion" {
		t.Errorf("get missing version: %v", err)
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String("not-a-version")})
	if apiCode(err) != "InvalidArgument" {
		t.Errorf("get invalid version: %v", err)
	}

	// Listing: the marker and both versions, newest first, and the null
	// version of the object written before versioning.
	list, err := c.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("docs")})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.DeleteMarkers) != 1 || aws.ToString(list.DeleteMarkers[0].VersionId) != marker || !aws.ToBool(list.DeleteMarkers[0].IsLatest) {
		t.Errorf("markers = %+v", list.DeleteMarkers)
	}
	var got []string
	for _, v := range list.Versions {
		got = append(got, aws.ToString(v.Key)+"@"+aws.ToString(v.VersionId))
	}
	if want := []string{"plain@null", "report.txt@" + v2, "report.txt@" + v1}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("versions = %v, want %v", got, want)
	}

	// Paging one entry at a time visits all five entries exactly once.
	var seen []string
	in := &awss3.ListObjectVersionsInput{Bucket: aws.String("docs"), MaxKeys: aws.Int32(1)}
	for i := 0; i < 10; i++ {
		page, err := c.ListObjectVersions(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.Versions {
			seen = append(seen, aws.ToString(v.VersionId))
		}
		for _, m := range page.DeleteMarkers {
			seen = append(seen, aws.ToString(m.VersionId))
		}
		if !aws.ToBool(page.IsTruncated) {
			break
		}
		in.KeyMarker, in.VersionIdMarker = page.NextKeyMarker, page.NextVersionIdMarker
	}
	if want := []string{"null", marker, v2, v1}; strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Errorf("paged = %v, want %v", seen, want)
	}

	// Restore v1 by copying it over the key.
	cp, err := c.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"),
		CopySource: aws.String("docs/report.txt?versionId=" + v1)})
	if err != nil || aws.ToString(cp.CopySourceVersionId) != v1 || aws.ToString(cp.VersionId) == "" {
		t.Fatalf("restore copy = %+v, %v", cp, err)
	}
	if _, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt")}); body != "draft" {
		t.Errorf("after restore = %q", body)
	}
	_, err = c.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("docs"), Key: aws.String("x"),
		CopySource: aws.String("docs/report.txt?versionId=" + marker)})
	if apiCode(err) != "InvalidRequest" {
		t.Errorf("copy from marker: %v", err)
	}

	// Removing a version for good.
	if out, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String(v2)}); err != nil || aws.ToString(out.VersionId) != v2 {
		t.Errorf("delete v2 = %+v, %v", out, err)
	}
	if out, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("docs"), Key: aws.String("report.txt"), VersionId: aws.String(marker)}); err != nil || !aws.ToBool(out.DeleteMarker) {
		t.Errorf("delete marker = %+v, %v", out, err)
	}

	// Bulk delete with and without versions.
	res, err := c.DeleteObjects(ctx, &awss3.DeleteObjectsInput{Bucket: aws.String("docs"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{
		{Key: aws.String("plain")},
		{Key: aws.String("report.txt"), VersionId: aws.String(v1)},
	}}})
	if err != nil || len(res.Errors) != 0 || len(res.Deleted) != 2 {
		t.Fatalf("delete objects = %+v, %v", res, err)
	}
	for _, d := range res.Deleted {
		switch aws.ToString(d.Key) {
		case "plain":
			if !aws.ToBool(d.DeleteMarker) || aws.ToString(d.DeleteMarkerVersionId) == "" {
				t.Errorf("plain: %+v", d)
			}
		case "report.txt":
			if aws.ToString(d.VersionId) != v1 || aws.ToBool(d.DeleteMarker) {
				t.Errorf("report.txt: %+v", d)
			}
		}
	}

	// Suspended: writes and deletes use the null version.
	enableVersioning(t, c, "docs", types.BucketVersioningStatusSuspended)
	putBody(t, c, "docs", "s", "one")
	putBody(t, c, "docs", "s", "two")
	del, err = c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("docs"), Key: aws.String("s")})
	if err != nil || !aws.ToBool(del.DeleteMarker) || aws.ToString(del.VersionId) != "null" {
		t.Errorf("suspended delete = %+v, %v", del, err)
	}
	list, _ = c.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("docs"), Prefix: aws.String("s")})
	if len(list.Versions) != 0 || len(list.DeleteMarkers) != 1 {
		t.Errorf("suspended history = %+v %+v", list.Versions, list.DeleteMarkers)
	}

	// Invalid configurations.
	_, err = c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{Bucket: aws.String("docs"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: "Disabled"}})
	if apiCode(err) != "MalformedXML" {
		t.Errorf("status Disabled: %v", err)
	}
	_, err = c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{Bucket: aws.String("docs"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled, MFADelete: types.MFADeleteEnabled}})
	if apiCode(err) != "NotImplemented" {
		t.Errorf("MFA delete: %v", err)
	}
	_, err = c.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("docs"), VersionIdMarker: aws.String("null")})
	if apiCode(err) != "InvalidArgument" {
		t.Errorf("version marker without key marker: %v", err)
	}

	// Versions keep the bucket from being deleted.
	_, err = c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("docs")})
	if apiCode(err) != "BucketNotEmpty" {
		t.Errorf("delete bucket with versions: %v", err)
	}
}

func TestE2EVersionsAccess(t *testing.T) {
	ctx := context.Background()
	e := newAuthE2E(t)
	mustBucket(t, e.root, "site")
	enableVersioning(t, e.root, "site", types.BucketVersioningStatusEnabled)
	old := aws.ToString(putBody(t, e.root, "site", "index.html", "old").VersionId)
	putBody(t, e.root, "site", "index.html", "new")
	e.store.SetBucketPolicy(ctx, "site", auth.Policy{Mode: "public"}.String())

	// A public bucket serves its current objects, never older versions.
	if code, body := anon(t, "GET", e.url+"/site/index.html", ""); code != 200 || body != "new" {
		t.Errorf("public read: %d %q", code, body)
	}
	if code, _ := anon(t, "GET", e.url+"/site/index.html?versionId="+old, ""); code != 403 {
		t.Errorf("anonymous read of an old version: %d", code)
	}
	if code, _ := anon(t, "GET", e.url+"/site?versions", ""); code != 403 {
		t.Errorf("anonymous version listing: %d", code)
	}

	// A read-only key may read old versions but not delete them or change versioning.
	ro := e.newKey(t, auth.Scope{Buckets: []string{"site"}, ReadOnly: true})
	if _, body := getBody(t, ro, &awss3.GetObjectInput{Bucket: aws.String("site"), Key: aws.String("index.html"), VersionId: aws.String(old)}); body != "old" {
		t.Errorf("read-only key, old version = %q", body)
	}
	if _, err := ro.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("site")}); err != nil {
		t.Errorf("read-only key, list versions: %v", err)
	}
	_, err := ro.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("site"), Key: aws.String("index.html"), VersionId: aws.String(old)})
	if apiCode(err) != "AccessDenied" {
		t.Errorf("read-only key deleted a version: %v", err)
	}
	_, err = ro.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{Bucket: aws.String("site"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusSuspended}})
	if apiCode(err) != "AccessDenied" {
		t.Errorf("read-only key changed versioning: %v", err)
	}
}
