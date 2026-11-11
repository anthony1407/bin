package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238), the codes authenticator apps
// show: HMAC-SHA1, 30-second steps, 6 digits, the parameters every app
// supports. A code from the step before or after is accepted too, for clocks
// that drift, but each step works only once (see VerifyTOTP).

const (
	totpPeriod = 30
	totpDigits = 6
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret in base32, the form apps take.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return totpEncoding.EncodeToString(b)
}

// TOTPURL is the otpauth:// address an authenticator app reads (typed in or
// as a QR code): it names the account and the issuer the app shows.
func TOTPURL(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"},
		"digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpPeriod)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpCode computes the code of one time step.
func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000)
}

// TOTPCodeAt is the code an authenticator app shows for secret at t (tests
// and tools use it; sign-in goes through VerifyTOTP).
func TOTPCodeAt(secret string, t time.Time) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	return totpCode(key, t.Unix()/totpPeriod), nil
}

// VerifyTOTP checks a code typed at now against secret. It accepts the
// current step and its neighbours, but only steps after lastStep, so a code
// seen over someone's shoulder cannot be used again. It returns the step that
// matched, to remember as the new lastStep.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return 0, false
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
