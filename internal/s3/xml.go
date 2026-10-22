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

// readXMLBody reads a small XML request body into v, checking Content-MD5
// when present. It reports whether the body was empty, leaving v untouched.
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
	switch {
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
