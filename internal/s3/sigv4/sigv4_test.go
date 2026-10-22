package sigv4

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bin/internal/s3/s3err"
)

// Test vectors from the AWS S3 SigV4 documentation.
const (
	exampleAK     = "AKIAIOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func exampleVerifier(now time.Time) *Verifier {
	return &Verifier{
		Region: "us-east-1",
		Store:  StaticStore{exampleAK: exampleSecret},
		Now:    func() time.Time { return now },
	}
}

func TestHeaderAuthAWSExample(t *testing.T) {
	r := httptest.NewRequest("GET", "/test.txt", nil)
	r.Host = "examplebucket.s3.amazonaws.com"
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,"+
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,"+
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")

	auth, err := exampleVerifier(exampleTime).Verify(r)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessKey != exampleAK {
		t.Errorf("access key = %q", auth.AccessKey)
	}
}

func presignedExample() *http.Request {
	return httptest.NewRequest("GET", "/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256"+
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request"+
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host"+
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404", nil)
}

func TestPresignedAWSExample(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want error
	}{
		{"valid", exampleTime.Add(time.Hour), nil},
		{"expired", exampleTime.Add(86401 * time.Second), errExpired},
		{"not yet valid", exampleTime.Add(-time.Hour), errNotValidYet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := presignedExample()
			r.Host = "examplebucket.s3.amazonaws.com"
			_, err := exampleVerifier(tc.now).Verify(r)
			if tc.want == nil && err != nil {
				t.Fatal(err)
			}
			if tc.want != nil && (err == nil || err.Error() != tc.want.Error()) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPresignedTampered(t *testing.T) {
	r := presignedExample()
	r.Host = "examplebucket.s3.amazonaws.com"
	r.URL.Path = "/other.txt"
	if _, err := exampleVerifier(exampleTime).Verify(r); !errors.Is(err, s3err.ErrSignatureDoesNotMatch) {
		t.Fatalf("err = %v", err)
	}
}

// chunkedExample builds the AWS "PUT Object in chunks" example request.
func chunkedExample(firstChunk []byte) *http.Request {
	var body strings.Builder
	body.WriteString("10000;chunk-signature=ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648\r\n")
	body.Write(firstChunk)
	body.WriteString("\r\n400;chunk-signature=0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497\r\n")
	body.WriteString(strings.Repeat("a", 1024))
	body.WriteString("\r\n0;chunk-signature=b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9\r\n\r\n")

	r := httptest.NewRequest("PUT", "/examplebucket/chunkObject.txt", strings.NewReader(body.String()))
	r.Host = "s3.amazonaws.com"
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	r.Header.Set("X-Amz-Content-Sha256", StreamingSigned)
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Decoded-Content-Length", "66560")
	r.Header.Set("Content-Length", "66824")
	r.ContentLength = 66824
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,"+
		"SignedHeaders=content-encoding;content-length;host;x-amz-content-sha256;x-amz-date;x-amz-decoded-content-length;x-amz-storage-class,"+
		"Signature=4f232c4386841ef735655705268965c44a0e4690baa4adea153f7db9fa80a0a9")
	return r
}

func TestChunkedAWSExample(t *testing.T) {
	r := chunkedExample([]byte(strings.Repeat("a", 65536)))
	if _, err := exampleVerifier(exampleTime).Verify(r); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 66560 || strings.Trim(string(got), "a") != "" {
		t.Errorf("decoded %d bytes", len(got))
	}
	if r.ContentLength != 66560 || r.Header.Get("Content-Encoding") != "" {
		t.Errorf("content-length %d, content-encoding %q", r.ContentLength, r.Header.Get("Content-Encoding"))
	}
}

func TestChunkedTamperedData(t *testing.T) {
	data := []byte(strings.Repeat("a", 65536))
	data[100] = 'b'
	r := chunkedExample(data)
	if _, err := exampleVerifier(exampleTime).Verify(r); err != nil {
		t.Fatal(err) // the seed signature only covers headers
	}
	if _, err := io.ReadAll(r.Body); !errors.Is(err, s3err.ErrSignatureDoesNotMatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestChunkedTruncated(t *testing.T) {
	r := chunkedExample([]byte(strings.Repeat("a", 65536)))
	full, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(strings.NewReader(string(full[:40000])))
	if _, err := exampleVerifier(exampleTime).Verify(r); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r.Body); !errors.Is(err, s3err.ErrIncompleteBody) {
		t.Fatalf("err = %v", err)
	}
}

// --- Requests signed by this package (for flows without public vectors) ---

const (
	testAK     = "refaccesskey"
	testSecret = "refsecretkey12345"
)

var testNow = time.Date(2026, 10, 22, 14, 4, 35, 0, time.UTC)

func testVerifier() *Verifier {
	return &Verifier{Region: "us-east-1", Store: StaticStore{testAK: testSecret}, Now: func() time.Time { return testNow }}
}

// sign adds a header signature to r covering the given headers.
func sign(r *http.Request, payload string, headers ...string) string {
	amzDate := testNow.Format(amzDateForm)
	r.Header.Set("X-Amz-Date", amzDate)
	r.Header.Set("X-Amz-Content-Sha256", payload)
	signed := append([]string{"host"}, headers...)
	signed = append(signed, "x-amz-content-sha256", "x-amz-date")
	cred := credential{accessKey: testAK, date: amzDate[:8], region: "us-east-1"}
	creq := canonicalRequest(r, canonicalURI(r.URL.Path), canonicalQuery(r.URL.RawQuery, false), signed, payload)
	sig := hexHMAC(signingKey(testSecret, cred.date, cred.region), stringToSign(amzDate, cred.scope(), creq))
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+testAK+"/"+cred.scope()+
		", SignedHeaders="+strings.Join(signed, ";")+", Signature="+sig)
	return sig
}

func hexHMAC(key []byte, s string) string {
	const digits = "0123456789abcdef"
	sum := hmacSHA256(key, s)
	out := make([]byte, len(sum)*2)
	for i, b := range sum {
		out[i*2], out[i*2+1] = digits[b>>4], digits[b&15]
	}
	return string(out)
}

func TestUnsignedTrailerLikeCRT(t *testing.T) {
	// Same framing aws-cli's CRT client uses for UploadPart: one chunk, then an
	// unsigned checksum trailer.
	data := strings.Repeat("x", 1000)
	body := "3e8\r\n" + data + "\r\n0\r\nx-amz-checksum-crc64nvme:OGYDL2FShCk=\r\n\r\n"
	r := httptest.NewRequest("PUT", "/compat/big.bin?partNumber=1&uploadId=abc", strings.NewReader(body))
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc64nvme")
	r.Header.Set("X-Amz-Decoded-Content-Length", "1000")
	sign(r, StreamingUnsignedTrailer, "content-encoding", "x-amz-decoded-content-length", "x-amz-trailer")

	if _, err := testVerifier().Verify(r); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Errorf("decoded %d bytes", len(got))
	}
	if v := r.Trailer.Get("x-amz-checksum-crc64nvme"); v != "OGYDL2FShCk=" {
		t.Errorf("trailer = %q", v)
	}
}

func TestUnsignedTrailerMissing(t *testing.T) {
	body := "3\r\nabc\r\n0\r\n\r\n"
	r := httptest.NewRequest("PUT", "/b/k", strings.NewReader(body))
	r.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32")
	r.Header.Set("X-Amz-Decoded-Content-Length", "3")
	sign(r, StreamingUnsignedTrailer, "x-amz-decoded-content-length", "x-amz-trailer")
	if _, err := testVerifier().Verify(r); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r.Body); !errors.Is(err, s3err.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestSignedTrailer(t *testing.T) {
	r := httptest.NewRequest("PUT", "/b/k", nil)
	r.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32")
	r.Header.Set("X-Amz-Decoded-Content-Length", "5")
	seed := sign(r, StreamingSignedTrailer, "x-amz-decoded-content-length", "x-amz-trailer")

	key := signingKey(testSecret, "20261022", "us-east-1")
	scope := "20261022/us-east-1/s3/aws4_request"
	chunkSig := func(prev, data string) string {
		return hexHMAC(key, "AWS4-HMAC-SHA256-PAYLOAD\n20261022T140435Z\n"+scope+"\n"+prev+"\n"+emptySHA256+"\n"+hexSHA256([]byte(data)))
	}
	s1 := chunkSig(seed, "hello")
	s2 := chunkSig(s1, "")
	trailer := "x-amz-checksum-crc32:NhCmhg==\n"
	ts := hexHMAC(key, "AWS4-HMAC-SHA256-TRAILER\n20261022T140435Z\n"+scope+"\n"+s2+"\n"+hexSHA256([]byte(trailer)))
	body := "5;chunk-signature=" + s1 + "\r\nhello\r\n0;chunk-signature=" + s2 + "\r\n" +
		"x-amz-checksum-crc32:NhCmhg==\r\nx-amz-trailer-signature:" + ts + "\r\n\r\n"
	r.Body = io.NopCloser(strings.NewReader(body))
	signedHeaders := r.Header.Clone() // Verify strips aws-chunked headers

	if _, err := testVerifier().Verify(r); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}

	// Tampering with the trailer value breaks the trailer signature.
	r2 := httptest.NewRequest("PUT", "/b/k", nil)
	r2.Header = signedHeaders
	r2.Body = io.NopCloser(strings.NewReader(strings.Replace(body, "NhCmhg==", "AAAAAA==", 1)))
	if _, err := testVerifier().Verify(r2); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r2.Body); !errors.Is(err, s3err.ErrSignatureDoesNotMatch) {
		t.Fatalf("tampered trailer: err = %v", err)
	}
}

