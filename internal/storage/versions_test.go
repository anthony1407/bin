package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func readVersion(t *testing.T, s *Store, bucket, key, versionID string) string {
	t.Helper()
	_, d, err := s.OpenVersion(context.Background(), bucket, key, versionID)
	if err != nil {
		t.Fatalf("open %s/%s@%s: %v", bucket, key, versionID, err)
	}
	defer d.Close()
	data, err := io.ReadAll(d.Verified())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func versionIDs(t *testing.T, s *Store, bucket, key string) []string {
	t.Helper()
	vs, err := s.ObjectVersions(context.Background(), bucket, key, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vs {
		id := v.VersionID
		if v.DeleteMarker {
			id += "(marker)"
		}
		if v.IsLatest {
			id += "*"
		}
		out = append(out, id)
	}
	return out
}

func TestVersioningUnversionedBucket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "ver")

	info := put(t, s, "ver", "k", "one", PutOptions{})
	if info.VersionID != NullVersion {
		t.Errorf("version = %q, want null", info.VersionID)
	}
	put(t, s, "ver", "k", "two", PutOptions{})
	if got := versionIDs(t, s, "ver", "k"); !reflect.DeepEqual(got, []string{"null*"}) {
		t.Errorf("versions = %v", got)
	}
	res, err := s.DeleteVersion(ctx, "ver", "k", "")
	if err != nil || res != (DeleteResult{}) {
		t.Errorf("delete = %+v, %v", res, err)
	}
	if got := versionIDs(t, s, "ver", "k"); got != nil {
		t.Errorf("after delete: %v", got)
	}
	if n := blobCount(t, dir); n != 0 {
		t.Errorf("%d blobs left", n)
	}
}

