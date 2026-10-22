package s3

import (
	"bytes"
	"context"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const mib = 1 << 20

func randomData(seed uint64, n int) []byte {
	rng := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

// The SDK's transfer manager uploads parts concurrently; with checksum mode
// enabled the SDK validates the full-object CRC64NVME bin combined from parts.
func TestE2ETransferManager(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "mgr")
	data := randomData(1, 23*mib+12345)

	up := manager.NewUploader(c, func(u *manager.Uploader) { u.PartSize = 5 * mib; u.Concurrency = 4 })
	out, err := up.Upload(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("mgr"), Key: aws.String("big.bin"), Body: bytes.NewReader(data),
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc64nvme,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(aws.ToString(out.ETag), `-5"`) {
		t.Errorf("multipart ETag %s", aws.ToString(out.ETag))
	}

	get, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("mgr"), Key: aws.String("big.bin"), ChecksumMode: types.ChecksumModeEnabled})
	if body != string(data) {
		t.Fatalf("download mismatch (%d bytes)", len(body))
	}
	if get.ChecksumType != types.ChecksumTypeFullObject || aws.ToString(get.ChecksumCRC64NVME) == "" {
		t.Errorf("checksum %s %s", get.ChecksumType, aws.ToString(get.ChecksumCRC64NVME))
	}

	// Concurrent ranged download, as aws s3 cp does for large files.
	buf := manager.NewWriteAtBuffer(nil)
	dl := manager.NewDownloader(c, func(d *manager.Downloader) { d.PartSize = 3 * mib; d.Concurrency = 5 })
	if _, err := dl.Download(ctx, buf, &awss3.GetObjectInput{Bucket: aws.String("mgr"), Key: aws.String("big.bin")}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("ranged download mismatch")
	}

	part, pbody := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("mgr"), Key: aws.String("big.bin"), PartNumber: aws.Int32(2)})
	if pbody != string(data[5*mib:10*mib]) || aws.ToInt32(part.PartsCount) != 5 {
		t.Errorf("part 2: %d bytes, parts count %d", len(pbody), aws.ToInt32(part.PartsCount))
	}
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("mgr"), Key: aws.String("big.bin"), PartNumber: aws.Int32(6)})
	if code := apiCode(err); code != "InvalidPartNumber" {
		t.Errorf("part 6 of 5: %v", err)
	}
	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("mgr"), Key: aws.String("big.bin"), PartNumber: aws.Int32(1)})
	if err != nil || aws.ToInt64(head.ContentLength) != 5*mib || aws.ToInt32(head.PartsCount) != 5 {
		t.Errorf("head part 1: %+v, %v", head, err)
	}
}

func TestE2EMultipartLowLevel(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "low")
	p1, p2 := randomData(2, 5*mib), randomData(3, 100)

	create, err := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("low"), Key: aws.String("k"), ContentType: aws.String("video/mp4"),
		Metadata: map[string]string{"md5chksum": "xyz"}, ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := create.UploadId
	if create.ChecksumType != types.ChecksumTypeComposite {
		t.Errorf("CRC32 default type %q", create.ChecksumType)
	}
	up := func(n int32, data []byte) *awss3.UploadPartOutput {
		out, err := c.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String("low"), Key: aws.String("k"), UploadId: id, PartNumber: aws.Int32(n),
			Body: bytes.NewReader(data), ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
		})
		if err != nil {
			t.Fatalf("part %d: %v", n, err)
		}
		return out
	}
	o1, o2 := up(1, p1), up(2, p2)
	o3 := up(3, []byte("discarded")) // uploaded but not listed in the final completion

	lp, err := c.ListParts(ctx, &awss3.ListPartsInput{Bucket: aws.String("low"), Key: aws.String("k"), UploadId: id, MaxParts: aws.Int32(2)})
	if err != nil || len(lp.Parts) != 2 || !aws.ToBool(lp.IsTruncated) || aws.ToString(lp.Parts[0].ChecksumCRC32) == "" {
		t.Fatalf("list parts: %+v, %v", lp, err)
	}
	lu, err := c.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{Bucket: aws.String("low")})
	if err != nil || len(lu.Uploads) != 1 || aws.ToString(lu.Uploads[0].UploadId) != aws.ToString(id) {
		t.Fatalf("list uploads: %+v, %v", lu, err)
	}

	completed := func(parts ...types.CompletedPart) error {
		_, err := c.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket: aws.String("low"), Key: aws.String("k"), UploadId: id,
			MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
		})
		return err
	}
	part := func(n int32, out *awss3.UploadPartOutput) types.CompletedPart {
		return types.CompletedPart{PartNumber: aws.Int32(n), ETag: out.ETag, ChecksumCRC32: out.ChecksumCRC32}
	}
	for name, tc := range map[string]struct {
		parts []types.CompletedPart
		code  string
	}{
		"wrong etag": {[]types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String(`"00"`)}}, "InvalidPart"},
		"order":      {[]types.CompletedPart{part(2, o2), part(1, o1)}, "InvalidPartOrder"},
		"too small":  {[]types.CompletedPart{part(2, o2), part(3, o3)}, "EntityTooSmall"},
	} {
		if code := apiCode(completed(tc.parts...)); code != tc.code {
			t.Errorf("%s: code %q, want %s", name, code, tc.code)
		}
	}
	if err := completed(part(1, o1), part(2, o2)); err != nil {
		t.Fatal(err)
	}

	got, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("low"), Key: aws.String("k"), ChecksumMode: types.ChecksumModeEnabled})
	if body != string(p1)+string(p2) || aws.ToString(got.ContentType) != "video/mp4" || got.Metadata["md5chksum"] != "xyz" {
		t.Errorf("completed object: %d bytes %+v", len(body), got)
	}
	if !strings.HasSuffix(aws.ToString(got.ChecksumCRC32), "-2") || got.ChecksumType != types.ChecksumTypeComposite {
		t.Errorf("composite checksum %q %s", aws.ToString(got.ChecksumCRC32), got.ChecksumType)
	}
	_, err = c.UploadPart(ctx, &awss3.UploadPartInput{Bucket: aws.String("low"), Key: aws.String("k"), UploadId: id,
		PartNumber: aws.Int32(4), Body: strings.NewReader("late")})
	if code := apiCode(err); code != "NoSuchUpload" {
		t.Errorf("part after completion: %v", err)
	}
}

