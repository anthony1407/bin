package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"bin/internal/secrets"
	"bin/internal/storage"
)

// Keyring resolves access keys: the root key from the configuration and the
// keys stored (encrypted) in the database. It implements sigv4.SecretStore.
type Keyring struct {
	RootAccessKey string
	RootSecretKey string
	Store         *storage.Store
	Box           *secrets.Box
	Log           *slog.Logger
	Now           func() time.Time // nil: time.Now

	mu      sync.Mutex
	touched map[string]time.Time
}

const touchInterval = time.Minute

func (k *Keyring) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// Secret returns the secret of an enabled, unexpired access key.
func (k *Keyring) Secret(accessKey string) (string, bool) {
	if accessKey == k.RootAccessKey {
		return k.RootSecretKey, true
	}
	ctx := context.Background()
	key, err := k.Store.GetAccessKey(ctx, accessKey)
	if err != nil || !key.Enabled || (!key.Expires.IsZero() && k.now().After(key.Expires)) {
		return "", false
	}
	secret, err := k.Box.Decrypt(key.SecretEnc)
	if err != nil {
		if k.Log != nil {
			k.Log.Error("cannot decrypt access key secret; was BIN_MASTER_KEY changed?", "access_key", accessKey)
		}
		return "", false
	}
	k.touch(ctx, accessKey)
	return secret, true
}

// touch records the last use of a key, at most once per touchInterval.
func (k *Keyring) touch(ctx context.Context, accessKey string) {
	now := k.now()
	k.mu.Lock()
	if k.touched == nil {
		k.touched = map[string]time.Time{}
	}
	last := k.touched[accessKey]
	if now.Sub(last) < touchInterval {
		k.mu.Unlock()
		return
	}
	k.touched[accessKey] = now
	k.mu.Unlock()
	k.Store.TouchAccessKey(ctx, accessKey, now)
}

// Grants returns what accessKey may do; root has full access. A key that
// belongs to groups gets the scopes of its enabled groups instead of its own.
func (k *Keyring) Grants(ctx context.Context, accessKey string) (Grants, error) {
	if accessKey == k.RootAccessKey {
		return FullAccess, nil
	}
	key, err := k.Store.GetAccessKey(ctx, accessKey)
	if err != nil {
		return nil, err
	}
	groups, err := k.Store.KeyGroups(ctx, accessKey)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		own, err := ParseScope(key.Scope)
		return Grants{own}, err
	}
	grants := Grants{}
	for _, g := range groups {
		if !g.Enabled {
			continue
		}
		sc, err := ParseScope(g.Scope)
		if err != nil {
			return nil, err
		}
		grants = append(grants, sc)
	}
	return grants, nil
}

// --- key generation ---------------------------------------------------------------

const (
	accessKeyChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	secretKeyChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

func randomString(alphabet string, n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, _ := rand.Int(rand.Reader, max)
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}

// NewCredentials returns a fresh access key (20 characters) and secret (40).
func NewCredentials() (accessKey, secretKey string) {
	return "BIN" + randomString(accessKeyChars, 17), randomString(secretKeyChars, 40)
}

// --- share-link key -----------------------------------------------------------------

// shareKeySetting names the setting holding the internal share-link key.
const shareKeySetting = "share_access_key"

// ShareCredentials returns the internal, read-only key that signs share
// links, creating it on first use.
func (k *Keyring) ShareCredentials(ctx context.Context) (accessKey, secretKey string, err error) {
	if ak, ok, err := k.Store.Setting(ctx, shareKeySetting); err != nil {
		return "", "", err
	} else if ok {
		if secret, ok := k.Secret(ak); ok {
			return ak, secret, nil
		}
	}
	return k.RotateShareKey(ctx)
}

// RotateShareKey replaces the share-link key, which invalidates every share
// link issued so far.
func (k *Keyring) RotateShareKey(ctx context.Context) (accessKey, secretKey string, err error) {
	if old, ok, _ := k.Store.Setting(ctx, shareKeySetting); ok {
		if err := k.Store.DeleteAccessKey(ctx, old); err != nil && !errors.Is(err, storage.ErrNoSuchAccessKey) {
			return "", "", err
		}
	}
	accessKey, secretKey = NewCredentials()
	err = k.Store.CreateAccessKey(ctx, storage.AccessKey{
		AccessKey: accessKey,
		SecretEnc: k.Box.Encrypt(secretKey),
		Name:      "share links",
		Enabled:   true,
		Internal:  true,
		Scope:     Scope{ReadOnly: true}.String(),
	})
	if err != nil {
		return "", "", err
	}
	return accessKey, secretKey, k.Store.SetSetting(ctx, shareKeySetting, accessKey)
}

// --- master key check ---------------------------------------------------------------

const masterCheckSetting = "master_key_check"

// CheckMasterKey makes sure box can decrypt what earlier runs encrypted. On
// first use it stores a check value; afterwards a different master key is
// reported instead of silently failing every stored secret.
func CheckMasterKey(ctx context.Context, store *storage.Store, box *secrets.Box) error {
	stored, ok, err := store.Setting(ctx, masterCheckSetting)
	if err != nil {
		return err
	}
	if !ok {
		return store.SetSetting(ctx, masterCheckSetting, box.Encrypt("bin master key check"))
	}
	if _, err := box.Decrypt(stored); err != nil {
		return fmt.Errorf("the master key does not match the one used to encrypt the stored secrets " +
			"(set BIN_MASTER_KEY back to its previous value, or restore .bin/master.key)")
	}
	return nil
}
