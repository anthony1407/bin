package auth

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bin/internal/secrets"
	"bin/internal/storage"
)

func TestScope(t *testing.T) {
	full := Scope{}
	ro := Scope{ReadOnly: true}
	photos := Scope{Buckets: []string{"photos"}}
	for _, tc := range []struct {
		s      Scope
		a      Action
		bucket string
		want   bool
	}{
		{full, ManageBucket, "x", true},
		{ro, ReadObject, "x", true},
		{ro, ListBucket, "x", true},
		{ro, WriteObject, "x", false},
		{ro, DeleteObject, "x", false},
		{ro, ManageBucket, "x", false},
		{photos, WriteObject, "photos", true},
		{photos, ReadObject, "backups", false},
		{photos, ListBuckets, "", true},
	} {
		if got := tc.s.Allows(tc.a, tc.bucket); got != tc.want {
			t.Errorf("%+v.Allows(%v, %q) = %v", tc.s, tc.a, tc.bucket, got)
		}
	}
	if full.String() != "" || ro.String() != `{"readOnly":true}` {
		t.Errorf("scope strings %q %q", full.String(), ro.String())
	}
	if s, _ := ParseScope(photos.String()); s.Buckets[0] != "photos" {
		t.Errorf("round trip %+v", s)
	}
}

