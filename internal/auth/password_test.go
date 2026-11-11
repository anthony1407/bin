package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	const pw = "Correct horse 8!"
	h, err := HashPassword(pw)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("hash: %q %v", h, err)
	}
	if h2, _ := HashPassword(pw); h2 == h {
		t.Error("two hashes of one password are equal: no salt")
	}
	if !CheckPassword(h, pw) {
		t.Error("the right password is refused")
	}
	for _, wrong := range []string{"", "Correct horse 8", "correct horse 8!"} {
		if CheckPassword(h, wrong) {
			t.Errorf("%q accepted", wrong)
		}
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=999999999,t=1,p=1$c2FsdA$aGFzaA", "$bcrypt$x"} {
		if CheckPassword(bad, "x") {
			t.Errorf("stored %q matched", bad)
		}
	}
}

// At least 6 characters, with an uppercase and a lowercase letter, a digit
// and a special character.
func TestPasswordPolicy(t *testing.T) {
	for pw, ok := range map[string]bool{
		"Ab1!xy":   true,  // exactly 6, every class
		"Ñandú-9z": true,  // letters of any script count
		"Ab1 xy":   true,  // a space is a special character
		"Ab1!x":    false, // 5 characters
		"ab1!xy":   false, // no uppercase
		"AB1!XY":   false, // no lowercase
		"Abc!xy":   false, // no digit
		"Ab12xy":   false, // no special character
		"":         false,
		strings.Repeat("Ab1!", MaxPasswordLength/4+1): false, // too long
	} {
		if err := CheckPasswordPolicy(pw); (err == nil) != ok {
			t.Errorf("%q: %v, want ok=%v", pw, err, ok)
		}
		if _, err := HashPassword(pw); (err == nil) != ok {
			t.Errorf("hash %q: %v, want ok=%v", pw, err, ok)
		}
	}
}
