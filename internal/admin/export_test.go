package admin

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"bin/internal/auth"
)

// waitExport waits for every job of a saved export's latest run to end.
func (e *env) waitExport(target string, want int) []importJob {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var list struct{ Jobs []importJob }
		e.call("GET", "/api/v1/export/jobs", nil, &list)
		var mine []importJob
		done := 0
		for _, j := range list.Jobs {
			if j.Source != target {
				continue
			}
			mine = append(mine, j)
			if j.State != "queued" && j.State != "running" {
				done++
			}
		}
		if len(mine) >= want && done == len(mine) {
			return mine
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("export did not finish")
	return nil
}

func TestExportToRemote(t *testing.T) {
	remote, rc := newRemote(t)
	ctx := context.Background()
	rc.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("docs")}) // already there: updated

	e := newEnv(t)
	e.login()
	for _, b := range []string{"photos", "docs"} {
		e.call("POST", "/api/v1/buckets", map[string]string{"name": b}, nil)
	}
	e.call("PUT", "/api/v1/buckets/photos/versioning", map[string]string{"status": "Enabled"}, nil)
	e.call("PUT", "/api/v1/buckets/photos/tags", map[string]any{"tags": map[string]string{"team": "media"}}, nil)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=a.txt", "alpha", nil)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=dir%2Fb.txt", "beta", nil)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=gone.txt", "deleted", nil)
	e.call("POST", "/api/v1/buckets/photos/trash", map[string]any{"keys": []string{"gone.txt"}}, nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=readme.md", "# hi", nil)

	creds := map[string]any{"endpoint": remote.URL, "accessKey": remoteAK, "secretKey": remoteSK, "region": "us-east-1"}
	var test struct {
		Buckets []struct {
			Name   string
			Exists bool
		}
	}
	if code := e.call("POST", "/api/v1/export/test", creds, &test); code != 200 || len(test.Buckets) != 2 {
		t.Fatalf("test: %d %+v", code, test)
	}
	for _, b := range test.Buckets {
		if b.Exists != (b.Name == "docs") {
			t.Errorf("exists on the remote: %+v", b)
		}
	}

	body := map[string]any{"name": "offsite", "allBuckets": true, "settings": true}
	for k, v := range creds {
		body[k] = v
	}
	var target exportTargetJSON
	if code := e.call("POST", "/api/v1/export/targets", body, &target); code != 201 || !target.AllBuckets || !target.SecretKeySet {
		t.Fatalf("create: %d %+v", code, target)
	}
	run := func() map[string]importJob {
		t.Helper()
		if code := e.call("POST", "/api/v1/export/targets/"+target.ID+"/run", nil, nil); code != 200 {
			t.Fatalf("run: %d", code)
		}
		jobs := map[string]importJob{}
		for _, j := range e.waitExport(target.ID, 2) { // newest first
			if _, seen := jobs[j.Bucket]; !seen {
				jobs[j.Bucket] = j // the latest run's job per bucket
			}
		}
		return jobs
	}

	jobs := run()
	if j := jobs["photos"]; j.State != "done" || j.Copied != 2 || j.Update {
		t.Errorf("photos: %+v", j)
	}
	if j := jobs["docs"]; j.State != "done" || j.Copied != 1 || !j.Update {
		t.Errorf("docs (already on the remote): %+v", j)
	}
	got := remoteKeys(t, rc, "photos")
	if got["a.txt"] != "alpha" || got["dir/b.txt"] != "beta" || len(got) != 2 {
		t.Errorf("remote photos (the trash must not travel): %v", got)
	}
	if v, _ := rc.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("photos")}); v == nil || v.Status != "Enabled" {
		t.Errorf("versioning not sent: %+v", v)
	}
	if tg, err := rc.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String("photos")}); err != nil || len(tg.TagSet) != 1 {
		t.Errorf("tags not sent: %+v %v", tg, err)
	}

	// Again: nothing changed, nothing is sent. Then one change goes.
	time.Sleep(10 * time.Millisecond)
	e.call("PUT", "/api/v1/buckets/photos/upload?key=a.txt", "alpha, edited", nil)
	jobs = run()
	if j := jobs["photos"]; j.Copied != 1 || j.Unchanged != 1 {
		t.Errorf("second run, photos: %+v", j)
	}
	if got := remoteKeys(t, rc, "photos"); got["a.txt"] != "alpha, edited" {
		t.Errorf("edited file not sent: %v", got)
	}

	for field, bad := range map[string]map[string]any{
		"buckets": {"name": "x", "endpoint": remote.URL, "accessKey": "a", "secretKey": "b"},
		"name":    {"name": "", "endpoint": remote.URL, "accessKey": "a", "secretKey": "b", "allBuckets": true},
	} {
		var out map[string]string
		if code := e.call("POST", "/api/v1/export/targets", bad, &out); code != 400 || out["field"] != field {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
}

// bin's anonymous access, written as an S3 bucket policy, reads back the same.
func TestS3BucketPolicyRoundTrip(t *testing.T) {
	for _, p := range []auth.Policy{
		{Mode: "public"},
		{Mode: "custom", Rules: []auth.Rule{
			{Prefix: "pub/", Access: auth.ReadOnly}, {Prefix: "inbox/", Access: auth.WriteOnly},
			{Prefix: "shared/", Access: auth.ReadWrite}, {Prefix: "one.txt", Access: auth.ReadOnly, Exact: true},
		}},
	} {
		doc, err := s3BucketPolicy("b", p)
		if err != nil {
			t.Fatal(err)
		}
		back, notes, ok := anonymousPolicy("b", doc)
		// The import returns rules sorted by prefix: compare them as sets.
		sortRules := func(rs []auth.Rule) { sort.Slice(rs, func(i, j int) bool { return rs[i].Prefix < rs[j].Prefix }) }
		sortRules(back.Rules)
		sortRules(p.Rules)
		if !ok || back.String() != p.String() {
			t.Errorf("%s -> %s -> %+v %v %v", p.String(), doc, back, ok, notes)
		}
		if !strings.Contains(doc, `"Version":"2012-10-17"`) {
			t.Errorf("not an S3 policy: %s", doc)
		}
	}
}
