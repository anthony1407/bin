package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// The RFC 6238 test vectors (SHA1, 8 digits there; the last 6 digits here).
func TestTOTPRFCVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	key, _ := totpEncoding.DecodeString(secret)
	for unix, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037",
	} {
		if got := totpCode(key, unix/totpPeriod); got != want {
			t.Errorf("t=%d: %s, want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	key, _ := totpEncoding.DecodeString(secret)
	now := time.Unix(1_800_000_000, 0)
	step := now.Unix() / totpPeriod
	code := totpCode(key, step)

	got, ok := VerifyTOTP(secret, code, now, 0)
	if !ok || got != step {
		t.Fatalf("current code refused: %v %d", ok, got)
	}
	if _, ok := VerifyTOTP(secret, code, now, step); ok {
		t.Error("a code was accepted twice")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, step-1), now, 0); !ok {
		t.Error("the previous step's code (clock drift) was refused")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, step-3), now, 0); ok {
		t.Error("an old code was accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Error("a short code was accepted")
	}
	if u := TOTPURL(secret, "bin", "ana"); !strings.HasPrefix(u, "otpauth://totp/bin:ana?") || !strings.Contains(u, "secret="+secret) {
		t.Errorf("url: %s", u)
	}
}
