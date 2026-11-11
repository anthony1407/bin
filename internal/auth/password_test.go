package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("hash: %q %v", h, err)
	}
	if h2, _ := HashPassword("correct horse battery"); h2 == h {
		t.Error("two hashes of one password are equal: no salt")
	}
	if !CheckPassword(h, "correct horse battery") {
		t.Error("the right password is refused")
	}
	for _, wrong := range []string{"", "correct horse batter", "Correct horse battery"} {
		if CheckPassword(h, wrong) {
			t.Errorf("%q accepted", wrong)
		}
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=999999999,t=1,p=1$c2FsdA$aGFzaA", "$bcrypt$x"} {
		if CheckPassword(bad, "x") {
			t.Errorf("stored %q matched", bad)
		}
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("a short password was accepted")
	}
	if _, err := HashPassword(strings.Repeat("x", MaxPasswordLength+1)); err == nil {
		t.Error("a huge password was accepted")
	}
}
