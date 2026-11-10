package admin

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"sort"
	"testing"
)

func TestCopyAndMoveObjects(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "src"}, nil)
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "dst"}, nil)
	e.call("PUT", "/api/v1/buckets/src/upload?key=a.txt", "a", nil)
	e.call("PUT", "/api/v1/buckets/src/upload?key=dir%2Fb.txt", "b", nil)
	e.call("PUT", "/api/v1/buckets/src/upload?key=dir%2Fsub%2Fc.txt", "c", nil)
	exists := func(bucket, key string) bool {
		return e.call("GET", "/api/v1/buckets/"+bucket+"/object?key="+key, nil, nil) == 200
	}
	var out struct{ Copied, Trashed int }

	// Copy into another bucket: originals stay, folders keep their layout.
	req := map[string]any{"keys": []string{"a.txt"}, "prefixes": []string{"dir/"}, "targetBucket": "dst", "targetPrefix": "archive"}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 3 || out.Trashed != 0 {
		t.Fatalf("copy: %d %+v", code, out)
	}
	for _, k := range []string{"archive/a.txt", "archive/dir/b.txt", "archive/dir/sub/c.txt"} {
		if !exists("dst", k) {
			t.Errorf("dst/%s not copied", k)
		}
	}
	if !exists("src", "a.txt") || !exists("src", "dir/sub/c.txt") {
		t.Error("copy removed originals")
	}

	// Move within the bucket: a rename, nothing goes to the trash.
	req = map[string]any{"prefixes": []string{"dir/"}, "targetPrefix": "moved/", "move": true}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 2 || out.Trashed != 0 {
		t.Fatalf("move: %d %+v", code, out)
	}
	for k, want := range map[string]bool{"moved/dir/b.txt": true, "moved/dir/sub/c.txt": true, "dir/b.txt": false, ".trash/dir/b.txt": false} {
		if exists("src", k) != want {
			t.Errorf("src/%s exists=%v after move, want %v", k, !want, want)
		}
	}

	// Move to another bucket: copies land, originals go to the source's trash.
	req = map[string]any{"prefixes": []string{"moved/"}, "targetBucket": "dst", "move": true}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 2 || out.Trashed != 2 {
		t.Fatalf("move across buckets: %d %+v", code, out)
	}
	if !exists("dst", "moved/dir/b.txt") || !exists("src", ".trash/moved/dir/b.txt") || exists("src", "moved/dir/b.txt") {
		t.Error("move across buckets: wrong layout")
	}

	// Moving a file onto itself is a no-op, not a trip to the trash.
	req = map[string]any{"keys": []string{"a.txt"}, "move": true}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 0 || !exists("src", "a.txt") {
		t.Errorf("move onto itself: %d %+v", code, out)
	}

	for name, bad := range map[string]map[string]any{
		"into itself":     {"prefixes": []string{"dir/"}, "targetPrefix": "dir/x/"},
		"into the trash":  {"keys": []string{"a.txt"}, "targetPrefix": ".trash/x"},
		"from the trash":  {"keys": []string{".trash/moved/dir/b.txt"}, "targetPrefix": "x"},
		"folder no slash": {"prefixes": []string{"moved"}, "targetPrefix": "x"},
	} {
		if code := e.call("POST", "/api/v1/buckets/src/copy", bad, nil); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := e.call("POST", "/api/v1/buckets/src/copy", map[string]any{"keys": []string{"missing.txt"}, "targetBucket": "dst"}, nil); code != 404 {
		t.Errorf("missing source: %d", code)
	}
}

// In a versioned bucket a move cannot be a rename: it copies, and the old
// key keeps its history behind a delete marker.
func TestMoveInVersionedBucket(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "ver"}, nil)
	e.call("PUT", "/api/v1/buckets/ver/versioning", map[string]string{"status": "Enabled"}, nil)
	for _, k := range []string{"a.txt", "b.txt", "c.txt"} {
		e.call("PUT", "/api/v1/buckets/ver/upload?key="+k, k, nil)
	}
	var out struct{ Moved int }
	if code := e.call("POST", "/api/v1/buckets/ver/trash", map[string]any{"keys": []string{"a.txt", "b.txt", "c.txt"}}, &out); code != 200 || out.Moved != 3 {
		t.Fatalf("trash: %d %+v", code, out)
	}
	for _, k := range []string{".trash/a.txt", ".trash/b.txt", ".trash/c.txt"} {
		if e.call("GET", "/api/v1/buckets/ver/object?key="+k, nil, nil) != 200 {
			t.Errorf("%s not in the trash", k)
		}
	}
	var versions struct{ Versions []struct{ DeleteMarker bool } }
	e.call("GET", "/api/v1/buckets/ver/versions?key=a.txt", nil, &versions)
	if len(versions.Versions) != 2 || !versions.Versions[0].DeleteMarker {
		t.Errorf("history of a.txt: %+v", versions.Versions)
	}
}

func TestFolderUsage(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "fold"}, nil)
	e.call("PUT", "/api/v1/buckets/fold/upload?key=dir%2Fa.txt", "aa", nil)
	e.call("PUT", "/api/v1/buckets/fold/upload?key=dir%2Fsub%2Fb.txt", "bbb", nil)
	var out struct {
		Folders map[string]struct {
			Objects, Size int64
		}
	}
	if code := e.call("POST", "/api/v1/buckets/fold/folders/usage", map[string]any{"prefixes": []string{"dir/", "none/"}}, &out); code != 200 {
		t.Fatalf("usage: %d", code)
	}
	if d := out.Folders["dir/"]; d.Objects != 2 || d.Size != 5 {
		t.Errorf("dir/: %+v", d)
	}
	if d := out.Folders["none/"]; d.Objects != 0 {
		t.Errorf("none/: %+v", d)
	}
}

func TestZipDownload(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "zips"}, nil)
	e.call("PUT", "/api/v1/buckets/zips/upload?key=docs%2Fa.txt", "alpha", nil)
	e.call("PUT", "/api/v1/buckets/zips/upload?key=docs%2Fpics%2Fb.txt", "beta", nil)
	e.call("PUT", "/api/v1/buckets/zips/upload?key=docs%2F..%2F..%2Fevil.txt", "x", nil)
	e.call("POST", "/api/v1/buckets/zips/folders", map[string]string{"prefix": "docs/empty"}, nil)

	var ticket struct{ URL string }
	req := map[string]any{"keys": []string{"docs/a.txt", "docs/../../evil.txt"}, "prefixes": []string{"docs/pics/", "docs/empty/"}, "base": "docs/"}
	if code := e.call("POST", "/api/v1/buckets/zips/zip", req, &ticket); code != 201 || ticket.URL == "" {
		t.Fatalf("ticket: %d %+v", code, ticket)
	}
	resp, err := e.client.Get(e.console + ticket.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(data)
	}
	sort.Strings(names)
	want := []string{"a.txt", "empty/", "evil.txt", "pics/b.txt"}
	if len(names) != len(want) {
		t.Fatalf("entries: %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries: %v, want %v", names, want)
		}
	}
	if got["a.txt"] != "alpha" || got["pics/b.txt"] != "beta" {
		t.Errorf("contents: %v", got)
	}

	// Tickets belong to their session.
	anon, err := (&http.Client{}).Get(e.console + ticket.URL)
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != 401 {
		t.Errorf("without a session: %d", anon.StatusCode)
	}
	if code := e.call("GET", "/api/v1/zip/nope", nil, nil); code != 404 {
		t.Errorf("unknown ticket: %d", code)
	}
}
