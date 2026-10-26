package admin

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7/pkg/signer"
	"github.com/secure-io/sio-go"
	"golang.org/x/crypto/argon2"
)

// The import reads users, groups and policies through MinIO's admin API.
// Only the few calls it needs are implemented here, rather than pulling in
// MinIO's admin client and its dependencies.

var (
	errNoAdminAPI  = errors.New("the remote server does not offer the MinIO admin API")
	errAdminDenied = errors.New("the remote credentials may not read users and policies")
	errAdminData   = errors.New("unexpected answer from the MinIO admin API")
)

// maxAdminResponse bounds what one admin call may return.
const maxAdminResponse = 16 << 20

type minioAdmin struct {
	endpoint             *url.URL
	accessKey, secretKey string
	client               *http.Client
}

func newMinioAdmin(in importRemote) (*minioAdmin, error) {
	u, err := parseRemoteEndpoint(in.Endpoint)
	if err != nil {
		return nil, err
	}
	return &minioAdmin{
		endpoint: u, accessKey: in.AccessKey, secretKey: in.SecretKey,
		client: &http.Client{Transport: importTransport, Timeout: time.Minute},
	}, nil
}

var emptySHA256 = func() string { s := sha256.Sum256(nil); return hex.EncodeToString(s[:]) }()

// get calls one admin endpoint, signed like MinIO's own client does. It
// tries the v4 API first and falls back to v3, which older servers speak.
func (a *minioAdmin) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	var last error
	for _, version := range []string{"v4", "v3"} {
		u := *a.endpoint
		u.Path = "/minio/admin/" + version + path
		u.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Amz-Content-Sha256", emptySHA256)
		resp, err := a.client.Do(signer.SignV4(*req, a.accessKey, a.secretKey, "", ""))
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxAdminResponse))
		resp.Body.Close()
		switch {
		case err != nil:
			return nil, err
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case resp.StatusCode == http.StatusForbidden:
			return nil, errAdminDenied
		}
		last = fmt.Errorf("%w (HTTP %d on %s)", errNoAdminAPI, resp.StatusCode, version)
	}
	return nil, last
}

func (a *minioAdmin) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	body, err := a.get(ctx, path, query)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %v", errAdminData, err)
	}
	return nil
}

// minioUser is a MinIO user as list-users describes it; its secret is never sent.
type minioUser struct {
	PolicyName string   `json:"policyName"` // comma separated
	Status     string   `json:"status"`     // "enabled" or "disabled"
	MemberOf   []string `json:"memberOf"`
}

type minioGroup struct {
	Members []string `json:"members"`
	Policy  string   `json:"policy"` // comma separated
	Status  string   `json:"status"`
}