func TestPolicy(t *testing.T) {
	public := Policy{Mode: "public"}
	custom := Policy{Mode: "custom", Rules: []Rule{
		{Prefix: "shared/", Access: ReadOnly},
		{Prefix: "inbox/", Access: WriteOnly},
		{Prefix: "scratch/", Access: ReadWrite},
	}}
	private := Policy{Mode: "private"}
	oneFile := Policy{Mode: "custom", Rules: []Rule{{Prefix: "photos/cat.jpg", Access: ReadOnly, Exact: true}}}
	for _, tc := range []struct {
		p        Policy
		a        Action
		resource string
		want     bool
	}{
		{private, ReadObject, "a", false},
		{public, ReadObject, "any/key", true},
		{public, ListBucket, "", true},
		{public, WriteObject, "a", false},
		{custom, ReadObject, "shared/x.jpg", true},
		{custom, ReadObject, "private/x.jpg", false},
		{custom, ListBucket, "shared/", true},
		{custom, ListBucket, "", false},
		{custom, WriteObject, "inbox/upload.bin", true},
		{custom, ReadObject, "inbox/upload.bin", false},
		{custom, DeleteObject, "inbox/upload.bin", false},
		{custom, DeleteObject, "scratch/tmp", true},
		{custom, BucketInfo, "", true},
		{custom, ManageBucket, "", false},
		{oneFile, ReadObject, "photos/cat.jpg", true},
		{oneFile, ReadObject, "photos/cat.jpg.bak", false}, // exact: not a prefix
		{oneFile, ReadObject, "photos/dog.jpg", false},
		{oneFile, ListBucket, "photos/cat.jpg", false},
		{oneFile, BucketInfo, "", false},
		{oneFile, WriteObject, "photos/cat.jpg", false},
	} {
		if got := tc.p.AllowsAnonymous(tc.a, tc.resource); got != tc.want {
			t.Errorf("%s %v %q = %v", tc.p.Mode, tc.a, tc.resource, got)
		}
	}
	for _, bad := range []Policy{{Mode: "open"}, {Mode: "custom"}, {Mode: "public", Rules: custom.Rules},
		{Mode: "custom", Rules: []Rule{{Prefix: "", Access: "everything"}}},
		{Mode: "custom", Rules: []Rule{{Prefix: "", Access: ReadOnly, Exact: true}}}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if p, err := ParsePolicy(""); err != nil || p.Mode != "private" || p.String() != "" {
		t.Errorf("empty policy: %+v %v", p, err)
	}
	if p, err := ParsePolicy(custom.String()); err != nil || len(p.Rules) != 3 {
		t.Errorf("round trip: %+v %v", p, err)
	}
}

func newKeyring(t *testing.T) (*Keyring, *storage.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	return &Keyring{RootAccessKey: "root", RootSecretKey: "rootsecret", Store: store, Box: box, Log: log}, store
}

func TestKeyring(t *testing.T) {
	ctx := context.Background()
	k, store := newKeyring(t)
	if s, ok := k.Secret("root"); !ok || s != "rootsecret" {
		t.Error("root key")
	}
	ak, sk := NewCredentials()
	if len(ak) != 20 || len(sk) != 40 {
		t.Errorf("credential lengths %d %d", len(ak), len(sk))
	}
	store.CreateAccessKey(ctx, storage.AccessKey{AccessKey: ak, SecretEnc: k.Box.Encrypt(sk), Enabled: true,
		Scope: Scope{ReadOnly: true}.String()})
	if s, ok := k.Secret(ak); !ok || s != sk {
		t.Error("stored key not resolved")
	}
	if key, _ := store.GetAccessKey(ctx, ak); key.LastUsed.IsZero() {
		t.Error("last use not recorded")
	}
	if g, _ := k.Grants(ctx, ak); g.Allows(WriteObject, "b") || !g.Allows(ReadObject, "b") {
		t.Errorf("grants %+v", g)
	}

	store.UpdateAccessKey(ctx, ak, func(a *storage.AccessKey) error { a.Enabled = false; return nil })
	if _, ok := k.Secret(ak); ok {
		t.Error("disabled key accepted")
	}
	store.UpdateAccessKey(ctx, ak, func(a *storage.AccessKey) error {
		a.Enabled, a.Expires = true, time.Now().Add(-time.Minute)
		return nil
	})
	if _, ok := k.Secret(ak); ok {
		t.Error("expired key accepted")
	}
	if _, ok := k.Secret("nobody"); ok {
		t.Error("unknown key accepted")
	}
}

func TestGroupGrants(t *testing.T) {
	ctx := context.Background()
	k, store := newKeyring(t)
	ak, sk := NewCredentials()
	// Own scope: full access, until the key joins a group.
	store.CreateAccessKey(ctx, storage.AccessKey{AccessKey: ak, SecretEnc: k.Box.Encrypt(sk), Enabled: true})
	if g, _ := k.Grants(ctx, ak); !g.Allows(ManageBucket, "any") {
		t.Fatalf("own scope: %+v", g)
	}

	store.CreateGroup(ctx, storage.Group{Name: "readers", Enabled: true,
		Scope: Scope{Buckets: []string{"photos"}, ReadOnly: true}.String(), Members: []string{ak}})
	store.CreateGroup(ctx, storage.Group{Name: "writers", Enabled: true,
		Scope: Scope{Buckets: []string{"inbox"}}.String(), Members: []string{ak}})
	g, err := k.Grants(ctx, ak)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a      Action
		bucket string
		want   bool
	}{
		{ReadObject, "photos", true},
		{WriteObject, "photos", false}, // readers is read-only
		{WriteObject, "inbox", true},   // writers grants it
		{ReadObject, "backups", false}, // the key's own full scope no longer applies
		{ListBuckets, "", true},
	} {
		if got := g.Allows(tc.a, tc.bucket); got != tc.want {
			t.Errorf("Allows(%v, %q) = %v", tc.a, tc.bucket, got)
		}
	}

	// Disabled groups grant nothing; a member of only disabled groups has no access.
	for _, name := range []string{"readers", "writers"} {
		store.UpdateGroup(ctx, name, func(g *storage.Group) error { g.Enabled = false; return nil })
	}
	if g, _ := k.Grants(ctx, ak); g.Allows(ReadObject, "photos") || g.Allows(ListBuckets, "") {
		t.Errorf("disabled groups still grant: %+v", g)
	}

	// Leaving every group restores the key's own scope.
	store.SetKeyGroups(ctx, ak, nil)
	if g, _ := k.Grants(ctx, ak); !g.Allows(ManageBucket, "any") {
		t.Errorf("own scope not restored: %+v", g)
	}
}

func TestShareKeyAndMasterCheck(t *testing.T) {
	ctx := context.Background()
	k, store := newKeyring(t)
	a1, s1, err := k.ShareCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a2, s2, _ := k.ShareCredentials(ctx); a2 != a1 || s2 != s1 {
		t.Error("share key must be stable until rotated")
	}
	if g, _ := k.Grants(ctx, a1); g.Allows(WriteObject, "b") {
		t.Error("share key must be read-only")
	}
	if list, _ := store.ListAccessKeys(ctx); len(list) != 0 {
		t.Error("share key must be hidden from listings")
	}
	a3, _, _ := k.RotateShareKey(ctx)
	if _, ok := k.Secret(a1); ok || a3 == a1 {
		t.Error("rotation must revoke the old share key")
	}

	if err := CheckMasterKey(ctx, store, k.Box); err != nil {
		t.Fatal(err)
	}
	if err := CheckMasterKey(ctx, store, k.Box); err != nil {
		t.Errorf("same key rejected: %v", err)
	}
	other, _ := secrets.New(bytes.Repeat([]byte{2}, 32))
	if err := CheckMasterKey(ctx, store, other); err == nil {
		t.Error("different master key accepted")
	}
}
