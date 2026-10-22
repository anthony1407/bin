package s3

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"bin/internal/checksum"
	"bin/internal/s3/s3err"
	"bin/internal/storage"
)

const s3NS = "http://s3.amazonaws.com/doc/2006-03-01/"

// maxXMLBody bounds request bodies that are parsed as XML (tagging,
// bucket configuration, multipart completion lists...).
const maxXMLBody = 1 << 20

// timeXML formats a timestamp the way S3 does in XML bodies.
func timeXML(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// writeXML renders v as an XML response with the standard declaration.
func writeXML(w http.ResponseWriter, status int, v any) error {
	body, err := xml.Marshal(v)
	if err != nil {
		return err
	}
	body = append([]byte(xml.Header), body...)
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

// readXMLBody reads a small XML request body into v, checking Content-MD5 and
// any x-amz-checksum-* header (or trailer) the client sent. It reports whether
// the body was empty, leaving v untouched.
func readXMLBody(r *http.Request, v any) (empty bool, err error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBody+1))
	if err != nil {
		return false, bodyError(err)
	}
	if len(data) > maxXMLBody {
		return false, s3err.ErrMalformedXML.WithMessage("The XML you provided was larger than the maximum allowed size.")
	}
	if want := r.Header.Get("Content-Md5"); want != "" {
		decoded, err := base64.StdEncoding.DecodeString(want)
		if err != nil || len(decoded) != md5.Size {
			return false, s3err.ErrInvalidDigest
		}
		if sum := md5.Sum(data); !bytes.Equal(sum[:], decoded) {
			return false, s3err.ErrBadDigest
		}
	}
	for _, a := range checksum.All {
		want := r.Header.Get(a.Header())
		if want == "" && r.Trailer != nil {
			want = r.Trailer.Get(a.Header())
		}
		if want == "" {
			continue
		}
		h := a.New()
		h.Write(data)
		if checksum.Encode(h.Sum(nil)) != want {
			return false, s3err.ErrBadDigest.WithMessage("The " + string(a) + " you specified did not match the calculated checksum.")
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return true, nil
	}
	if err := xml.Unmarshal(data, v); err != nil {
		return false, s3err.ErrMalformedXML
	}
	return false, nil
}

// bodyError maps a request-body read failure to an S3 error. Integrity errors
// raised by the SigV4 body reader are already S3 errors.
func bodyError(err error) error {
	var apiErr *s3err.Error
	if errors.As(err, &apiErr) {
		return err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return s3err.ErrIncompleteBody
	}
	return err
}

// storageError maps storage errors to S3 errors; unknown errors pass through
// and become InternalError.
func storageError(err error) error {
	var apiErr *s3err.Error
	if errors.As(err, &apiErr) {
		return err // raised while reading the request body (signature, digest...)
	}
	switch {
	case errors.Is(err, storage.ErrNoSuchKey):
		return s3err.ErrNoSuchKey
	case errors.Is(err, storage.ErrKeyTooLong):
		return s3err.ErrKeyTooLong
	case errors.Is(err, storage.ErrInvalidKey):
		return s3err.ErrInvalidArgument.WithMessage("Object keys must be valid UTF-8.")
	case errors.Is(err, storage.ErrPrecondition):
		return s3err.ErrPreconditionFailed
	case errors.Is(err, storage.ErrIncompleteBody), errors.Is(err, io.ErrUnexpectedEOF):
		return s3err.ErrIncompleteBody
	case errors.Is(err, storage.ErrNoSuchUpload):
		return s3err.ErrNoSuchUpload
	case errors.Is(err, storage.ErrInvalidPart):
		return s3err.ErrInvalidPart
	case errors.Is(err, storage.ErrInvalidPartOrder):
		return s3err.ErrInvalidPartOrder
	case errors.Is(err, storage.ErrEntityTooSmall):
		return s3err.ErrEntityTooSmall
	case errors.Is(err, storage.ErrSizeMismatch):
		return s3err.ErrInvalidRequest.WithMessage("The provided 'x-amz-mp-object-size' header value does not match the size of the completed object.")
	case errors.Is(err, storage.ErrBadDigest):
		return s3err.ErrBadDigest.WithMessage("The checksum you specified did not match the calculated checksum of the object.")
	case errors.Is(err, storage.ErrInvalidChecksumType):
		return s3err.ErrInvalidRequest.WithMessage("The checksum type is not supported for this checksum algorithm.")
	case errors.Is(err, storage.ErrInvalidRange):
		return s3err.ErrInvalidRange
	case errors.Is(err, storage.ErrNoSuchBucket):
		return s3err.ErrNoSuchBucket
	case errors.Is(err, storage.ErrBucketExists):
		return s3err.ErrBucketAlreadyOwnedByYou
	case errors.Is(err, storage.ErrBucketNotEmpty):
		return s3err.ErrBucketNotEmpty
	case errors.Is(err, storage.ErrInvalidBucketName):
		return s3err.ErrInvalidBucketName
	case errors.Is(err, storage.ErrNoTags):
		return s3err.ErrNoSuchTagSet
	}
	return err
}