// minioServiceAccount is an access key of a MinIO user ("service account").
// With ImpliedPolicy it has its parent's access; otherwise Policy narrows it.
type minioServiceAccount struct {
	AccessKey     string     `json:"accessKey"`
	ParentUser    string     `json:"parentUser"`
	AccountStatus string     `json:"accountStatus"`
	ImpliedPolicy bool       `json:"impliedPolicy"`
	Policy        string     `json:"policy"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Expiration    *time.Time `json:"expiration"`
}

// remoteIAM is the identity configuration of a MinIO server.
type remoteIAM struct {
	Policies        map[string]json.RawMessage // name -> IAM policy document
	Users           map[string]minioUser
	Groups          map[string]minioGroup
	ServiceAccounts []minioServiceAccount
	// Caller is the access key the import connects with; when it is MinIO's
	// root user it is not in Users, but it may own service accounts.
	Caller string
}

// getEncrypted is get for the endpoints that encrypt their answer.
func (a *minioAdmin) getEncrypted(ctx context.Context, path string, query url.Values, out any) error {
	body, err := a.get(ctx, path, query)
	if err != nil {
		return err
	}
	plain, err := decryptAdminData(a.secretKey, body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(plain, out); err != nil {
		return fmt.Errorf("%w: %v", errAdminData, err)
	}
	return nil
}

func (a *minioAdmin) fetchIAM(ctx context.Context) (*remoteIAM, error) {
	iam := &remoteIAM{Groups: map[string]minioGroup{}, Caller: a.accessKey}
	if err := a.getJSON(ctx, "/list-canned-policies", nil, &iam.Policies); err != nil {
		return nil, err
	}
	if err := a.getEncrypted(ctx, "/list-users", nil, &iam.Users); err != nil {
		return nil, err
	}
	var groups []string
	if err := a.getJSON(ctx, "/groups", nil, &groups); err != nil {
		return nil, err
	}
	for _, name := range groups {
		var g minioGroup
		if err := a.getJSON(ctx, "/group", url.Values{"group": {name}}, &g); err != nil {
			return nil, err
		}
		iam.Groups[name] = g
	}
	owners := []string{a.accessKey}
	for name := range iam.Users {
		if name != a.accessKey {
			owners = append(owners, name)
		}
	}
	sort.Strings(owners[1:])
	for _, owner := range owners {
		var list struct {
			Accounts []minioServiceAccount `json:"accounts"`
		}
		if err := a.getEncrypted(ctx, "/list-service-accounts", url.Values{"user": {owner}}, &list); err != nil {
			return nil, err
		}
		for _, sa := range list.Accounts {
			if !sa.ImpliedPolicy {
				// The listing leaves the narrowing policy out.
				var info minioServiceAccount
				if err := a.getEncrypted(ctx, "/info-service-account", url.Values{"accessKey": {sa.AccessKey}}, &info); err != nil {
					return nil, err
				}
				sa.Policy = info.Policy
			}
			if sa.ParentUser == "" {
				sa.ParentUser = owner
			}
			iam.ServiceAccounts = append(iam.ServiceAccounts, sa)
		}
	}
	return iam, nil
}

// bucketQuota returns a bucket's hard quota in bytes; 0 means none.
func (a *minioAdmin) bucketQuota(ctx context.Context, bucket string) (int64, error) {
	var q struct {
		Quota int64 `json:"quota"` // older servers
		Size  int64 `json:"size"`
	}
	if err := a.getJSON(ctx, "/get-bucket-quota", url.Values{"bucket": {bucket}}, &q); err != nil {
		return 0, err
	}
	return max(q.Size, q.Quota), nil
}

// The admin API encrypts some answers: salt (32) | algorithm (1) | nonce (8)
// | sio stream, keyed by the caller's secret key.
const (
	adminArgon2idAESGCM   = 0x00
	adminArgon2idChaCha20 = 0x01
	adminPBKDF2AESGCM     = 0x02
)

// argon2Mu keeps the 64 MiB Argon2 derivation to one at a time.
var argon2Mu sync.Mutex

func decryptAdminData(password string, data []byte) ([]byte, error) {
	if len(data) < 32+1+8 {
		return nil, errAdminData
	}
	salt, id, nonce := data[:32], data[32], data[33:41]
	var key []byte
	switch id {
	case adminArgon2idAESGCM, adminArgon2idChaCha20:
		argon2Mu.Lock()
		key = argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
		argon2Mu.Unlock()
	case adminPBKDF2AESGCM:
		var err error
		if key, err = pbkdf2.Key(sha256.New, password, salt, 8192, 32); err != nil {
			return nil, err
		}
	default:
		return nil, errAdminData
	}
	cipher := sio.AES_256_GCM
	if id == adminArgon2idChaCha20 {
		cipher = sio.ChaCha20Poly1305
	}
	stream, err := cipher.Stream(key)
	if err != nil {
		return nil, err
	}
	plain, err := io.ReadAll(stream.DecryptReader(bytes.NewReader(data[41:]), nonce, nil))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAdminData, err)
	}
	return plain, nil
}

// splitPolicies parses MinIO's comma separated policy list.
func splitPolicies(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
