// Package sigv4 authenticates S3 requests signed with AWS Signature Version 4,
// either in the Authorization header or in the query string (presigned URLs),
// and wraps the request body so its integrity is verified while it is read.
//
// Supported payload modes (x-amz-content-sha256):
//
//	<hex sha256>                                  body hash checked at EOF
//	UNSIGNED-PAYLOAD                              no body check
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD            aws-chunked, every chunk signed
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER    same, plus signed trailers
//	STREAMING-UNSIGNED-PAYLOAD-TRAILER            aws-chunked, unsigned trailers
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bin/internal/s3/s3err"
)

const (
	algorithm   = "AWS4-HMAC-SHA256"
	service     = "s3"
	terminator  = "aws4_request"
	amzDateForm = "20060102T150405Z"

	// MaxSkew is how far a request's timestamp may be from the server clock.
	MaxSkew = 15 * time.Minute
	// maxPresignExpiry is the longest validity a presigned URL may request.
	maxPresignExpiry = 7 * 24 * time.Hour

	UnsignedPayload          = "UNSIGNED-PAYLOAD"
	StreamingSigned          = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	StreamingSignedTrailer   = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	StreamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"

	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// SecretStore resolves the secret key of an access key.
type SecretStore interface {
	Secret(accessKey string) (secret string, ok bool)
}

// StaticStore is a fixed set of access key → secret key pairs.
type StaticStore map[string]string

func (s StaticStore) Secret(accessKey string) (string, bool) {
	secret, ok := s[accessKey]
	return secret, ok
}

// Verifier checks SigV4 signatures for one region.
type Verifier struct {
	Region string
	Store  SecretStore
	Now    func() time.Time // nil = time.Now
}

// Auth is the outcome of a successful verification.
type Auth struct {
	AccessKey string // empty for anonymous requests
}

// Anonymous reports whether the request carried no credentials.
func (a *Auth) Anonymous() bool { return a.AccessKey == "" }

var (
	errUnsupportedAuth = s3err.ErrInvalidRequest.WithMessage(
		"The authorization mechanism you have provided is not supported. Please use AWS4-HMAC-SHA256.")
	errMissingContentSHA = s3err.ErrInvalidRequest.WithMessage(
		"Missing required header for this request: x-amz-content-sha256")
	errExpired      = s3err.ErrAccessDenied.WithMessage("Request has expired")
	errNotValidYet  = s3err.ErrAccessDenied.WithMessage("Request is not valid yet")
	errBadAlgorithm = s3err.ErrAuthorizationQueryMalformed.WithMessage(
		"X-Amz-Algorithm only supports \"AWS4-HMAC-SHA256\"")
	errBadExpires = s3err.ErrAuthorizationQueryMalformed.WithMessage(
		"X-Amz-Expires must be a number of seconds between 1 and 604800")
)

// Verify authenticates r. On success it replaces r.Body with a reader that
// decodes aws-chunked bodies and verifies payload integrity; read errors from
// that body are *s3err.Error values. Requests without any credentials return
// an anonymous Auth, leaving authorization to the caller.
func (v *Verifier) Verify(r *http.Request) (*Auth, error) {
	authz := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(authz, algorithm+" "):
		return v.verifyHeader(r, authz)
	case authz != "":
		return nil, errUnsupportedAuth
	case r.URL.Query().Has("X-Amz-Credential"):
		return v.verifyQuery(r)
	default:
		return &Auth{}, nil
	}
}

// credential is the parsed Credential component: AK/date/region/s3/aws4_request.
type credential struct {
	accessKey, date, region string
}

func (c credential) scope() string {
	return c.date + "/" + c.region + "/" + service + "/" + terminator
}

