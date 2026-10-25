package admin

import (
	"io"
	"net/http"
	"testing"
)

func TestShareLinkRecords(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "docs"}, nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=report.pdf", "quarterly", nil)
	e.call("PUT", "/api/v1/buckets/docs/upload?key=other.pdf", "other", nil)

	var first, second struct{ ID, URL string }
	e.call("POST", "/api/v1/buckets/docs/share", map[string]any{"key": "report.pdf", "expiresIn": 3600}, &first)
	e.call("POST", "/api/v1/buckets/docs/share", map[string]any{"key": "report.pdf", "expiresIn": 600, "download": true}, &second)
	if first.ID == "" || second.ID == "" {
		t.Fatalf("share: %+v %+v", first, second)
	}

	get := func(u string) (int, string) {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(first.URL); code != 200 || body != "quarterly" {
		t.Fatalf("first link: %d %q", code, body)
	}

	// The explorer listing marks shared objects, with the last expiry.
	var list struct{ Objects []objectJSON }
	e.call("GET", "/api/v1/buckets/docs/objects", nil, &list)
	for _, o := range list.Objects {
		switch o.Key {
		case "report.pdf":
			if o.Shares != 2 || o.ShareExpiresAt == nil {
				t.Errorf("report.pdf: %+v", o)
			}
		case "other.pdf":
			if o.Shares != 0 || o.ShareExpiresAt != nil {
				t.Errorf("other.pdf: %+v", o)
			}
		}
	}

	// The share dialog lists the live links, longest-lived first, URL included.
	var links struct{ Links []shareLinkJSON }
	e.call("GET", "/api/v1/buckets/docs/shares?key=report.pdf", nil, &links)
	if len(links.Links) != 2 || links.Links[0].ID != first.ID || links.Links[0].URL != first.URL ||
		!links.Links[1].Download || links.Links[0].CreatedBy != rootAK {
		t.Fatalf("links: %+v", links.Links)
	}

	// Revoking one stops it at once; the other keeps working.
	if code := e.call("DELETE", "/api/v1/shares/"+first.ID, nil, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, body := get(first.URL); code != 403 {
		t.Errorf("revoked link: %d %s", code, body)
	}
	if code, _ := get(second.URL); code != 200 {
		t.Errorf("other link after revoking one: %d", code)
	}
	e.call("GET", "/api/v1/buckets/docs/shares?key=report.pdf", nil, &links)
	if len(links.Links) != 1 || links.Links[0].ID != second.ID {
		t.Errorf("after revoke: %+v", links.Links)
	}
	if code := e.call("DELETE", "/api/v1/shares/sl_missing", nil, nil); code != 404 {
		t.Errorf("revoke unknown: %d", code)
	}

	// Revoke all clears the records too.
	if code := e.call("POST", "/api/v1/settings/revoke-share-links", nil, nil); code != 204 {
		t.Fatalf("revoke all: %d", code)
	}
	list.Objects = nil // decoding into old elements would keep omitted fields
	e.call("GET", "/api/v1/buckets/docs/objects", nil, &list)
	for _, o := range list.Objects {
		if o.Shares != 0 {
			t.Errorf("after revoke all: %+v", o)
		}
	}
}
