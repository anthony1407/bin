package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// upload sends a file to a dropzone the way the public page does: no session,
// only the token header.
func (e *env) dropzoneUpload(token, name, body string) (int, map[string]string) {
	e.t.Helper()
	req, _ := http.NewRequest("PUT", e.console+"/api/v1/public/dropzone/files?name="+urlQuery(name), strings.NewReader(body))
	req.Header.Set(dropzoneTokenHeader, token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/141.0 Safari/537.36")
	resp, err := http.DefaultClient.Do(req) // no cookie jar: an outside uploader
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]string{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestDropzones(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.call("POST", "/api/v1/buckets", map[string]string{"name": "inbox"}, nil)

	var dz dropzoneJSON
	if code := e.call("POST", "/api/v1/dropzones", map[string]any{
		"bucket": "inbox", "prefix": "clients", "note": "Send the signed contract", "expiresIn": 3600, "maxBytes": 10,
	}, &dz); code != 201 || dz.Prefix != "clients/" || !strings.Contains(dz.URL, "/upload#") {
		t.Fatalf("create: %d %+v", code, dz)
	}
	token := dz.URL[strings.Index(dz.URL, "#")+1:]

	if code := e.call("POST", "/api/v1/dropzones", map[string]any{
		"bucket": "missing", "expiresIn": 3600, "maxBytes": 10,
	}, nil); code != 404 {
		t.Errorf("unknown bucket: %d", code)
	}

	// The public page learns the note and limits, not where files go.
	req, _ := http.NewRequest("GET", e.console+"/api/v1/public/dropzone", nil)
	req.Header.Set(dropzoneTokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if resp.StatusCode != 200 || info["note"] != "Send the signed contract" || info["bucket"] != nil {
		t.Errorf("public info: %d %v", resp.StatusCode, info)
	}

	if code, out := e.dropzoneUpload(token, "../../secret/contract.pdf", "signed"); code != 201 || out["name"] != "contract.pdf" {
		t.Errorf("upload: %d %v", code, out)
	}
	// A second file with the same name is kept apart, never overwriting.
	if code, out := e.dropzoneUpload(token, "contract.pdf", "again"); code != 201 || out["name"] != "contract (1).pdf" {
		t.Errorf("same name: %d %v", code, out)
	}
	var listed struct{ Objects []objectJSON }
	e.call("GET", "/api/v1/buckets/inbox/objects?prefix=clients/", nil, &listed)
	if len(listed.Objects) != 2 || listed.Objects[0].Key != "clients/contract (1).pdf" || listed.Objects[1].Key != "clients/contract.pdf" {
		t.Errorf("stored: %+v", listed.Objects)
	}

	if code, _ := e.dropzoneUpload(token, "big.bin", "more than ten bytes"); code != 413 {
		t.Errorf("over the limit: %d", code)
	}
	if code, _ := e.dropzoneUpload("wrong-token", "x.txt", "x"); code != 404 {
		t.Errorf("bad token: %d", code)
	}
	if code, _ := e.dropzoneUpload(token, "..", "x"); code != 400 {
		t.Errorf("bad name: %d", code)
	}

	// Listing shows the link again; deleting it stops uploads at once.
	var list struct{ Dropzones []dropzoneJSON }
	if e.call("GET", "/api/v1/dropzones", nil, &list); len(list.Dropzones) != 1 || list.Dropzones[0].URL != dz.URL {
		t.Errorf("list: %+v", list.Dropzones)
	}
	// The console sees what came in through the link: rejected uploads are not counted.
	if got := list.Dropzones[0]; got.Uploads != 2 || got.UploadedBytes != int64(len("signed")+len("again")) || got.LastUploadAt == nil {
		t.Errorf("summary: %+v", got)
	}
	var uploads struct{ Uploads []dropzoneUploadJSON }
	if code := e.call("GET", "/api/v1/dropzones/"+dz.ID+"/uploads", nil, &uploads); code != 200 || len(uploads.Uploads) != 2 ||
		uploads.Uploads[0].Name != "contract (1).pdf" || uploads.Uploads[0].Key != "clients/contract (1).pdf" || uploads.Uploads[1].Size != 6 {
		t.Errorf("uploads: %d %+v", code, uploads.Uploads)
	}
	if u := uploads.Uploads[0]; u.Remote != "127.0.0.1" || !strings.Contains(u.UserAgent, "Chrome/141") {
		t.Errorf("uploader not recorded: %+v", u)
	}
	if code := e.call("DELETE", "/api/v1/dropzones/"+dz.ID, nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := e.dropzoneUpload(token, "late.txt", "x"); code != 404 {
		t.Errorf("after delete: %d", code)
	}
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":             "report.pdf",
		"a/b/c.txt":              "c.txt",
		`C:\Users\me\photo.jpg`:  "photo.jpg",
		"bad\x00\nname.txt":      "badname.txt",
		"..":                     "",
		"":                       "",
		strings.Repeat("a", 300): "",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := numberedName("archive.tar.gz", 2); got != "archive.tar (2).gz" {
		t.Errorf("numberedName = %q", got)
	}
	if got := numberedName(".env", 1); got != ".env (1)" {
		t.Errorf("numberedName = %q", got)
	}
}
