package admin

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestAllowedRemoteIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":              false,
		"::1":                    false,
		"169.254.169.254":        false, // cloud metadata (link-local)
		"::ffff:169.254.169.254": false,
		"fe80::1":                false,
		"fd00:ec2::254":          false,
		"100.100.100.200":        false,
		"0.0.0.0":                false,
		"224.0.0.1":              false,
		"10.0.0.5":               true, // a server on the LAN
		"192.168.1.20":           true,
		"52.216.0.1":             true,
	} {
		if got := allowedRemoteIP(netip.MustParseAddr(addr)); got != want {
			t.Errorf("allowedRemoteIP(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestRemoteClientRejectsBadEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"s3.example.com", "ftp://s3.example.com", "https://", "https://u:p@s3.example.com",
		"https://s3.example.com/bucket", "https://s3.example.com/?x=1",
	} {
		if _, err := remoteClient(importRemote{Endpoint: endpoint, AccessKey: "a", SecretKey: "b"}); err == nil {
			t.Errorf("%q: accepted", endpoint)
		}
	}
	if _, err := remoteClient(importRemote{Endpoint: "https://s3.example.com/", AccessKey: "a", SecretKey: "b"}); err != nil {
		t.Errorf("valid endpoint refused: %v", err)
	}
}

// The check runs on the dialed address, so a name resolving to loopback is
// refused too, and the reply does not echo the remote error.
func TestRemoteClientRefusesLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:9000", "http://localhost:9000"} {
		client, err := remoteClient(importRemote{Endpoint: endpoint, AccessKey: "a", SecretKey: "b", Region: "us-east-1"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err = client.ListBuckets(ctx)
		cancel()
		if code, _ := remoteErrorMessage(err); code != "endpoint_not_allowed" {
			t.Errorf("%s: got %q (%v)", endpoint, code, err)
		}
	}
}

// An empty remote bucket is imported as an empty bucket, not dropped.
func TestImportKeepsEmptyBuckets(t *testing.T) {
	remote, rc := newRemote(t)
	ctx := context.Background()
	for _, b := range []string{"full", "empty"} {
		rc.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(b)})
	}
	rc.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("full"), Key: aws.String("a.txt"), Body: strings.NewReader("a")})

	e := newEnv(t)
	e.login()
	if code := e.call("POST", "/api/v1/import/start", map[string]any{
		"endpoint": remote.URL, "accessKey": remoteAK, "secretKey": remoteSK, "region": "us-east-1",
		"buckets": []string{"full", "empty"},
	}, nil); code != 200 {
		t.Fatalf("start: %d", code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var list struct{ Jobs []importJob }
		e.call("GET", "/api/v1/import/jobs", nil, &list)
		done := 0
		for _, j := range list.Jobs {
			if j.State == "done" {
				done++
			}
		}
		if done == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs did not finish: %+v", list.Jobs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, b := range []string{"full", "empty"} {
		if code := e.call("GET", "/api/v1/buckets/"+b, nil, nil); code != 200 {
			t.Errorf("bucket %s after import: %d", b, code)
		}
	}
}

// In a container, localhost is the container: the refusal points to the host.
func TestBlockedAddressHintsDockerHost(t *testing.T) {
	saved := inContainer
	t.Cleanup(func() { inContainer = saved })
	for _, in := range []bool{false, true} {
		inContainer = in
		_, msg := remoteErrorMessage(errBlockedAddress)
		if got := strings.Contains(msg, "host.docker.internal"); got != in {
			t.Errorf("inContainer=%v: hint shown=%v in %q", in, got, msg)
		}
	}
}
