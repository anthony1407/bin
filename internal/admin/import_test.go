package admin

import (
	"context"
	"net/netip"
	"testing"
	"time"
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
