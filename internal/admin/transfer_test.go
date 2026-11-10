package admin

import "testing"

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
	var out struct{ Copied int }

	// Copy into another bucket: originals stay, folders keep their layout.
	req := map[string]any{"keys": []string{"a.txt"}, "prefixes": []string{"dir/"}, "targetBucket": "dst", "targetPrefix": "archive"}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 3 {
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

	// Move within the bucket: originals go to the trash.
	req = map[string]any{"prefixes": []string{"dir/"}, "targetPrefix": "moved/", "move": true}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 2 {
		t.Fatalf("move: %d %+v", code, out)
	}
	for _, k := range []string{"moved/dir/b.txt", "moved/dir/sub/c.txt", ".trash/dir/b.txt", ".trash/dir/sub/c.txt"} {
		if !exists("src", k) {
			t.Errorf("src/%s missing after move", k)
		}
	}
	if exists("src", "dir/b.txt") {
		t.Error("moved original still in place")
	}

	// Moving a file onto itself is a no-op, not a trip to the trash.
	req = map[string]any{"keys": []string{"a.txt"}, "move": true}
	if code := e.call("POST", "/api/v1/buckets/src/copy", req, &out); code != 200 || out.Copied != 0 || !exists("src", "a.txt") {
		t.Errorf("move onto itself: %d %+v", code, out)
	}

	for name, bad := range map[string]map[string]any{
		"into itself":     {"prefixes": []string{"moved/"}, "targetPrefix": "moved/dir/"},
		"into the trash":  {"keys": []string{"a.txt"}, "targetPrefix": ".trash/x"},
		"from the trash":  {"keys": []string{".trash/dir/b.txt"}, "targetPrefix": "x"},
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
