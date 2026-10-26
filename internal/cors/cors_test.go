package cors

import (
	"reflect"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestValidate(t *testing.T) {
	good := Rule{AllowedOrigins: []string{"https://*.example.com"}, AllowedMethods: []string{"GET", "PUT"},
		AllowedHeaders: []string{"*"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: intp(3000)}
	if err := Validate([]Rule{good}); err != nil {
		t.Errorf("valid rule rejected: %v", err)
	}
	tooMany := make([]Rule, MaxRules+1)
	for i := range tooMany {
		tooMany[i] = good
	}
	bad := map[string][]Rule{
		"no rules":          nil,
		"too many rules":    tooMany,
		"no origin":         {{AllowedMethods: []string{"GET"}}},
		"two wildcards":     {{AllowedOrigins: []string{"*.*.com"}, AllowedMethods: []string{"GET"}}},
		"no method":         {{AllowedOrigins: []string{"*"}}},
		"lower-case method": {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"get"}}},
		"PATCH":             {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"PATCH"}}},
		"header wildcards":  {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, AllowedHeaders: []string{"x-*-*"}}},
		"expose wildcard":   {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, ExposeHeaders: []string{"*"}}},
		"negative max age":  {{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, MaxAgeSeconds: intp(-1)}},
		"long id":           {{ID: strings.Repeat("x", 256), AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}}},
	}
	for name, rules := range bad {
		if err := Validate(rules); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMatch(t *testing.T) {
	rules := []Rule{
		{ID: "app", AllowedOrigins: []string{"https://app.example.com"}, AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"Content-Type", "x-amz-*"}},
		{ID: "subdomains", AllowedOrigins: []string{"https://*.example.com"}, AllowedMethods: []string{"GET"}},
		{ID: "anyone", AllowedOrigins: []string{"*"}, AllowedMethods: []string{"HEAD"}},
	}
	cases := []struct {
		origin, method string
		headers        []string
		want           string // rule ID, "" for no match
	}{
		{"https://app.example.com", "PUT", []string{"content-type", "x-amz-date"}, "app"},
		{"https://app.example.com", "PUT", []string{"authorization"}, ""},
		{"https://app.example.com", "GET", []string{"authorization"}, ""}, // app refuses the header, subdomains allows none
		{"https://app.example.com", "GET", nil, "app"},
		{"https://cdn.example.com", "GET", nil, "subdomains"},
		{"https://example.com", "GET", nil, ""}, // "*." needs a subdomain
		{"https://cdn.example.com", "DELETE", nil, ""},
		{"http://anywhere.test", "HEAD", nil, "anyone"},
		{"https://APP.example.com", "GET", nil, "subdomains"}, // origins compare exactly; the wildcard still matches
	}
	for _, c := range cases {
		r, ok := Match(rules, c.origin, c.method, c.headers)
		if got := map[bool]string{true: r.ID}[ok]; got != c.want {
			t.Errorf("Match(%s %s %v) = %q, want %q", c.method, c.origin, c.headers, got, c.want)
		}
	}
	if !rules[2].AnyOrigin() || rules[1].AnyOrigin() {
		t.Error("AnyOrigin")
	}
}

func TestRoundTripAndHeaders(t *testing.T) {
	rules := []Rule{{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, MaxAgeSeconds: intp(0)}}
	got, err := Parse(Encode(rules))
	if err != nil || !reflect.DeepEqual(got, rules) {
		t.Errorf("round trip = %+v, %v", got, err)
	}
	if Encode(nil) != "" {
		t.Error("no rules should encode as empty")
	}
	if got, _ := Parse(""); got != nil {
		t.Error("empty should parse as no rules")
	}
	if got := RequestHeaders(" Content-Type ,X-Amz-Date,, "); !reflect.DeepEqual(got, []string{"content-type", "x-amz-date"}) {
		t.Errorf("RequestHeaders = %v", got)
	}
}
