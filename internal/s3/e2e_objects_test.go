package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func mustBucket(t *testing.T, c *awss3.Client, name string) {
	t.Helper()
	if _, err := c.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatal(err)
	}
}

func getBody(t *testing.T, c *awss3.Client, in *awss3.GetObjectInput) (*awss3.GetObjectOutput, string) {
	t.Helper()
	out, err := c.GetObject(context.Background(), in)
	if err != nil {
		t.Fatalf("get %s: %v", aws.ToString(in.Key), err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read %s: %v", aws.ToString(in.Key), err)
	}
	return out, string(data)
}

func TestE2EObjectChecksums(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "sums")
	for _, alg := range []types.ChecksumAlgorithm{
		types.ChecksumAlgorithmCrc32, types.ChecksumAlgorithmCrc32c, types.ChecksumAlgorithmCrc64nvme,
		types.ChecksumAlgorithmSha1, types.ChecksumAlgorithmSha256,
	} {
		key := "obj-" + string(alg)
		data := strings.Repeat("checksum me "+string(alg)+"\n", 5000)
		put, err := c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("sums"), Key: aws.String(key),
			Body: strings.NewReader(data), ChecksumAlgorithm: alg,
		})
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if put.ETag == nil {
			t.Errorf("%s: no ETag", alg)
		}
		// With checksum mode enabled the SDK validates the returned checksum
		// against the body it receives.
		_, body := getBody(t, c, &awss3.GetObjectInput{
			Bucket: aws.String("sums"), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled,
		})
		if body != data {
			t.Errorf("%s: body mismatch", alg)
		}
	}

	_, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("sums"), Key: aws.String("bad"),
		Body: strings.NewReader("payload"), ChecksumCRC32: aws.String("AAAAAA=="),
	})
	if code := apiCode(err); code != "BadDigest" {
		t.Errorf("wrong CRC32: %v", err)
	}
	wrongMD5 := md5.Sum([]byte("something else"))
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("sums"), Key: aws.String("bad"),
		Body: strings.NewReader("payload"), ContentMD5: aws.String(base64.StdEncoding.EncodeToString(wrongMD5[:])),
	})
	if code := apiCode(err); code != "BadDigest" {
		t.Errorf("wrong Content-MD5: %v", err)
	}
	if _, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("sums"), Key: aws.String("bad")}); err == nil {
		t.Error("an upload with a bad digest was stored")
	}
}

// An unseekable body makes the SDK stream with aws-chunked and send the
// checksum as a trailer, the same framing aws-cli's CRT client uses.
func TestE2EStreamingTrailer(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "stream")
	data := bytes.Repeat([]byte("streamed "), 300000) // ~2.7 MB
	_, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("stream"), Key: aws.String("big.bin"),
		Body:          io.MultiReader(bytes.NewReader(data)), // hides Seek
		ContentLength: aws.Int64(int64(len(data))),
	}, awss3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	if err != nil {
		t.Fatal(err)
	}
	_, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("stream"), Key: aws.String("big.bin")})
	if body != string(data) {
		t.Errorf("streamed upload: got %d bytes", len(body))
	}
}

