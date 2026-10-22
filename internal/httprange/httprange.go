// Package httprange parses single-range HTTP Range headers, as S3 and the
// console's download endpoint accept them.
package httprange

import (
	"strconv"
	"strings"
)

// Parse interprets a "bytes=" header for a resource of size bytes. ok is false
// when the header should be ignored (bad syntax, several ranges); satisfiable
// is false when the range lies outside the resource.
func Parse(h string, size int64) (start, length int64, ok, satisfiable bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if first == "" { // suffix: the last N bytes
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, true, false
		}
		if n > size {
			n = size
		}
		return size - n, n, true, true
	}
	a, err := strconv.ParseInt(first, 10, 64)
	if err != nil || a < 0 {
		return 0, 0, false, false
	}
	b := size - 1
	if last != "" {
		if b, err = strconv.ParseInt(last, 10, 64); err != nil || b < a {
			return 0, 0, false, false
		}
	}
	if a >= size {
		return 0, 0, true, false
	}
	if b >= size {
		b = size - 1
	}
	return a, b - a + 1, true, true
}

// ContentRange renders the Content-Range value for a satisfied range.
func ContentRange(start, length, size int64) string {
	return "bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(start+length-1, 10) + "/" + strconv.FormatInt(size, 10)
}
