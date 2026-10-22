// Package secrets encrypts values at rest (access key secrets) with a master
// key, using AES-256-GCM.
//
// The master key comes from BIN_MASTER_KEY when set (any string; it is hashed
// to 256 bits) or from <data>/.bin/master.key, generated on first start.
// Keeping it in the environment separates it from the data: a stolen copy of
// the data directory alone then reveals no secret.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrWrongKey means a value was encrypted with a different master key, or
// has been tampered with.
var ErrWrongKey = errors.New("value cannot be decrypted with this master key")

// Box encrypts and decrypts with one master key.
type Box struct {
	aead cipher.AEAD
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns base64(nonce || ciphertext || tag).
func (b *Box) Encrypt(plaintext string) string {
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed)
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", ErrWrongKey
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrWrongKey
	}
	return string(plain), nil
}

// LoadKey returns the master key: derived from passphrase when it is set,
// otherwise read from (or, the first time, generated into) keyFile. The
// boolean reports whether a new key file was created.
func LoadKey(passphrase, keyFile string) ([]byte, bool, error) {
	if passphrase != "" {
		sum := sha256.Sum256([]byte("bin master key\x00" + passphrase))
		return sum[:], false, nil
	}
	data, err := os.ReadFile(keyFile)
	if err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, false, fmt.Errorf("%s is not a valid master key file", keyFile)
		}
		return key, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	if err := writeDurable(keyFile, []byte(hex.EncodeToString(key)+"\n")); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// writeDurable creates path atomically (temp file, fsync, rename) with 0600.
func writeDurable(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".master.key-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