func TestE2EObjectMetadataAndKeys(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "meta")
	keys := []string{"plain.txt", "folder with spaces/ñandú +1.txt", "report(final)!=*'.txt", "a//b/../c", "emoji/😀.png"}
	for _, key := range keys {
		_, err := c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("meta"), Key: aws.String(key), Body: strings.NewReader("body of " + key),
			ContentType:        aws.String("text/plain; charset=utf-8"),
			CacheControl:       aws.String("max-age=60"),
			ContentDisposition: aws.String(`attachment; filename="x.txt"`),
			ContentLanguage:    aws.String("es"),
			Metadata:           map[string]string{"md5chksum": "DHDwCX81EAWKYyE9ga0Dbg==", "mtime": "1792678638.7254636"},
		})
		if err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
		out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("meta"), Key: aws.String(key)})
		if body != "body of "+key {
			t.Errorf("%q: body %q", key, body)
		}
		if aws.ToString(out.ContentType) != "text/plain; charset=utf-8" || aws.ToString(out.CacheControl) != "max-age=60" ||
			aws.ToString(out.ContentDisposition) != `attachment; filename="x.txt"` || aws.ToString(out.ContentLanguage) != "es" ||
			out.Metadata["md5chksum"] != "DHDwCX81EAWKYyE9ga0Dbg==" || out.Metadata["mtime"] != "1792678638.7254636" {
			t.Errorf("%q: headers %+v", key, out)
		}
	}

	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("meta"), Key: aws.String("plain.txt")})
	if err != nil || aws.ToInt64(head.ContentLength) != int64(len("body of plain.txt")) || head.LastModified == nil {
		t.Errorf("head = %+v, %v", head, err)
	}
	_, err = c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("meta"), Key: aws.String("missing")})
	if code := apiCode(err); code != "NotFound" {
		t.Errorf("head missing: %v", err)
	}

	// An empty object. (The Go SDK always sends a Content-Type, so the
	// binary/octet-stream default is checked with aws-cli instead.)
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("meta"), Key: aws.String("empty"), Body: strings.NewReader("")})
	out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("meta"), Key: aws.String("empty")})
	if body != "" || aws.ToString(out.ContentType) != "application/octet-stream" ||
		aws.ToString(out.ETag) != `"d41d8cd98f00b204e9800998ecf8427e"` {
		t.Errorf("empty object: body %q, content-type %q, etag %s", body, aws.ToString(out.ContentType), aws.ToString(out.ETag))
	}

	big := map[string]string{"big": strings.Repeat("x", 2100)}
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("meta"), Key: aws.String("k"), Body: strings.NewReader("x"), Metadata: big})
	if code := apiCode(err); code != "MetadataTooLarge" {
		t.Errorf("oversized metadata: %v", err)
	}
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("meta"), Key: aws.String(strings.Repeat("k", 1025)), Body: strings.NewReader("x")})
	if code := apiCode(err); code != "KeyTooLongError" {
		t.Errorf("long key: %v", err)
	}
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("missing"), Key: aws.String("k"), Body: strings.NewReader("x")})
	if code := apiCode(err); code != "NoSuchBucket" {
		t.Errorf("missing bucket: %v", err)
	}
}

func TestE2ERangesAndConditions(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "ranges")
	const data = "0123456789"
	put, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), Body: strings.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	for rng, want := range map[string]string{
		"bytes=2-5": "2345", "bytes=7-": "789", "bytes=-3": "789", "bytes=8-100": "89", "bytes=0-0": "0",
		"bytes=1-2,4-5": data, "items=0-1": data, // ignored: multiple ranges, unknown unit
	} {
		out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), Range: aws.String(rng)})
		if body != want {
			t.Errorf("%s: got %q want %q (Content-Range %s)", rng, body, want, aws.ToString(out.ContentRange))
		}
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), Range: aws.String("bytes=10-")})
	if code := apiCode(err); code != "InvalidRange" {
		t.Errorf("unsatisfiable range: %v", err)
	}

	etag := aws.ToString(put.ETag)
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), IfMatch: aws.String(`"nomatch"`)})
	if code := apiCode(err); code != "PreconditionFailed" {
		t.Errorf("If-Match mismatch: %v", err)
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), IfNoneMatch: aws.String(etag)})
	if code := apiCode(err); code != "NotModified" {
		t.Errorf("If-None-Match match: %v", err)
	}
	future := time.Now().Add(time.Hour)
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), IfModifiedSince: &future})
	if code := apiCode(err); code != "NotModified" {
		t.Errorf("If-Modified-Since future: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), IfUnmodifiedSince: &past})
	if code := apiCode(err); code != "PreconditionFailed" {
		t.Errorf("If-Unmodified-Since past: %v", err)
	}
	if _, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), IfMatch: aws.String(etag), PartNumber: aws.Int32(1)}); body != data {
		t.Errorf("If-Match + partNumber=1: %q", body)
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), PartNumber: aws.Int32(2)})
	if code := apiCode(err); code != "InvalidPartNumber" {
		t.Errorf("partNumber=2 of a single-part object: %v", err)
	}

	// Conditional writes.
	_, err = c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("ranges"), Key: aws.String("k"), Body: strings.NewReader("x"), IfNoneMatch: aws.String("*")})
	if code := apiCode(err); code != "PreconditionFailed" {
		t.Errorf("If-None-Match * on existing key: %v", err)
	}
}