func TestPayloadHash(t *testing.T) {
	body := "hello bin\n"
	good := "c130210fe5050a70e54b61d8b98adb0a33ddf851769db188c3819112c26de0d0" // sha256(body)
	for _, tc := range []struct {
		name, hash string
		want       error
	}{
		{"match", good, nil},
		{"mismatch", emptySHA256, s3err.ErrContentSHA256Mismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "/b/k", strings.NewReader(body))
			sign(r, tc.hash)
			if _, err := testVerifier().Verify(r); err != nil {
				t.Fatal(err)
			}
			_, err := io.ReadAll(r.Body)
			if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyErrors(t *testing.T) {
	cases := []struct {
		name   string
		modify func(r *http.Request)
		want   error
	}{
		{"wrong secret", func(r *http.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), "Signature=", "Signature=0", 1))
		}, s3err.ErrSignatureDoesNotMatch},
		{"unknown key", func(r *http.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), testAK, "nobody", 1))
		}, s3err.ErrInvalidAccessKeyID},
		{"wrong region", func(r *http.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), "us-east-1", "eu-west-1", 1))
		}, s3err.ErrAuthorizationHeaderMalformed},
		{"skewed clock", func(r *http.Request) {
			r.Header.Set("X-Amz-Date", testNow.Add(-20*time.Minute).Format(amzDateForm))
		}, s3err.ErrRequestTimeTooSkewed},
		{"missing content sha", func(r *http.Request) { r.Header.Del("X-Amz-Content-Sha256") }, errMissingContentSHA},
		{"sigv2", func(r *http.Request) { r.Header.Set("Authorization", "AWS refaccesskey:abc=") }, errUnsupportedAuth},
		{"garbage", func(r *http.Request) { r.Header.Set("Authorization", "AWS4-HMAC-SHA256 nonsense") }, s3err.ErrAuthorizationHeaderMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			sign(r, emptySHA256)
			tc.modify(r)
			_, err := testVerifier().Verify(r)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAnonymous(t *testing.T) {
	auth, err := testVerifier().Verify(httptest.NewRequest("GET", "/b/k", nil))
	if err != nil || !auth.Anonymous() {
		t.Fatalf("auth %+v, err %v", auth, err)
	}
}

func TestCanonicalQuery(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"uploads": "uploads=",
		"list-type=2&prefix=dir%2F&delimiter=%2F": "delimiter=%2F&list-type=2&prefix=dir%2F",
		"prefix=a+b&x-id=GetObject":               "prefix=a%2Bb&x-id=GetObject",
		"prefix=a%20b":                            "prefix=a%20b",
		"b=2&a=1&a=0":                             "a=0&a=1&b=2",
	}
	for in, want := range cases {
		if got := canonicalQuery(in, false); got != want {
			t.Errorf("canonicalQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalURI(t *testing.T) {
	cases := map[string]string{
		"":                                   "/",
		"/b/folder with spaces/ñandú +1.txt": "/b/folder%20with%20spaces/%C3%B1and%C3%BA%20%2B1.txt",
		"/b/a//b/../c":                       "/b/a//b/../c",
		"/b/~tilde_-.":                       "/b/~tilde_-.",
	}
	for in, want := range cases {
		if got := canonicalURI(in); got != want {
			t.Errorf("canonicalURI(%q) = %q, want %q", in, got, want)
		}
	}
}