func TestE2EMultipartAbortAndCopy(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "cpy")
	src := randomData(4, 6*mib)
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("cpy"), Key: aws.String("src"), Body: bytes.NewReader(src)})

	create, _ := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("cpy"), Key: aws.String("dst")})
	whole, err := c.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("cpy"), Key: aws.String("dst"), UploadId: create.UploadId, PartNumber: aws.Int32(1),
		CopySource: aws.String("cpy/src"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ranged, err := c.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("cpy"), Key: aws.String("dst"), UploadId: create.UploadId, PartNumber: aws.Int32(2),
		CopySource: aws.String("cpy/src"), CopySourceRange: aws.String("bytes=100-199"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String("cpy"), Key: aws.String("dst"), UploadId: create.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{
			{PartNumber: aws.Int32(1), ETag: whole.CopyPartResult.ETag},
			{PartNumber: aws.Int32(2), ETag: ranged.CopyPartResult.ETag},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body := getBody(t, c, &awss3.GetObjectInput{Bucket: aws.String("cpy"), Key: aws.String("dst")})
	if body != string(src)+string(src[100:200]) {
		t.Error("copied parts mismatch")
	}

	ab, _ := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("cpy"), Key: aws.String("gone")})
	if _, err := c.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String("cpy"), Key: aws.String("gone"), UploadId: ab.UploadId}); err != nil {
		t.Fatal(err)
	}
	_, err = c.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String("cpy"), Key: aws.String("gone"), UploadId: ab.UploadId})
	if code := apiCode(err); code != "NoSuchUpload" {
		t.Errorf("abort twice: %v", err)
	}
}

func TestE2EDeleteObjects(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	mustBucket(t, c, "bulk")
	for _, k := range []string{"a", "b", "dir/c"} {
		c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("bulk"), Key: aws.String(k), Body: strings.NewReader("x")})
	}
	out, err := c.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String("bulk"),
		Delete: &types.Delete{Objects: []types.ObjectIdentifier{
			{Key: aws.String("a")}, {Key: aws.String("dir/c")}, {Key: aws.String("never-existed")},
			{Key: aws.String("b"), VersionId: aws.String("v123")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Deleted) != 3 || len(out.Errors) != 1 || aws.ToString(out.Errors[0].Key) != "b" {
		t.Errorf("deleted %d, errors %+v", len(out.Deleted), out.Errors)
	}
	list, _ := c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("bulk")})
	if len(list.Contents) != 1 || aws.ToString(list.Contents[0].Key) != "b" {
		t.Errorf("remaining: %+v", list.Contents)
	}
	quiet, err := c.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String("bulk"),
		Delete: &types.Delete{Quiet: aws.Bool(true), Objects: []types.ObjectIdentifier{{Key: aws.String("b")}}},
	})
	if err != nil || len(quiet.Deleted) != 0 || len(quiet.Errors) != 0 {
		t.Errorf("quiet: %+v, %v", quiet, err)
	}
}
