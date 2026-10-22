// Package s3err defines S3 API errors and renders them in the standard S3 XML
// format:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<Error><Code>NoSuchKey</Code><Message>...</Message><Key>k</Key>
//	<BucketName>b</BucketName><Resource>/b/k</Resource>
//	<RequestId>...</RequestId><HostId>...</HostId></Error>
//
// Codes, statuses and messages follow the AWS S3 error reference.
package s3err

import (
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
)

// Error is an S3 API error. Values are comparable by Code with errors.Is.
type Error struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Is matches any *Error with the same Code, so a customized message still
// matches the base error: errors.Is(err, s3err.ErrNoSuchKey).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// WithMessage returns a copy of e with a more specific message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

func newErr(code string, status int, msg string) *Error {
	return &Error{Code: code, Message: msg, HTTPStatus: status}
}

var (
	ErrAccessDenied                 = newErr("AccessDenied", 403, "Access Denied.")
	ErrAuthorizationHeaderMalformed = newErr("AuthorizationHeaderMalformed", 400, "The authorization header is malformed.")
	ErrAuthorizationQueryMalformed  = newErr("AuthorizationQueryParametersError", 400, "Error parsing the X-Amz-Credential parameter.")
	ErrBadDigest                    = newErr("BadDigest", 400, "The Content-MD5 you specified did not match what we received.")
	ErrBucketAlreadyExists          = newErr("BucketAlreadyExists", 409, "The requested bucket name is not available. The bucket namespace is shared by all users of the system. Please select a different name and try again.")
	ErrBucketAlreadyOwnedByYou      = newErr("BucketAlreadyOwnedByYou", 409, "Your previous request to create the named bucket succeeded and you already own it.")
	ErrBucketNotEmpty               = newErr("BucketNotEmpty", 409, "The bucket you tried to delete is not empty")
	ErrEntityTooLarge               = newErr("EntityTooLarge", 400, "Your proposed upload exceeds the maximum allowed object size.")
	ErrEntityTooSmall               = newErr("EntityTooSmall", 400, "Your proposed upload is smaller than the minimum allowed object size.")
	ErrIllegalLocationConstraint    = newErr("IllegalLocationConstraintException", 400, "The location constraint is incompatible for the region specific endpoint this request was sent to.")
	ErrIncompleteBody               = newErr("IncompleteBody", 400, "You did not provide the number of bytes specified by the Content-Length HTTP header.")
	ErrInternalError                = newErr("InternalError", 500, "We encountered an internal error, please try again.")
	ErrInvalidAccessKeyID           = newErr("InvalidAccessKeyId", 403, "The AWS Access Key Id you provided does not exist in our records.")
	ErrInvalidArgument              = newErr("InvalidArgument", 400, "Invalid Argument")
	ErrInvalidBucketName            = newErr("InvalidBucketName", 400, "The specified bucket is not valid.")
	ErrInvalidDigest                = newErr("InvalidDigest", 400, "The Content-MD5 you specified is not valid.")
	ErrInvalidPart                  = newErr("InvalidPart", 400, "One or more of the specified parts could not be found. The part may not have been uploaded, or the specified entity tag may not match the part's entity tag.")
	ErrInvalidPartOrder             = newErr("InvalidPartOrder", 400, "The list of parts was not in ascending order. The parts list must be specified in order by part number.")
	ErrInvalidRange                 = newErr("InvalidRange", 416, "The requested range is not satisfiable")
	ErrInvalidRequest               = newErr("InvalidRequest", 400, "Invalid Request")
	ErrInvalidTag                   = newErr("InvalidTag", 400, "The tag provided was not a valid tag.")
	ErrKeyTooLong                   = newErr("KeyTooLongError", 400, "Your key is too long")
	ErrMalformedXML                 = newErr("MalformedXML", 400, "The XML you provided was not well-formed or did not validate against our published schema.")
	ErrMethodNotAllowed             = newErr("MethodNotAllowed", 405, "The specified method is not allowed against this resource.")
	ErrMissingContentLength         = newErr("MissingContentLength", 411, "You must provide the Content-Length HTTP header.")
	ErrNoSuchBucket                 = newErr("NoSuchBucket", 404, "The specified bucket does not exist")
	ErrNoSuchKey                    = newErr("NoSuchKey", 404, "The specified key does not exist.")
	ErrNoSuchTagSet                 = newErr("NoSuchTagSet", 404, "The TagSet does not exist")
	ErrNoSuchUpload                 = newErr("NoSuchUpload", 404, "The specified multipart upload does not exist. The upload ID may be invalid, or the upload may have been aborted or completed.")
	ErrNotImplemented               = newErr("NotImplemented", 501, "A header you provided implies functionality that is not implemented.")
	ErrPreconditionFailed           = newErr("PreconditionFailed", 412, "At least one of the pre-conditions you specified did not hold")
	ErrRequestTimeTooSkewed         = newErr("RequestTimeTooSkewed", 403, "The difference between the request time and the server's time is too large.")
	ErrSignatureDoesNotMatch        = newErr("SignatureDoesNotMatch", 403, "The request signature we calculated does not match the signature you provided. Check your key and signing method.")
	ErrContentSHA256Mismatch        = newErr("XAmzContentSHA256Mismatch", 400, "The provided 'x-amz-content-sha256' header does not match what was computed.")
)

// Resource identifies what the failed request was about.
type Resource struct {
	Bucket string
	Key    string
}

// path renders the Resource element the way S3 does: /bucket/key.
func (r Resource) path() string {
	switch {
	case r.Bucket == "":
		return "/"
	case r.Key == "":
		return "/" + r.Bucket
	default:
		return "/" + r.Bucket + "/" + r.Key
	}
}

type errorBody struct {
	XMLName    xml.Name `xml:"Error"`
	Code       string   `xml:"Code"`
	Message    string   `xml:"Message"`
	Key        string   `xml:"Key,omitempty"`
	BucketName string   `xml:"BucketName,omitempty"`
	Resource   string   `xml:"Resource"`
	RequestID  string   `xml:"RequestId"`
	HostID     string   `xml:"HostId"`
}

// Write renders err as an S3 error response. Errors that are not *Error become
// InternalError; the caller is expected to have logged the original.
// HEAD responses carry the status code only, as HEAD cannot have a body.
func Write(w http.ResponseWriter, r *http.Request, err error, res Resource, requestID, hostID string) {
	var e *Error
	if !errors.As(err, &e) {
		e = ErrInternalError
	}
	h := w.Header()
	if r.Method == http.MethodHead {
		w.WriteHeader(e.HTTPStatus)
		return
	}
	body, _ := xml.Marshal(errorBody{
		Code:       e.Code,
		Message:    e.Message,
		Key:        res.Key,
		BucketName: res.Bucket,
		Resource:   res.path(),
		RequestID:  requestID,
		HostID:     hostID,
	})
	body = append([]byte(xml.Header), body...)
	h.Set("Content-Type", "application/xml")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(e.HTTPStatus)
	w.Write(body)
}
