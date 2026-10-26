package storage

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func putKeys(t *testing.T, s *Store, bucket string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		put(t, s, bucket, k, "x", PutOptions{})
	}
}

// entries flattens a page into "key" and "prefix/" strings in listing order.
func entries(r ListResult) []string {
	var out []string
	for _, o := range r.Objects {
		out = append(out, o.Key)
	}
	out = append(out, r.CommonPrefixes...)
	sort.Strings(out)
	return out
}

// A folder counts every current object under it, nested ones included, and
// nothing from a sibling whose name merely starts the same.
func TestPrefixUsage(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "photos")
	putKeys(t, s, "photos", "2024/a.jpg", "2024/sub/b.jpg", "2024-old/c.jpg")
	got, err := s.PrefixUsage(ctx, "photos", []string{"2024/", "2024-old/", "none/"})
	if err != nil {
		t.Fatal(err)
	}
	if u := got["2024/"]; u.Objects != 2 || u.Bytes != 2 || u.Modified.IsZero() {
		t.Errorf("2024/: %+v", u)
	}
	if u := got["2024-old/"]; u.Objects != 1 || u.Bytes != 1 {
		t.Errorf("2024-old/: %+v", u)
	}
	if u := got["none/"]; u.Objects != 0 || u.Bytes != 0 || !u.Modified.IsZero() {
		t.Errorf("none/: %+v", u)
	}
}

func TestListBasics(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "photos")
	putKeys(t, s, "photos", "2024/a.jpg", "2024/b.jpg", "2024/sub/c.jpg", "2025/d.jpg", "readme.txt", "tmp/x", "zzz")

	cases := []struct {
		name string
		opts ListOptions
		want []string
		more bool
	}{
		{"all", ListOptions{MaxKeys: 1000}, []string{"2024/a.jpg", "2024/b.jpg", "2024/sub/c.jpg", "2025/d.jpg", "readme.txt", "tmp/x", "zzz"}, false},
		{"root folders", ListOptions{Delimiter: "/", MaxKeys: 1000}, []string{"2024/", "2025/", "readme.txt", "tmp/", "zzz"}, false},
		{"inside 2024", ListOptions{Prefix: "2024/", Delimiter: "/", MaxKeys: 1000}, []string{"2024/a.jpg", "2024/b.jpg", "2024/sub/"}, false},
		{"prefix without delimiter", ListOptions{Prefix: "2024/", MaxKeys: 1000}, []string{"2024/a.jpg", "2024/b.jpg", "2024/sub/c.jpg"}, false},
		{"partial prefix", ListOptions{Prefix: "20", Delimiter: "/", MaxKeys: 1000}, []string{"2024/", "2025/"}, false},
		{"page of 2", ListOptions{Delimiter: "/", MaxKeys: 2}, []string{"2024/", "2025/"}, true},
		{"after a folder", ListOptions{Delimiter: "/", After: "2024/", MaxKeys: 1000}, []string{"2025/", "readme.txt", "tmp/", "zzz"}, false},
		{"after a key inside a folder", ListOptions{Delimiter: "/", After: "2024/b.jpg", MaxKeys: 1000}, []string{"2025/", "readme.txt", "tmp/", "zzz"}, false},
		{"zero keys", ListOptions{MaxKeys: 0}, nil, false},
		{"no match", ListOptions{Prefix: "nothing", MaxKeys: 1000}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := s.ListObjects(ctx, "photos", tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := entries(r); !reflect.DeepEqual(got, tc.want) || r.IsTruncated != tc.more {
				t.Errorf("got %v truncated=%v, want %v truncated=%v", got, r.IsTruncated, tc.want, tc.more)
			}
		})
	}

	r, _ := s.ListObjects(ctx, "photos", ListOptions{MaxKeys: 1})
	if len(r.Objects) != 1 || r.Objects[0].ETag == "" || r.Objects[0].Size != 1 || r.Objects[0].ModTime.IsZero() {
		t.Errorf("object fields: %+v", r.Objects)
	}
	if _, err := s.ListObjects(ctx, "missing", ListOptions{MaxKeys: 10}); err != ErrNoSuchBucket {
		t.Errorf("missing bucket: %v", err)
	}
}

// naiveList is an obviously-correct reference: every entry of the listing,
// in order, computed from a sorted key list.
func naiveList(keys []string, prefix, delim string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if i := strings.Index(rest, delim); delim != "" && i >= 0 {
			cp := prefix + rest[:i+len(delim)]
			if !seen[cp] {
				seen[cp] = true
				out = append(out, cp)
			}
			continue
		}
		out = append(out, k)
	}
	return out
}

// TestListMatchesReference paginates random listings to the end and compares
// every page with the reference implementation, with tiny query batches so
// batch boundaries and prefix jumps interleave.
func TestListMatchesReference(t *testing.T) {
	old := listBatch
	listBatch = 3
	defer func() { listBatch = old }()

	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "fuzz")
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", "/", "|", "é", "-", maxRune}
	set := map[string]bool{}
	for len(set) < 400 {
		var b strings.Builder
		for n := 1 + rng.IntN(6); n > 0; n-- {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		set[b.String()] = true
	}
	var keys []string
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	putKeys(t, s, "fuzz", keys...)

	prefixes := []string{"", "a", "a/", "b/a", "é", "|", "a" + maxRune}
	delims := []string{"", "/", "|", "a/", "é", maxRune}
	for _, prefix := range prefixes {
		for _, delim := range delims {
			want := naiveList(keys, prefix, delim)
			for _, max := range []int{1, 2, 7, 1000} {
				name := fmt.Sprintf("prefix=%q delim=%q max=%d", prefix, delim, max)
				var got []string
				after := ""
				for page := 0; ; page++ {
					r, err := s.ListObjects(ctx, "fuzz", ListOptions{Prefix: prefix, Delimiter: delim, After: after, MaxKeys: max})
					if err != nil {
						t.Fatal(err)
					}
					e := entries(r)
					if len(e) > max {
						t.Fatalf("%s: page of %d entries", name, len(e))
					}
					got = append(got, e...)
					if !r.IsTruncated {
						break
					}
					if r.NextMarker == "" || page > len(keys) {
						t.Fatalf("%s: truncated page without a usable marker", name)
					}
					after = r.NextMarker
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s:\n got  %q\n want %q", name, got, want)
				}
			}
		}
	}
}
