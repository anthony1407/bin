package sigv4

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Presign returns rawURL signed for method with query-string authentication,
// valid for expires (at most 7 days). Existing query parameters (such as
// response-content-disposition) are kept and covered by the signature.
func Presign(method, rawURL, accessKey, secretKey, region string, expires time.Duration, now time.Time) (string, error) {
	if expires <= 0 || expires > maxPresignExpiry {
		return "", fmt.Errorf("presigned URLs must expire within %s", maxPresignExpiry)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	now = now.UTC()
	amzDate := now.Format(amzDateForm)
	cred := credential{accessKey: accessKey, date: amzDate[:8], region: region}

	q := u.Query()
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", accessKey+"/"+cred.scope())
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	// Percent-encode with the canonical rules (spaces as %20, never "+") so the
	// URL the client sends is exactly what was signed.
	var pairs []string
	for k, values := range q {
		for _, v := range values {
			pairs = append(pairs, encode(k, true)+"="+encode(v, true))
		}
	}
	u.RawQuery = canonicalQuery(strings.Join(pairs, "&"), true)

	r := &http.Request{Method: method, URL: u, Host: u.Host, Header: http.Header{}}
	creq := canonicalRequest(r, canonicalURI(u.Path), u.RawQuery, []string{"host"}, UnsignedPayload)
	key := signingKey(secretKey, cred.date, region)
	sig := hex.EncodeToString(hmacSHA256(key, stringToSign(amzDate, cred.scope(), creq)))
	u.RawQuery += "&X-Amz-Signature=" + sig
	u.RawPath = canonicalURI(u.Path)
	return u.String(), nil
}
