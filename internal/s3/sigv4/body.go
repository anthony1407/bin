package sigv4

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"

	"bin/internal/s3/s3err"
)

var (
	errBadChunk       = s3err.ErrInvalidRequest.WithMessage("Invalid aws-chunked encoding")
	errMissingTrailer = s3err.ErrInvalidRequest.WithMessage(
		"The request declared a trailer in x-amz-trailer that was not sent")
	errDecodedLength = s3err.ErrIncompleteBody.WithMessage(
		"The decoded body does not match x-amz-decoded-content-length")
)

// chunkSigner holds what is needed to verify chunk and trailer signatures,
// which chain from the request's seed signature.
type chunkSigner struct {
	key     []byte
	amzDate string
	scope   string
	seed    string
}

// wrapBody replaces r.Body with a reader that enforces the payload mode.
// signer is nil for presigned requests, which cannot use signed chunks.
func wrapBody(r *http.Request, payload string, signer *chunkSigner) error {
	switch payload {
	case UnsignedPayload:
		return nil
	case StreamingSigned, StreamingSignedTrailer, StreamingUnsignedTrailer:
	default:
		r.Body = &hashReader{body: r.Body, h: sha256.New(), want: payload}
		return nil
	}

	raw := r.Header.Get("X-Amz-Decoded-Content-Length")
	if raw == "" {
		return s3err.ErrMissingContentLength
	}
	decoded, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || decoded < 0 {
		return s3err.ErrInvalidArgument.WithMessage("x-amz-decoded-content-length must be a non-negative integer")
	}
	cr := &chunkedReader{
		body:     r.Body,
		br:       bufio.NewReaderSize(r.Body, 4096),
		want:     decoded,
		trailers: payload != StreamingSigned,
		trailer:  http.Header{},
	}
	for _, name := range strings.Split(r.Header.Get("X-Amz-Trailer"), ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			cr.declared = append(cr.declared, name)
		}
	}
	if payload != StreamingUnsignedTrailer {
		if signer == nil {
			return s3err.ErrInvalidRequest.WithMessage("Signed streaming payloads require header authentication")
		}
		cr.signer = signer
		cr.prevSig = signer.seed
		cr.hash = sha256.New()
	}

	r.Body = cr
	r.ContentLength = decoded
	r.Trailer = cr.trailer
	r.Header.Del("X-Amz-Decoded-Content-Length")
	// aws-chunked is a transfer detail, not part of the stored object.
	if enc := removeToken(r.Header.Get("Content-Encoding"), "aws-chunked"); enc != "" {
		r.Header.Set("Content-Encoding", enc)
	} else {
		r.Header.Del("Content-Encoding")
	}
	return nil
}

func removeToken(list, token string) string {
	var keep []string
	for _, t := range strings.Split(list, ",") {
		if t = strings.TrimSpace(t); t != "" && !strings.EqualFold(t, token) {
			keep = append(keep, t)
		}
	}
	return strings.Join(keep, ",")
}

// hashReader checks the body against a hex SHA-256 once it reaches EOF.
type hashReader struct {
	body io.ReadCloser
	h    hash.Hash
	want string
}

func (r *hashReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.h.Write(p[:n])
	if err == io.EOF && hex.EncodeToString(r.h.Sum(nil)) != r.want {
		return n, s3err.ErrContentSHA256Mismatch
	}
	return n, err
}

func (r *hashReader) Close() error { return r.body.Close() }