func TestE2EDeleteAndCopy(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "src")
	mustBucket(t, c, "dst")
	put, _ := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("src"), Key: aws.String("folder/a b.txt"), Body: strings.NewReader("copy me"),
		ContentType: aws.String("text/plain"), Metadata: map[string]string{"author": "bin"},
	})

	cp, err := c.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("dst"), Key: aws.String("copy.txt"), CopySource: aws.String("src/folder/a%20b.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(cp.CopyObjectResult.ETag) != aws.ToString(put.ETag) || cp.CopyObjectResult.LastModified == nil {
		t.Errorf("copy result %+v", cp.CopyObjectResult)
	}
	out, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("dst"), Key: aws.String("copy.txt")})
	if body != "copy me" || out.Metadata["author"] != "bin" || aws.ToString(out.ContentType) != "text/plain" {
		t.Errorf("copied object %q %+v", body, out)
	}

	_, err = c.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("dst"), Key: aws.String("copy.txt"), CopySource: aws.String("dst/copy.txt"),
		MetadataDirective: types.MetadataDirectiveReplace, ContentType: aws.String("application/x-bin"),
		Metadata: map[string]string{"fresh": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ = getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("dst"), Key: aws.String("copy.txt")})
	if out.Metadata["fresh"] != "yes" || out.Metadata["author"] != "" || aws.ToString(out.ContentType) != "application/x-bin" {
		t.Errorf("REPLACE metadata: %+v", out)
	}

	for name, in := range map[string]*awss3.CopyObjectInput{
		"self copy without REPLACE": {Bucket: aws.String("dst"), Key: aws.String("copy.txt"), CopySource: aws.String("dst/copy.txt")},
		"missing source":            {Bucket: aws.String("dst"), Key: aws.String("x"), CopySource: aws.String("src/missing")},
		"source If-Match":           {Bucket: aws.String("dst"), Key: aws.String("x"), CopySource: aws.String("src/folder/a%20b.txt"), CopySourceIfMatch: aws.String(`"nomatch"`)},
	} {
		_, err := c.CopyObject(ctx, in)
		want := map[string]string{"self copy without REPLACE": "InvalidRequest", "missing source": "NoSuchKey", "source If-Match": "PreconditionFailed"}[name]
		if code := apiCode(err); code != want {
			t.Errorf("%s: %v (want %s)", name, err, want)
		}
	}

	_, err = c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("src")})
	if code := apiCode(err); code != "BucketNotEmpty" {
		t.Errorf("delete non-empty bucket: %v", err)
	}
	if _, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("src"), Key: aws.String("folder/a b.txt")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("src"), Key: aws.String("folder/a b.txt")}); err != nil {
		t.Errorf("deleting twice must succeed: %v", err)
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("src"), Key: aws.String("folder/a b.txt")})
	if code := apiCode(err); code != "NoSuchKey" {
		t.Errorf("get deleted: %v", err)
	}
	if _, err := c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("src")}); err != nil {
		t.Errorf("delete emptied bucket: %v", err)
	}
}

func TestE2EPresignedDownload(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "share")
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("share"), Key: aws.String("photo.jpg"), Body: strings.NewReader("jpeg bytes")})

	ps, err := awss3.NewPresignClient(c).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("share"), Key: aws.String("photo.jpg"),
		ResponseContentDisposition: aws.String(`attachment; filename="photo.jpg"`),
	}, awss3.WithPresignExpires(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ps.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "jpeg bytes" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="photo.jpg"` {
		t.Errorf("presigned GET: %d %q %v", resp.StatusCode, body, resp.Header)
	}
}

// Bytes damaged on disk must never reach a client as a complete, successful
// download: the connection is cut before the end of the body.
func TestE2ECorruptionAbortsDownload(t *testing.T) {
	ctx := context.Background()
	c, dir := newE2EWithDir(t)
	mustBucket(t, c, "rot")
	data := strings.Repeat("precious data ", 50000) // 700 KB
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("rot"), Key: aws.String("k"), Body: strings.NewReader(data)})

	var blob string
	filepath.WalkDir(filepath.Join(dir, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			blob = p
		}
		return nil
	})
	raw, _ := os.ReadFile(blob)
	raw[12345] ^= 0x01
	os.WriteFile(blob, raw, 0o640)

	out, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("rot"), Key: aws.String("k")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(out.Body)
	out.Body.Close()
	if err == nil {
		t.Fatalf("download of corrupt data succeeded (%d bytes)", len(got))
	}
	if len(got) >= len(data) {
		t.Errorf("client received all %d bytes before the abort", len(got))
	}

	// A range read does not cover the damaged byte and still works.
	_, part := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("rot"), Key: aws.String("k"), Range: aws.String("bytes=0-99")})
	if part != data[:100] {
		t.Errorf("range read: %q", part)
	}
}