func TestVersioningEnabled(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "ver")
	put(t, s, "ver", "k", "before", PutOptions{}) // the null version, from before versioning
	if err := s.SetBucketVersioning(ctx, "ver", VersioningEnabled); err != nil {
		t.Fatal(err)
	}

	v1 := put(t, s, "ver", "k", "one", PutOptions{})
	v2 := put(t, s, "ver", "k", "two", PutOptions{})
	if !ValidVersionID(v1.VersionID) || v1.VersionID == NullVersion || v1.VersionID == v2.VersionID {
		t.Fatalf("version ids %q %q", v1.VersionID, v2.VersionID)
	}
	want := []string{v2.VersionID + "*", v1.VersionID, "null"}
	if got := versionIDs(t, s, "ver", "k"); !reflect.DeepEqual(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
	if _, data := readAll(t, s, "ver", "k"); data != "two" {
		t.Errorf("latest = %q", data)
	}
	for id, data := range map[string]string{v1.VersionID: "one", NullVersion: "before"} {
		if got := readVersion(t, s, "ver", "k", id); got != data {
			t.Errorf("version %s = %q, want %q", id, got, data)
		}
	}

	// A delete adds a marker: the key disappears, its versions stay.
	res, err := s.DeleteVersion(ctx, "ver", "k", "")
	if err != nil || !res.DeleteMarker || !ValidVersionID(res.VersionID) || res.VersionID == NullVersion {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	if _, err := s.StatObject(ctx, "ver", "k"); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("stat after delete: %v", err)
	}
	marker, err := s.StatVersion(ctx, "ver", "k", "")
	if !errors.Is(err, ErrDeleteMarker) || marker.VersionID != res.VersionID || !marker.IsLatest {
		t.Errorf("stat latest = %+v, %v", marker, err)
	}
	if _, _, err := s.OpenVersion(ctx, "ver", "k", res.VersionID); !errors.Is(err, ErrDeleteMarker) {
		t.Errorf("open marker: %v", err)
	}
	page, _ := s.ListObjects(ctx, "ver", ListOptions{MaxKeys: 10})
	if len(page.Objects) != 0 {
		t.Errorf("listing shows %v", page.Objects)
	}
	deleted, _ := s.ListObjects(ctx, "ver", ListOptions{MaxKeys: 10, Deleted: true})
	if len(deleted.Objects) != 1 || deleted.Objects[0].VersionID != res.VersionID {
		t.Errorf("deleted listing = %+v", deleted.Objects)
	}
	if u, _ := s.BucketUsage(ctx); u["ver"].Objects != 3 || u["ver"].Bytes != int64(len("before")+len("one")+len("two")) {
		t.Errorf("usage = %+v", u["ver"])
	}

	// Removing the marker brings the object back.
	if res, err := s.DeleteVersion(ctx, "ver", "k", res.VersionID); err != nil || !res.DeleteMarker {
		t.Fatalf("delete marker = %+v, %v", res, err)
	}
	if _, data := readAll(t, s, "ver", "k"); data != "two" {
		t.Errorf("after undelete = %q", data)
	}

	// Removing the latest version promotes the previous one; removing an
	// older one leaves the latest alone. Their data goes.
	if _, err := s.DeleteVersion(ctx, "ver", "k", v2.VersionID); err != nil {
		t.Fatal(err)
	}
	if _, data := readAll(t, s, "ver", "k"); data != "one" {
		t.Errorf("after removing v2 = %q", data)
	}
	if _, err := s.DeleteVersion(ctx, "ver", "k", NullVersion); err != nil {
		t.Fatal(err)
	}
	if got := versionIDs(t, s, "ver", "k"); !reflect.DeepEqual(got, []string{v1.VersionID + "*"}) {
		t.Errorf("versions = %v", got)
	}
	if n := blobCount(t, dir); n != 1 {
		t.Errorf("%d blobs, want 1", n)
	}

	// Removing a version that does not exist succeeds, as in S3.
	if res, err := s.DeleteVersion(ctx, "ver", "k", newVersionID()); err != nil || res.DeleteMarker {
		t.Errorf("delete missing version = %+v, %v", res, err)
	}
	if _, err := s.StatVersion(ctx, "ver", "k", newVersionID()); !errors.Is(err, ErrNoSuchVersion) {
		t.Errorf("stat missing version: %v", err)
	}
	for _, bad := range []string{"v1", "NULL", strings.ToUpper(v1.VersionID)} {
		if _, err := s.StatVersion(ctx, "ver", "k", bad); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("stat %q: %v", bad, err)
		}
		if _, err := s.DeleteVersion(ctx, "ver", "k", bad); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("delete %q: %v", bad, err)
		}
	}

	// A delete of a key that never existed still leaves a marker, as in S3.
	if res, err := s.DeleteVersion(ctx, "ver", "ghost", ""); err != nil || !res.DeleteMarker {
		t.Errorf("delete ghost = %+v, %v", res, err)
	}
	if err := s.DeleteBucket(ctx, "ver"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Errorf("delete bucket with versions: %v", err)
	}
}

func TestVersioningSuspended(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "ver")
	if err := s.SetBucketVersioning(ctx, "ver", "Disabled"); !errors.Is(err, ErrInvalidVersioning) {
		t.Errorf("invalid status: %v", err)
	}
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	v1 := put(t, s, "ver", "k", "one", PutOptions{})
	s.SetBucketVersioning(ctx, "ver", VersioningSuspended)
	if b, _ := s.GetBucket(ctx, "ver"); b.Versioning != VersioningSuspended {
		t.Errorf("versioning = %q", b.Versioning)
	}

	// Writes now replace the null version and keep the older ones.
	if info := put(t, s, "ver", "k", "two", PutOptions{}); info.VersionID != NullVersion {
		t.Errorf("version = %q", info.VersionID)
	}
	put(t, s, "ver", "k", "three", PutOptions{})
	if got := versionIDs(t, s, "ver", "k"); !reflect.DeepEqual(got, []string{"null*", v1.VersionID}) {
		t.Errorf("versions = %v", got)
	}

	// A delete replaces the null version with a null delete marker.
	res, err := s.DeleteVersion(ctx, "ver", "k", "")
	if err != nil || res != (DeleteResult{VersionID: NullVersion, DeleteMarker: true}) {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	if got := versionIDs(t, s, "ver", "k"); !reflect.DeepEqual(got, []string{"null(marker)*", v1.VersionID}) {
		t.Errorf("versions = %v", got)
	}
	if n := blobCount(t, dir); n != 1 {
		t.Errorf("%d blobs, want 1 (v1)", n)
	}

	// The quota precheck counts the null version as replaced.
	put(t, s, "ver", "q", "12345", PutOptions{})
	s.SetBucketQuota(ctx, "ver", int64(len("one")+5))
	put(t, s, "ver", "q", "abcde", PutOptions{})
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	if _, err := s.PutObject(ctx, "ver", "q", strings.NewReader("x"), 1, PutOptions{}); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("versioned put over quota: %v", err)
	}
}

