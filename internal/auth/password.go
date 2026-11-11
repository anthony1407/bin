package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Console passwords of users are stored as argon2id hashes in the PHC string
// format ("$argon2id$v=19$m=...,t=...,p=...$salt$hash"), so the parameters
// can be raised later without breaking stored hashes. They are separate from
// the S3 secret key: changing one never touches the other.

// Parameters follow the OWASP recommendation for argon2id (19 MiB, 2 passes).
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// MinPasswordLength and MaxPasswordLength bound console passwords, in characters.
const (
	MinPasswordLength = 10
	MaxPasswordLength = 256
)

// ErrWeakPassword is returned for passwords outside the length bounds.
var ErrWeakPassword = fmt.Errorf("use %d to %d characters", MinPasswordLength, MaxPasswordLength)

// CheckPasswordPolicy reports whether a new password is acceptable.
func CheckPasswordPolicy(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength || !utf8.ValidString(password) {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns the stored form of a console password.
func HashPassword(password string) (string, error) {
	if err := CheckPasswordPolicy(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(sum)), nil
}

var errBadHash = errors.New("unrecognized password hash")

// CheckPassword reports whether password matches a stored hash. An empty or
// unreadable hash never matches.
func CheckPassword(stored, password string) bool {
	ok, err := checkPassword(stored, password)
	return err == nil && ok
}

func checkPassword(stored, password string) (bool, error) {
	parts := strings.Split(stored, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		memory == 0 || memory > 1<<20 || time == 0 || time > 16 || threads == 0 {
		return false, errBadHash // also refuses parameters that would exhaust the server
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 64 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyPasswordCheck spends the time of a real check, so a sign-in for a key
// without a password (or that does not exist) is not told apart by timing.
func DummyPasswordCheck(password string) {
	argon2.IDKey([]byte(password), make([]byte, argonSaltLen), argonTime, argonMemory, argonThreads, argonKeyLen)
}