func (v *Verifier) parseCredential(s string, malformed *s3err.Error) (credential, string, error) {
	parts := strings.Split(s, "/")
	if len(parts) < 5 {
		return credential{}, "", malformed
	}
	n := len(parts)
	c := credential{
		accessKey: strings.Join(parts[:n-4], "/"),
		date:      parts[n-4],
		region:    parts[n-3],
	}
	if c.accessKey == "" || parts[n-2] != service || parts[n-1] != terminator {
		return credential{}, "", malformed
	}
	if _, err := time.Parse("20060102", c.date); err != nil {
		return credential{}, "", malformed
	}
	if c.region != v.Region {
		return credential{}, "", malformed.WithMessage(
			"The authorization header is malformed; the region '" + c.region +
				"' is wrong; expecting '" + v.Region + "'")
	}
	secret, ok := v.Store.Secret(c.accessKey)
	if !ok {
		return credential{}, "", s3err.ErrInvalidAccessKeyID
	}
	return c, secret, nil
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) verifyHeader(r *http.Request, authz string) (*Auth, error) {
	malformed := s3err.ErrAuthorizationHeaderMalformed
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(authz, algorithm+" "), ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, malformed
		}
		fields[k] = val
	}
	cred, secret, err := v.parseCredential(fields["Credential"], malformed)
	if err != nil {
		return nil, err
	}
	signedHeaders := strings.Split(fields["SignedHeaders"], ";")
	signature := fields["Signature"]
	if len(signedHeaders) == 0 || signature == "" || !contains(signedHeaders, "host") {
		return nil, malformed
	}

	amzDate := r.Header.Get("X-Amz-Date")
	var t time.Time
	if amzDate != "" {
		t, err = time.Parse(amzDateForm, amzDate)
	} else if d := r.Header.Get("Date"); d != "" {
		t, err = http.ParseTime(d)
		amzDate = t.UTC().Format(amzDateForm)
	} else {
		err = s3err.ErrAccessDenied
	}
	if err != nil {
		return nil, s3err.ErrAccessDenied.WithMessage("AWS authentication requires a valid Date or x-amz-date header")
	}
	if amzDate[:8] != cred.date {
		return nil, malformed.WithMessage("The authorization header is malformed; Invalid credential date. Date is not the same as X-Amz-Date.")
	}
	if skew := v.now().Sub(t); skew > MaxSkew || skew < -MaxSkew {
		return nil, s3err.ErrRequestTimeTooSkewed
	}

	payload := r.Header.Get("X-Amz-Content-Sha256")
	if payload == "" {
		return nil, errMissingContentSHA
	}
	if !validPayloadMode(payload) {
		return nil, s3err.ErrInvalidArgument.WithMessage("x-amz-content-sha256 must be UNSIGNED-PAYLOAD, a STREAMING mode or a valid sha256 value.")
	}

	key := signingKey(secret, cred.date, cred.region)
	query := canonicalQuery(r.URL.RawQuery, false)
	if !signatureMatches(r, query, signedHeaders, payload, key, amzDate, cred.scope(), signature) {
		return nil, s3err.ErrSignatureDoesNotMatch
	}

	if err := wrapBody(r, payload, &chunkSigner{key: key, amzDate: amzDate, scope: cred.scope(), seed: signature}); err != nil {
		return nil, err
	}
	return &Auth{AccessKey: cred.accessKey}, nil
}

func (v *Verifier) verifyQuery(r *http.Request) (*Auth, error) {
	q := r.URL.Query()
	if q.Get("X-Amz-Algorithm") != algorithm {
		return nil, errBadAlgorithm
	}
	cred, secret, err := v.parseCredential(q.Get("X-Amz-Credential"), s3err.ErrAuthorizationQueryMalformed)
	if err != nil {
		return nil, err
	}
	amzDate := q.Get("X-Amz-Date")
	t, err := time.Parse(amzDateForm, amzDate)
	if err != nil || amzDate[:8] != cred.date {
		return nil, s3err.ErrAuthorizationQueryMalformed.WithMessage(
			"X-Amz-Date must be in the ISO8601 Long Format \"yyyyMMdd'T'HHmmss'Z'\" and match the credential date")
	}
	secs, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || secs < 1 || time.Duration(secs)*time.Second > maxPresignExpiry {
		return nil, errBadExpires
	}
	now := v.now()
	if t.Sub(now) > MaxSkew {
		return nil, errNotValidYet
	}
	if now.After(t.Add(time.Duration(secs) * time.Second)) {
		return nil, errExpired
	}
	signedHeaders := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	signature := q.Get("X-Amz-Signature")
	if signature == "" || !contains(signedHeaders, "host") {
		return nil, s3err.ErrAuthorizationQueryMalformed
	}
	payload := q.Get("X-Amz-Content-Sha256")
	if payload == "" {
		payload = UnsignedPayload
	}

	key := signingKey(secret, cred.date, cred.region)
	query := canonicalQuery(r.URL.RawQuery, true)
	if !signatureMatches(r, query, signedHeaders, payload, key, amzDate, cred.scope(), signature) {
		return nil, s3err.ErrSignatureDoesNotMatch
	}
	if payload != UnsignedPayload {
		if err := wrapBody(r, payload, nil); err != nil {
			return nil, err
		}
	}
	return &Auth{AccessKey: cred.accessKey}, nil
}

// signatureMatches checks the signature against the strictly re-encoded path
// first (what AWS SDKs sign) and then, if different, against the path exactly
// as the client sent it, for clients that leave characters like "!" or "("
// unescaped and sign them that way. The raw path is only trusted when it
// decodes to the same path the request is routed by.
func signatureMatches(r *http.Request, query string, signedHeaders []string, payload string,
	key []byte, amzDate, scope, signature string) bool {
	uris := []string{canonicalURI(r.URL.Path)}
	if raw, _, _ := strings.Cut(r.RequestURI, "?"); raw != uris[0] && strings.HasPrefix(raw, "/") {
		if decoded, err := url.PathUnescape(raw); err == nil && decoded == r.URL.Path {
			uris = append(uris, raw)
		}
	}
	for _, uri := range uris {
		creq := canonicalRequest(r, uri, query, signedHeaders, payload)
		want := hex.EncodeToString(hmacSHA256(key, stringToSign(amzDate, scope, creq)))
		if hmac.Equal([]byte(want), []byte(signature)) {
			return true
		}
	}
	return false
}

func validPayloadMode(p string) bool {
	switch p {
	case UnsignedPayload, StreamingSigned, StreamingSignedTrailer, StreamingUnsignedTrailer:
		return true
	}
	if len(p) != 64 {
		return false
	}
	_, err := hex.DecodeString(p)
	return err == nil
}

func stringToSign(amzDate, scope, canonicalRequest string) string {
	return algorithm + "\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonicalRequest))
}

func signingKey(secret, date, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, terminator)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