func TestCopyObjectVersion(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "ver")
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	v1 := put(t, s, "ver", "k", "one", PutOptions{ContentType: "text/one"})
	put(t, s, "ver", "k", "two", PutOptions{})

	// Restoring an old version is copying it over the latest.
	got, err := s.CopyObject(ctx, "ver", "k", "ver", "k", CopyOptions{SourceVersionID: v1.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionID == v1.VersionID || got.ContentType != "text/one" {
		t.Errorf("copy = %+v", got)
	}
	if _, data := readAll(t, s, "ver", "k"); data != "one" {
		t.Errorf("latest = %q", data)
	}
	if n := len(versionIDs(t, s, "ver", "k")); n != 3 {
		t.Errorf("%d versions, want 3", n)
	}
	res, _ := s.DeleteVersion(ctx, "ver", "k", "")
	if _, err := s.CopyObject(ctx, "ver", "k", "ver", "other", CopyOptions{SourceVersionID: res.VersionID}); !errors.Is(err, ErrDeleteMarker) {
		t.Errorf("copy from marker: %v", err)
	}
	if _, err := s.CopyObject(ctx, "ver", "k", "ver", "other", CopyOptions{}); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("copy deleted latest: %v", err)
	}
}

func TestMultipartVersioned(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "ver")
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	put(t, s, "ver", "k", "small", PutOptions{})
	u, err := s.CreateUpload(ctx, "ver", "k", UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := putPart(t, s, "ver", "k", u.ID, 1, []byte("multipart"))
	info := completeAll(t, s, "ver", "k", u.ID, p)
	if !ValidVersionID(info.VersionID) || info.VersionID == NullVersion {
		t.Errorf("version = %q", info.VersionID)
	}
	if n := len(versionIDs(t, s, "ver", "k")); n != 2 {
		t.Errorf("%d versions, want 2", n)
	}
}

func TestPurgeAndDeletePrefix(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "ver")
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	for i := 0; i < 3; i++ {
		putKeys(t, s, "ver", "dir/a", "dir/b", "keep")
	}

	// A folder delete follows versioning: markers, versions kept.
	n, err := s.DeletePrefix(ctx, "ver", "dir/")
	if err != nil || n != 2 {
		t.Fatalf("delete prefix = %d, %v", n, err)
	}
	if got := versionIDs(t, s, "ver", "dir/a"); len(got) != 4 || !strings.HasSuffix(got[0], "(marker)*") {
		t.Errorf("dir/a versions = %v", got)
	}

	n, err = s.PurgePrefix(ctx, "ver", "dir/")
	if err != nil || n != 8 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if got := versionIDs(t, s, "ver", "dir/a"); got != nil {
		t.Errorf("dir/a after purge = %v", got)
	}
	if n := blobCount(t, dir); n != 3 {
		t.Errorf("%d blobs, want 3 (keep)", n)
	}
	if _, err := s.PurgePrefix(ctx, "ver", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket(ctx, "ver"); err != nil {
		t.Errorf("delete purged bucket: %v", err)
	}
}

func TestScrubCoversOldVersions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openTest(t, dir)
	mustCreate(t, s, "ver")
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	old := put(t, s, "ver", "k", "old data", PutOptions{})
	put(t, s, "ver", "k", "new data", PutOptions{})
	s.DeleteVersion(ctx, "ver", "gone", "") // a marker: nothing to read

	if err := os.WriteFile(s.blobPath(old.blob), []byte("OLD DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.Scrub(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Objects != 2 || len(res.Corrupt) != 1 || res.Corrupt[0].VersionID != old.VersionID {
		t.Errorf("scrub = %+v", res)
	}
}

