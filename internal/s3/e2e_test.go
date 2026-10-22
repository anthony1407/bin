package s3

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"bin/internal/s3/sigv4"
	"bin/internal/storage"
)

// newE2E starts bin's S3 API on a real HTTP server backed by a temporary data
// directory and returns an AWS SDK client pointed at it.
func newE2E(t *testing.T) *awss3.Client {
	c, _ := newE2EWithDir(t)
	return c
}

// newE2EWithDir also returns the data directory, for tests that inspect or
// damage what is on disk.
func newE2EWithDir(t *testing.T) (*awss3.Client, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := httptest.NewServer(NewHandler(Options{
		Store:  store,
		Region: "us-east-1",
		HostID: "e2e-host",
		Owner:  Owner{ID: "owner-id", DisplayName: "bin"},
		Auth:   &sigv4.Verifier{Region: "us-east-1", Store: sigv4.StaticStore{testAK: testSecret}},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	t.Cleanup(srv.Close)
	return awss3.New(awss3.Options{
		BaseEndpoint: aws.String(srv.URL),
		Region:       "us-east-1",
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(testAK, testSecret, ""),
	}), dir
}

// apiCode returns the S3 error code carried by err, or "".
func apiCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

func TestE2EBucketLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)

	out, err := c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Buckets) != 0 || aws.ToString(out.Owner.ID) != "owner-id" {
		t.Fatalf("empty list = %+v", out)
	}

	for _, b := range []string{"photos", "backups"} {
		if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(b)}); err != nil {
			t.Fatalf("create %s: %v", b, err)
		}
	}
	_, err = c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("photos")})
	if code := apiCode(err); code != "BucketAlreadyOwnedByYou" {
		t.Errorf("duplicate create: %v", err)
	}

	out, err = c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil || len(out.Buckets) != 2 || aws.ToString(out.Buckets[0].Name) != "backups" || out.Buckets[0].CreationDate.IsZero() {
		t.Fatalf("list = %+v, %v", out, err)
	}

	head, err := c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("photos")})
	if err != nil || aws.ToString(head.BucketRegion) != "us-east-1" {
		t.Errorf("head = %+v, %v", head, err)
	}
	_, err = c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("missing")})
	if code := apiCode(err); code != "NotFound" {
		t.Errorf("head missing: code %q, %v", code, err)
	}

	loc, err := c.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("photos")})
	if err != nil || loc.LocationConstraint != "" {
		t.Errorf("location = %q, %v", loc.LocationConstraint, err)
	}
	ver, err := c.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("photos")})
	if err != nil || ver.Status != "" {
		t.Errorf("versioning = %q, %v", ver.Status, err)
	}

	if _, err := c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("photos")}); err != nil {
		t.Fatal(err)
	}
	_, err = c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("photos")})
	if code := apiCode(err); code != "NoSuchBucket" {
		t.Errorf("delete twice: %v", err)
	}
	_, err = c.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("photos")})
	if code := apiCode(err); code != "NoSuchBucket" {
		t.Errorf("location of deleted bucket: %v", err)
	}
}

func TestE2ECreateBucketErrors(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)

	_, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("Bad_Name")})
	if code := apiCode(err); code != "InvalidBucketName" {
		t.Errorf("invalid name: %v", err)
	}
	_, err = c.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String("elsewhere"),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraintEuWest1,
		},
	})
	if code := apiCode(err); code != "IllegalLocationConstraintException" {
		t.Errorf("wrong region: %v", err)
	}
	_, err = c.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket:                     aws.String("locked"),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	if code := apiCode(err); code != "NotImplemented" {
		t.Errorf("object lock: %v", err)
	}
}

func TestE2EBucketTagging(t *testing.T) {
	ctx := context.Background()
	c := newE2E(t)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("photos")})

	_, err := c.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String("photos")})
	if code := apiCode(err); code != "NoSuchTagSet" {
		t.Errorf("no tags yet: %v", err)
	}

	_, err = c.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{
		Bucket: aws.String("photos"),
		Tagging: &types.Tagging{TagSet: []types.Tag{
			{Key: aws.String("team"), Value: aws.String("home")},
			{Key: aws.String("año"), Value: aws.String("2026 / fotos")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String("photos")})
	if err != nil {
		t.Fatal(err)
	}
	var pairs []string
	for _, tag := range got.TagSet {
		pairs = append(pairs, aws.ToString(tag.Key)+"="+aws.ToString(tag.Value))
	}
	sort.Strings(pairs)
	if len(pairs) != 2 || pairs[0] != "año=2026 / fotos" || pairs[1] != "team=home" {
		t.Errorf("tags = %v", pairs)
	}

	for name, set := range map[string][]types.Tag{
		"duplicate key": {{Key: aws.String("a"), Value: aws.String("1")}, {Key: aws.String("a"), Value: aws.String("2")}},
		"system prefix": {{Key: aws.String("aws:owner"), Value: aws.String("x")}},
		"bad character": {{Key: aws.String("semi;colon"), Value: aws.String("x")}},
	} {
		_, err := c.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{
			Bucket: aws.String("photos"), Tagging: &types.Tagging{TagSet: set},
		})
		if code := apiCode(err); code != "InvalidTag" {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := c.DeleteBucketTagging(ctx, &awss3.DeleteBucketTaggingInput{Bucket: aws.String("photos")}); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String("photos")})
	if code := apiCode(err); code != "NoSuchTagSet" {
		t.Errorf("after delete: %v", err)
	}
}
