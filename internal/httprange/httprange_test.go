package httprange

import "testing"

func TestParse(t *testing.T) {
	type res struct {
		start, length   int64
		ok, satisfiable bool
	}
	for h, want := range map[string]res{
		"bytes=2-5":     {2, 4, true, true},
		"bytes=7-":      {7, 3, true, true},
		"bytes=-3":      {7, 3, true, true},
		"bytes=-30":     {0, 10, true, true},
		"bytes=8-100":   {8, 2, true, true},
		"bytes=10-":     {0, 0, true, false},
		"bytes=-0":      {0, 0, true, false},
		"bytes=1-2,4-5": {0, 0, false, false},
		"items=0-1":     {0, 0, false, false},
		"bytes=5-2":     {0, 0, false, false},
		"bytes=abc":     {0, 0, false, false},
	} {
		s, l, ok, sat := Parse(h, 10)
		if (res{s, l, ok, sat}) != want {
			t.Errorf("%s: got %d,%d,%v,%v want %+v", h, s, l, ok, sat, want)
		}
	}
	if got := ContentRange(2, 4, 10); got != "bytes 2-5/10" {
		t.Errorf("ContentRange = %q", got)
	}
}
