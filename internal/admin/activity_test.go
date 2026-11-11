package admin

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestUserActivity(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "shared"}, nil)
	key, u := newUser(t, e, map[string]any{"name": "ana", "buckets": []string{"shared"}, "canCreateBuckets": true})

	// Uploads from the console and through S3, and a bucket of her own.
	u.call("PUT", "/api/v1/buckets/shared/upload?key=console.txt", "12345", nil) // 5 bytes
	u.call("POST", "/api/v1/buckets", map[string]string{"name": "anas-own"}, nil)
	s3c := s3Client(e.s3URL, key.AccessKey, key.SecretKey)
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("anas-own"), Key: aws.String("s3.txt"),
		Body: strings.NewReader("0123456789")}); err != nil { // 10 bytes
		t.Fatal(err)
	}

	// Downloads are not recorded until the setting says so; previews never are.
	get := func() {
		out, err := s3c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("anas-own"), Key: aws.String("s3.txt")})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, out.Body)
		out.Body.Close()
	}
	get()
	if code := e.call("PUT", "/api/v1/settings", map[string]any{"auditDownloads": true}, nil); code != 200 {
		t.Fatalf("turn on download audit: %d", code)
	}
	get()                                                                               // S3: 10 bytes
	u.call("GET", "/api/v1/buckets/shared/download?key=console.txt", nil, nil)          // console: 5 bytes
	u.call("GET", "/api/v1/buckets/shared/download?key=console.txt&inline=1", nil, nil) // a preview: not recorded

	var act activityJSON
	if code := e.call("GET", "/api/v1/access-keys/"+key.AccessKey+"/activity", nil, &act); code != 200 {
		t.Fatalf("activity: %d", code)
	}
	if len(act.Buckets) != 1 || act.Buckets[0].Name != "anas-own" || act.Buckets[0].Objects != 1 || !act.Buckets[0].Owned {
		t.Errorf("buckets: %+v", act.Buckets)
	}
	if act.Uploads != 2 || act.UploadBytes != 15 {
		t.Errorf("uploads: %d (%d bytes), want 2 (15)", act.Uploads, act.UploadBytes)
	}
	if act.Downloads != 2 || act.DownloadBytes != 15 || !act.DownloadsRecorded {
		t.Errorf("downloads: %d (%d bytes) recorded=%v, want 2 (15)", act.Downloads, act.DownloadBytes, act.DownloadsRecorded)
	}
	if act.LastActivity == nil || len(act.Recent) == 0 {
		t.Fatalf("recent: %+v", act.Recent)
	}
	for _, r := range act.Recent {
		if r.Actor != key.AccessKey {
			t.Errorf("someone else's action in her activity: %+v", r)
		}
	}
	// The audit listing filters by actor too.
	var page struct{ Entries []auditJSON }
	e.call("GET", "/api/v1/audit?actor="+key.AccessKey, nil, &page)
	if len(page.Entries) != len(act.Recent) {
		t.Errorf("audit ?actor: %d entries, activity shows %d", len(page.Entries), len(act.Recent))
	}
	// Admin only.
	if code := u.call("GET", "/api/v1/access-keys/"+key.AccessKey+"/activity", nil, nil); code != 403 {
		t.Errorf("a user reading activity: %d", code)
	}
}
