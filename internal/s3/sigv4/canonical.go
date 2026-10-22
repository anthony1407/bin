package sigv4

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// canonicalRequest builds the SigV4 canonical request:
//
//	METHOD \n URI \n QUERY \n HEADERS \n SIGNED-HEADERS \n PAYLOAD-HASH
func canonicalRequest(r *http.Request, uri, query string, signedHeaders []string, payload string) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte('\n')
	b.WriteString(uri)
	b.WriteByte('\n')
	b.WriteString(query)
	b.WriteByte('\n')
	for _, name := range signedHeaders {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(headerValue(r, name))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(signedHeaders, ";"))
	b.WriteByte('\n')
	b.WriteString(payload)
	return b.String()
}

// canonicalURI encodes the decoded path once, keeping "/" literal. S3 does not
// normalize paths: "a//b/../c" is signed as is.
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return encode(path, false)
}

// canonicalQuery sorts parameters by name, then value, and encodes both.
// Values are unescaped with path rules, so a literal "+" stays a plus sign.
// For presigned URLs X-Amz-Signature is excluded.
func canonicalQuery(raw string, presigned bool) string {
	if raw == "" {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if dk, err := url.PathUnescape(k); err == nil {
			k = dk
		}
		if dv, err := url.PathUnescape(v); err == nil {
			v = dv
		}
		if presigned && k == "X-Amz-Signature" {
			continue
		}
		pairs = append(pairs, pair{encode(k, true), encode(v, true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.k)
		b.WriteByte('=')
		b.WriteString(p.v)
	}
	return b.String()
}

// headerValue returns the canonical value of a signed header: values trimmed,
// inner runs of spaces collapsed, multiple values joined with commas. Go moves
// Host and sometimes Content-Length out of the header map, so they are
// recovered from the request.
func headerValue(r *http.Request, name string) string {
	switch name {
	case "host":
		return r.Host
	case "content-length":
		if v := r.Header.Get("Content-Length"); v != "" {
			return v
		}
		if r.ContentLength >= 0 {
			return strconv.FormatInt(r.ContentLength, 10)
		}
	}
	values := r.Header.Values(name)
	trimmed := make([]string, len(values))
	for i, v := range values {
		trimmed[i] = strings.Join(strings.Fields(v), " ")
	}
	return strings.Join(trimmed, ",")
}

// encode percent-encodes every byte except the unreserved set A-Z a-z 0-9
// - _ . ~ (and "/" when encodeSlash is false), using uppercase hex.
func encode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}
