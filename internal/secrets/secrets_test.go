package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTripAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	enc := b.Encrypt("wJalrXUtnFEMI/K7MDENG")
	if enc == b.Encrypt("wJalrXUtnFEMI/K7MDENG") {
		t.Error("encryption must be randomized")
	}
	got, err := b.Decrypt(enc)
	if err != nil || got != "wJalrXUtnFEMI/K7MDENG" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Decrypt(enc); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong key: %v", err)
	}
	tampered := []byte(enc)
	tampered[len(tampered)-5] ^= 1
	if _, err := b.Decrypt(string(tampered)); !errors.Is(err, ErrWrongKey) {
		t.Errorf("tampered: %v", err)
	}
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "master.key")

	k1, created, err := LoadKey("", file)
	if err != nil || !created || len(k1) != 32 {
		t.Fatalf("generate: %v created=%v", err, created)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
			t.Errorf("key file mode %v", fi.Mode().Perm())
		}
	}
	k2, created, err := LoadKey("", file)
	if err != nil || created || !bytes.Equal(k1, k2) {
		t.Errorf("reload: %v created=%v same=%v", err, created, bytes.Equal(k1, k2))
	}

	p1, _, _ := LoadKey("correct horse battery staple", file)
	p2, _, _ := LoadKey("correct horse battery staple", file)
	p3, _, _ := LoadKey("another passphrase", file)
	if !bytes.Equal(p1, p2) || bytes.Equal(p1, p3) || bytes.Equal(p1, k1) {
		t.Error("passphrase derivation is not deterministic or not distinct")
	}

	os.WriteFile(file, []byte("not hex"), 0o600)
	if _, _, err := LoadKey("", file); err == nil {
		t.Error("corrupt key file accepted")
	}
}