// TestListVersionsMatchesReference pages through random histories with every
// page size, with and without a delimiter, and compares the concatenated
// pages with a naive listing.
func TestListVersionsMatchesReference(t *testing.T) {
	ctx := context.Background()
	old := listBatch
	listBatch = 3
	t.Cleanup(func() { listBatch = old })

	s := openTest(t, t.TempDir())
	mustCreate(t, s, "ver")
	s.SetBucketVersioning(ctx, "ver", VersioningEnabled)
	rng := rand.New(rand.NewPCG(7, 11))
	names := []string{"a", "a/b", "a/b/c", "a/c", "ver", "b/", "c/d/e", "c/x", "d", "e/f"}
	history := map[string][]string{} // key -> versions, newest first ("id" or "id!" for markers)
	for i := 0; i < 40; i++ {
		k := names[rng.IntN(len(names))]
		if rng.IntN(4) == 0 {
			res, err := s.DeleteVersion(ctx, "ver", k, "")
			if err != nil {
				t.Fatal(err)
			}
			history[k] = append([]string{res.VersionID + "!"}, history[k]...)
		} else {
			info := put(t, s, "ver", k, "x", PutOptions{})
			history[k] = append([]string{info.VersionID}, history[k]...)
		}
	}
	keys := make([]string, 0, len(history))
	for k := range history {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	naive := func(prefix, delim string) []string {
		var out []string
		seen := map[string]bool{}
		for _, k := range keys {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			if delim != "" {
				if i := strings.Index(k[len(prefix):], delim); i >= 0 {
					cp := k[:len(prefix)+i+len(delim)]
					if !seen[cp] {
						seen[cp] = true
						out = append(out, cp)
					}
					continue
				}
			}
			for _, v := range history[k] {
				out = append(out, k+"@"+v)
			}
		}
		return out
	}
	flatten := func(r ListVersionsResult) []string {
		type entry struct{ sortKey, text string }
		var es []entry
		for _, v := range r.Versions {
			id := v.VersionID
			if v.DeleteMarker {
				id += "!"
			}
			es = append(es, entry{v.Key, v.Key + "@" + id})
		}
		for _, p := range r.CommonPrefixes {
			es = append(es, entry{p, p})
		}
		sort.SliceStable(es, func(i, j int) bool { return es[i].sortKey < es[j].sortKey })
		var out []string
		for _, e := range es {
			out = append(out, e.text)
		}
		return out
	}

	for _, prefix := range []string{"", "a", "a/", "c/"} {
		for _, delim := range []string{"", "/"} {
			want := naive(prefix, delim)
			for size := 1; size <= len(want)+1; size++ {
				t.Run(fmt.Sprintf("prefix=%q/delim=%q/size=%d", prefix, delim, size), func(t *testing.T) {
					var got []string
					opts := ListVersionsOptions{Prefix: prefix, Delimiter: delim, MaxKeys: size}
					for pages := 0; ; pages++ {
						if pages > len(want)+2 {
							t.Fatal("listing does not end")
						}
						r, err := s.ListVersions(ctx, "ver", opts)
						if err != nil {
							t.Fatal(err)
						}
						got = append(got, flatten(r)...)
						if !r.IsTruncated {
							break
						}
						opts.KeyMarker, opts.VersionIDMarker = r.NextKeyMarker, r.NextVersionIDMarker
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("got  %v\nwant %v", got, want)
					}
				})
			}
		}
	}

	// Exactly one version per key is the latest: the newest.
	r, _ := s.ListVersions(ctx, "ver", ListVersionsOptions{MaxKeys: 1000})
	latest := map[string]string{}
	for _, v := range r.Versions {
		if v.IsLatest {
			if latest[v.Key] != "" {
				t.Errorf("%s has two latest versions", v.Key)
			}
			latest[v.Key] = v.VersionID
		}
	}
	for k, h := range history {
		if want := strings.TrimSuffix(h[0], "!"); latest[k] != want {
			t.Errorf("%s latest = %s, want %s", k, latest[k], want)
		}
	}
}

func TestVersionIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newVersionID()
		if !ValidVersionID(id) || seen[id] {
			t.Fatalf("bad or repeated id %q", id)
		}
		seen[id] = true
	}
	if !ValidVersionID(NullVersion) || ValidVersionID("") || ValidVersionID(filepath.Join("..", "x")) {
		t.Error("ValidVersionID accepts the wrong things")
	}
}
