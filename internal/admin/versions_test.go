package admin

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestConsoleVersioning(t *testing.T) {
	e := newEnv(t)
	e.login()
	if code := e.call("POST", "/api/v1/buckets", map[string]string{"name": "docs"}, nil); code != 201 {
		t.Fatalf("create bucket: %d", code)
	}
	var b bucketJSON
	if code := e.call("PUT", "/api/v1/buckets/docs/versioning", map[string]string{"status": "Disabled"}, nil); code != 400 {
		t.Errorf("status Disabled: %d", code)
	}
	if code := e.call("PUT", "/api/v1/buckets/docs/versioning", map[string]string{"status": "Enabled"}, &b); code != 200 || b.Versioning != "Enabled" {
		t.Fatalf("enable: %d %+v", code, b)
	}

	upload := func(body string) objectJSON {
		var o objectJSON
		if code := e.call("PUT", "/api/v1/buckets/docs/upload?key=notes.txt", body, &o); code != 201 {
			t.Fatalf("upload: %d", code)
		}
		return o
	}
	v1, v2 := upload("first"), upload("second")
	if v1.VersionID == "" || v1.VersionID == v2.VersionID {
		t.Fatalf("version ids %q %q", v1.VersionID, v2.VersionID)
	}

	var hist struct {
		Versions []versionJSON `json:"versions"`
	}
	e.call("GET", "/api/v1/buckets/docs/versions?key=notes.txt", nil, &hist)
	if len(hist.Versions) != 2 || hist.Versions[0].VersionID != v2.VersionID || !hist.Versions[0].IsLatest {
		t.Fatalf("history = %+v", hist.Versions)
	}

	download := func(versionID string) (int, string) {
		resp, err := e.client.Get(e.console + "/api/v1/buckets/docs/download?key=notes.txt&versionId=" + url.QueryEscape(versionID))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	if code, body := download(v1.VersionID); code != 200 || body != "first" {
		t.Errorf("download v1: %d %q", code, body)
	}
	var o objectJSON
	if code := e.call("GET", "/api/v1/buckets/docs/object?key=notes.txt&versionId="+v1.VersionID, nil, &o); code != 200 || o.Size != 5 {
		t.Errorf("stat v1: %d %+v", code, o)
	}

	// Restore v1: a new version with v1's content.
	if code := e.call("POST", "/api/v1/buckets/docs/versions/restore", map[string]string{"key": "notes.txt", "versionId": v1.VersionID}, &o); code != 200 || o.VersionID == v1.VersionID {
		t.Fatalf("restore: %d %+v", code, o)
	}
	if code, body := download(o.VersionID); code != 200 || body != "first" {
		t.Errorf("restored content: %d %q", code, body)
	}

	// Deleting from the explorer leaves a marker: the object shows among the deleted ones.
	if code := e.call("DELETE", "/api/v1/buckets/docs/objects", map[string]any{"keys": []string{"notes.txt"}}, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	var page struct {
		Objects []objectJSON `json:"objects"`
	}
	e.call("GET", "/api/v1/buckets/docs/objects", nil, &page)
	if len(page.Objects) != 0 {
		t.Errorf("deleted object still listed: %+v", page.Objects)
	}
	e.call("GET", "/api/v1/buckets/docs/objects?deleted=true", nil, &page)
	if len(page.Objects) != 1 || page.Objects[0].Key != "notes.txt" || page.Objects[0].VersionID == "" {
		t.Fatalf("deleted listing = %+v", page.Objects)
	}
	if code := e.call("GET", "/api/v1/buckets/docs/object?key=notes.txt", nil, nil); code != 404 {
		t.Errorf("stat deleted: %d", code)
	}
	var apiErr apiError
	if code := e.call("GET", "/api/v1/buckets/docs/object?key=notes.txt&versionId="+page.Objects[0].VersionID, nil, &apiErr); code != 409 || apiErr.Error != "delete_marker" {
		t.Errorf("stat marker: %d %+v", code, apiErr)
	}

	// Removing the marker brings it back.
	var del struct {
		DeleteMarker bool `json:"deleteMarker"`
	}
	if code := e.call("DELETE", "/api/v1/buckets/docs/versions?key=notes.txt&versionId="+page.Objects[0].VersionID, nil, &del); code != 200 || !del.DeleteMarker {
		t.Fatalf("undelete: %d %+v", code, del)
	}
	if code, body := download(""); code != 200 || body != "first" {
		t.Errorf("after undelete: %d %q", code, body)
	}
	if code := e.call("DELETE", "/api/v1/buckets/docs/versions?key=notes.txt", nil, &apiErr); code != 400 {
		t.Errorf("delete version without id: %d", code)
	}

	// Suspend, then force-delete the bucket: every version goes.
	if code := e.call("PUT", "/api/v1/buckets/docs/versioning", map[string]string{"status": "Suspended"}, &b); code != 200 || b.Versioning != "Suspended" {
		t.Errorf("suspend: %d %+v", code, b)
	}
	if code := e.call("DELETE", "/api/v1/buckets/docs", nil, nil); code != 409 {
		t.Errorf("delete bucket with versions: %d", code)
	}
	if code := e.call("DELETE", "/api/v1/buckets/docs?force=true", nil, nil); code != 204 {
		t.Errorf("force delete: %d", code)
	}

	// The audit log names the version actions.
	var entries struct {
		Entries []struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
		} `json:"entries"`
	}
	e.call("GET", "/api/v1/audit?source=console", nil, &entries)
	actions := map[string]string{}
	for _, en := range entries.Entries {
		actions[en.Action] = en.Resource
	}
	for _, a := range []string{"PutBucketVersioning", "RestoreObjectVersion", "DeleteObjectVersion"} {
		if _, ok := actions[a]; !ok {
			t.Errorf("audit log misses %s: %v", a, actions)
		}
	}
	if r := actions["DeleteObjectVersion"]; !strings.Contains(r, "(version ") {
		t.Errorf("DeleteObjectVersion resource = %q", r)
	}
}

func TestConsoleCORS(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "assets"}, nil)

	var b bucketJSON
	e.call("GET", "/api/v1/buckets/assets", nil, &b)
	if b.CORS == nil || len(b.CORS) != 0 {
		t.Errorf("new bucket CORS = %#v, want an empty list", b.CORS)
	}
	rules := map[string]any{"rules": []map[string]any{{
		"allowedOrigins": []string{"https://app.example.com"}, "allowedMethods": []string{"GET", "PUT"},
		"allowedHeaders": []string{"*"}, "exposeHeaders": []string{"ETag"}, "maxAgeSeconds": 300,
	}}}
	if code := e.call("PUT", "/api/v1/buckets/assets/cors", rules, &b); code != 200 || len(b.CORS) != 1 || *b.CORS[0].MaxAgeSeconds != 300 {
		t.Fatalf("put CORS: %d %+v", code, b)
	}

	// The S3 API applies the rules at once.
	resp := rawPreflight(t, e.s3URL+"/assets/file.bin", "https://app.example.com", "PUT")
	if resp != 200 {
		t.Errorf("preflight after console change: %d", resp)
	}

	var apiErr apiError
	bad := map[string]any{"rules": []map[string]any{{"allowedOrigins": []string{"*"}, "allowedMethods": []string{"PATCH"}}}}
	if code := e.call("PUT", "/api/v1/buckets/assets/cors", bad, &apiErr); code != 400 || apiErr.Error != "invalid_cors" || !strings.Contains(apiErr.Message, "PATCH") {
		t.Errorf("invalid rule: %d %+v", code, apiErr)
	}
	if code := e.call("PUT", "/api/v1/buckets/assets/cors", map[string]any{"rules": []any{}}, &b); code != 200 || len(b.CORS) != 0 {
		t.Errorf("clear CORS: %d %+v", code, b)
	}
	if resp := rawPreflight(t, e.s3URL+"/assets/file.bin", "https://app.example.com", "PUT"); resp != 403 {
		t.Errorf("preflight after clearing: %d", resp)
	}
}

func rawPreflight(t *testing.T, target, origin, method string) int {
	t.Helper()
	req, _ := http.NewRequest("OPTIONS", target, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