// chunkedReader decodes an aws-chunked body:
//
//	<hex-size>[;chunk-signature=<sig>]\r\n<data>\r\n ... 0[;chunk-signature=<sig>]\r\n
//	[<trailer-name>:<value>\r\n ...][x-amz-trailer-signature:<sig>\r\n]\r\n
//
// Data is handed to the caller before its chunk signature can be checked; the
// error surfaces when the chunk ends, so callers must not commit anything
// until they read the body to io.EOF.
type chunkedReader struct {
	body     io.ReadCloser
	br       *bufio.Reader
	signer   *chunkSigner // nil: chunks are unsigned
	prevSig  string
	hash     hash.Hash
	trailers bool     // a trailer section may follow the last chunk
	declared []string // names announced in x-amz-trailer
	trailer  http.Header

	left     int64 // bytes left in the current chunk
	chunkSig string
	decoded  int64
	want     int64
	err      error
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	for c.err == nil && c.left == 0 {
		c.err = c.nextChunk()
	}
	if c.err != nil {
		return 0, c.err
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.br.Read(p)
	c.left -= int64(n)
	c.decoded += int64(n)
	if c.hash != nil {
		c.hash.Write(p[:n])
	}
	switch {
	case c.left == 0:
		c.err = c.endChunk()
	case err == io.EOF:
		c.err = s3err.ErrIncompleteBody
	case err != nil:
		c.err = err
	}
	return n, c.err
}

func (c *chunkedReader) Close() error { return c.body.Close() }

func (c *chunkedReader) readLine() (string, error) {
	line, err := c.br.ReadSlice('\n')
	if err == io.EOF {
		if len(line) == 0 {
			return "", io.EOF
		}
		return "", s3err.ErrIncompleteBody
	}
	if err != nil {
		return "", errBadChunk
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), nil
}

func (c *chunkedReader) nextChunk() error {
	line, err := c.readLine()
	if err == io.EOF {
		return s3err.ErrIncompleteBody
	}
	if err != nil {
		return err
	}
	sizeHex, ext, _ := strings.Cut(line, ";")
	size, err := strconv.ParseInt(strings.TrimSpace(sizeHex), 16, 64)
	if err != nil || size < 0 {
		return errBadChunk
	}
	if c.decoded+size > c.want {
		return errDecodedLength
	}
	if c.signer != nil {
		sig, ok := strings.CutPrefix(strings.TrimSpace(ext), "chunk-signature=")
		if !ok || sig == "" {
			return errBadChunk
		}
		c.chunkSig = sig
		c.hash.Reset()
	}
	if size == 0 {
		if c.signer != nil {
			if err := c.verifyChunk(emptySHA256); err != nil {
				return err
			}
		}
		return c.finish()
	}
	c.left = size
	return nil
}

// endChunk consumes the CRLF after a chunk's data and checks its signature.
// Only finish returns io.EOF, once the whole body has been validated.
func (c *chunkedReader) endChunk() error {
	line, err := c.readLine()
	if err != nil || line != "" {
		return errBadChunk
	}
	if c.signer != nil {
		return c.verifyChunk(hex.EncodeToString(c.hash.Sum(nil)))
	}
	return nil
}

func (c *chunkedReader) verifyChunk(dataHash string) error {
	s := c.signer
	sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + s.amzDate + "\n" + s.scope + "\n" +
		c.prevSig + "\n" + emptySHA256 + "\n" + dataHash
	if !hmac.Equal([]byte(hex.EncodeToString(hmacSHA256(s.key, sts))), []byte(c.chunkSig)) {
		return s3err.ErrSignatureDoesNotMatch
	}
	c.prevSig = c.chunkSig
	return nil
}

// finish reads the trailer section after the final chunk and validates it.
func (c *chunkedReader) finish() error {
	var canonical strings.Builder
	trailerSig := ""
	for {
		line, err := c.readLine()
		if err == io.EOF || (err == nil && line == "") {
			break // some clients omit the closing CRLF
		}
		if err != nil {
			return err
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if !ok || !c.trailers {
			return errBadChunk
		}
		if name == "x-amz-trailer-signature" {
			trailerSig = value
			continue
		}
		c.trailer.Set(name, value)
		canonical.WriteString(name + ":" + value + "\n")
	}
	for _, name := range c.declared {
		if c.trailer.Get(name) == "" {
			return errMissingTrailer
		}
	}
	if c.signer != nil && c.trailers {
		s := c.signer
		sts := "AWS4-HMAC-SHA256-TRAILER\n" + s.amzDate + "\n" + s.scope + "\n" +
			c.prevSig + "\n" + hexSHA256([]byte(canonical.String()))
		want := hex.EncodeToString(hmacSHA256(s.key, sts))
		if !hmac.Equal([]byte(want), []byte(trailerSig)) {
			return s3err.ErrSignatureDoesNotMatch
		}
	}
	if c.decoded != c.want {
		return errDecodedLength
	}
	return io.EOF
}
