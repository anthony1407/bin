// Package cors holds a bucket's CORS rules: which browser origins may call
// the S3 API, with which methods and headers. Rules are stored as JSON in the
// bucket row and evaluated the way S3 does: the first rule that matches the
// origin, the method and (for preflights) every requested header wins.
package cors

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Limits S3 applies to a CORS configuration.
const (
	MaxRules    = 100
	MaxIDLength = 255
)

// Methods are the methods a rule can allow.
var Methods = []string{"GET", "PUT", "POST", "DELETE", "HEAD"}

// Rule is one CORS rule. Origins and headers may hold one "*" wildcard each.
type Rule struct {
	ID             string   `json:"id,omitempty"`
	AllowedOrigins []string `json:"allowedOrigins"`
	AllowedMethods []string `json:"allowedMethods"`
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`
	ExposeHeaders  []string `json:"exposeHeaders,omitempty"`
	// MaxAgeSeconds is how long browsers may cache a preflight; nil sends no
	// Access-Control-Max-Age.
	MaxAgeSeconds *int `json:"maxAgeSeconds,omitempty"`
}

// Parse reads stored rules; "" means no CORS configuration.
func Parse(s string) ([]Rule, error) {
	if s == "" {
		return nil, nil
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(s), &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// Encode stores rules; no rules encode as "".
func Encode(rules []Rule) string {
	if len(rules) == 0 {
		return ""
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

// Validate applies S3's rules for a CORS configuration. The error message is
// meant for the user.
func Validate(rules []Rule) error {
	if len(rules) == 0 {
		return errors.New("add at least one CORS rule")
	}
	if len(rules) > MaxRules {
		return fmt.Errorf("a CORS configuration can have at most %d rules", MaxRules)
	}
	for i, r := range rules {
		n := i + 1
		if len(r.ID) > MaxIDLength {
			return fmt.Errorf("rule %d: the ID can have at most %d characters", n, MaxIDLength)
		}
		if len(r.AllowedOrigins) == 0 {
			return fmt.Errorf("rule %d: add at least one allowed origin", n)
		}
		for _, o := range r.AllowedOrigins {
			if strings.TrimSpace(o) == "" {
				return fmt.Errorf("rule %d: an allowed origin is empty", n)
			}
			if strings.Count(o, "*") > 1 {
				return fmt.Errorf("rule %d: the origin %q can contain at most one * wildcard", n, o)
			}
		}
		if len(r.AllowedMethods) == 0 {
			return fmt.Errorf("rule %d: add at least one allowed method", n)
		}
		for _, m := range r.AllowedMethods {
			if !validMethod(m) {
				return fmt.Errorf("rule %d: unsupported method %q; use GET, PUT, POST, DELETE or HEAD", n, m)
			}
		}
		for _, h := range r.AllowedHeaders {
			if strings.TrimSpace(h) == "" {
				return fmt.Errorf("rule %d: an allowed header is empty", n)
			}
			if strings.Count(h, "*") > 1 {
				return fmt.Errorf("rule %d: the header %q can contain at most one * wildcard", n, h)
			}
		}
		for _, h := range r.ExposeHeaders {
			if strings.TrimSpace(h) == "" || strings.Contains(h, "*") {
				return fmt.Errorf("rule %d: expose headers must be header names, without wildcards", n)
			}
		}
		if r.MaxAgeSeconds != nil && *r.MaxAgeSeconds < 0 {
			return fmt.Errorf("rule %d: max age cannot be negative", n)
		}
	}
	return nil
}

func validMethod(m string) bool {
	for _, v := range Methods {
		if m == v {
			return true
		}
	}
	return false
}

// Match returns the first rule that allows origin to use method and send all
// of headers (lower-case names, from Access-Control-Request-Headers; nil for
// actual requests, whose headers S3 does not check).
func Match(rules []Rule, origin, method string, headers []string) (Rule, bool) {
	for _, r := range rules {
		if !r.allowsOrigin(origin) || !r.allowsMethod(method) {
			continue
		}
		ok := true
		for _, h := range headers {
			if !r.allowsHeader(h) {
				ok = false
				break
			}
		}
		if ok {
			return r, true
		}
	}
	return Rule{}, false
}

func (r Rule) allowsOrigin(origin string) bool {
	for _, p := range r.AllowedOrigins {
		if wildcardMatch(p, origin) {
			return true
		}
	}
	return false
}

func (r Rule) allowsMethod(method string) bool {
	for _, m := range r.AllowedMethods {
		if m == method {
			return true
		}
	}
	return false
}

func (r Rule) allowsHeader(header string) bool {
	for _, p := range r.AllowedHeaders {
		if wildcardMatch(strings.ToLower(p), strings.ToLower(header)) {
			return true
		}
	}
	return false
}

// AnyOrigin reports whether the rule admits every origin with a bare "*".
// Such responses say "Access-Control-Allow-Origin: *" and never allow
// credentials, as S3 does.
func (r Rule) AnyOrigin() bool {
	for _, o := range r.AllowedOrigins {
		if o == "*" {
			return true
		}
	}
	return false
}

// wildcardMatch matches s against a pattern with at most one "*", which
// stands for any run of characters (empty included).
func wildcardMatch(pattern, s string) bool {
	before, after, found := strings.Cut(pattern, "*")
	if !found {
		return pattern == s
	}
	return len(s) >= len(before)+len(after) && strings.HasPrefix(s, before) && strings.HasSuffix(s, after)
}

// RequestHeaders splits an Access-Control-Request-Headers value into
// lower-case header names.
func RequestHeaders(v string) []string {
	var out []string
	for _, h := range strings.Split(v, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out = append(out, h)
		}
	}
	return out
}
