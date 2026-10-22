package s3

import (
	"encoding/base64"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"bin/internal/s3/s3err"
	"bin/internal/storage"
)

// maxListKeys is the most entries one listing page returns.
const maxListKeys = 1000

type objectXML struct {
	Key          string    `xml:"Key"`
	LastModified string    `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	Owner        *ownerXML `xml:"Owner,omitempty"`
	StorageClass string    `xml:"StorageClass"`
}

type commonPrefixXML struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResultV2 struct {
	XMLName               xml.Name          `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name                  string            `xml:"Name"`
	Prefix                string            `xml:"Prefix"`
	ContinuationToken     string            `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string            `xml:"NextContinuationToken,omitempty"`
	StartAfter            string            `xml:"StartAfter,omitempty"`
	KeyCount              int               `xml:"KeyCount"`
	MaxKeys               int               `xml:"MaxKeys"`
	Delimiter             string            `xml:"Delimiter,omitempty"`
	EncodingType          string            `xml:"EncodingType,omitempty"`
	IsTruncated           bool              `xml:"IsTruncated"`
	Contents              []objectXML       `xml:"Contents"`
	CommonPrefixes        []commonPrefixXML `xml:"CommonPrefixes"`
}

type listBucketResultV1 struct {
	XMLName        xml.Name          `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name           string            `xml:"Name"`
	Prefix         string            `xml:"Prefix"`
	Marker         string            `xml:"Marker"`
	NextMarker     string            `xml:"NextMarker,omitempty"`
	MaxKeys        int               `xml:"MaxKeys"`
	Delimiter      string            `xml:"Delimiter,omitempty"`
	EncodingType   string            `xml:"EncodingType,omitempty"`
	IsTruncated    bool              `xml:"IsTruncated"`
	Contents       []objectXML       `xml:"Contents"`
	CommonPrefixes []commonPrefixXML `xml:"CommonPrefixes"`
}

// listParams reads the parameters shared by both listing versions.
func listParams(r *http.Request) (prefix, delimiter string, maxKeys int, encode func(string) string, err error) {
	q := r.URL.Query()
	prefix, delimiter = q.Get("prefix"), q.Get("delimiter")
	maxKeys = maxListKeys
	if v := q.Get("max-keys"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 0 {
			return "", "", 0, nil, s3err.ErrInvalidArgument.WithMessage("Provided max-keys not an integer or within integer range")
		}
		maxKeys = min(n, maxListKeys)
	}
	encode = func(s string) string { return s }
	switch et := q.Get("encoding-type"); et {
	case "":
	case "url":
		encode = urlEncodeKey
	default:
		return "", "", 0, nil, s3err.ErrInvalidArgument.WithMessage("Invalid Encoding Method specified in Request")
	}
	return prefix, delimiter, maxKeys, encode, nil
}

func (h *Handler) listObjectsV2(w http.ResponseWriter, r *http.Request, req Request) error {
	prefix, delimiter, maxKeys, encode, err := listParams(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	token, startAfter := q.Get("continuation-token"), q.Get("start-after")
	after := startAfter
	if q.Has("continuation-token") {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || token == "" {
			return s3err.ErrInvalidArgument.WithMessage("The continuation token provided is incorrect")
		}
		after = string(decoded)
	}
	res, err := h.opts.Store.ListObjects(r.Context(), req.Bucket, storage.ListOptions{
		Prefix: prefix, Delimiter: delimiter, After: after, MaxKeys: maxKeys,
	})
	if err != nil {
		return storageError(err)
	}
	var owner *ownerXML
	if q.Get("fetch-owner") == "true" {
		o := ownerXML(h.opts.Owner)
		owner = &o
	}
	out := listBucketResultV2{
		Name:              req.Bucket,
		Prefix:            encode(prefix),
		ContinuationToken: token,
		StartAfter:        encode(startAfter),
		KeyCount:          len(res.Objects) + len(res.CommonPrefixes),
		MaxKeys:           maxKeys,
		Delimiter:         encode(delimiter),
		IsTruncated:       res.IsTruncated,
		Contents:          contentsXML(res.Objects, encode, owner),
		CommonPrefixes:    prefixesXML(res.CommonPrefixes, encode),
	}
	if q.Get("encoding-type") != "" {
		out.EncodingType = "url"
	}
	if res.IsTruncated {
		out.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(res.NextMarker))
	}
	return writeXML(w, http.StatusOK, out)
}

func (h *Handler) listObjectsV1(w http.ResponseWriter, r *http.Request, req Request) error {
	prefix, delimiter, maxKeys, encode, err := listParams(r)
	if err != nil {
		return err
	}
	marker := r.URL.Query().Get("marker")
	res, err := h.opts.Store.ListObjects(r.Context(), req.Bucket, storage.ListOptions{
		Prefix: prefix, Delimiter: delimiter, After: marker, MaxKeys: maxKeys,
	})
	if err != nil {
		return storageError(err)
	}
	owner := ownerXML(h.opts.Owner)
	out := listBucketResultV1{
		Name:           req.Bucket,
		Prefix:         encode(prefix),
		Marker:         encode(marker),
		MaxKeys:        maxKeys,
		Delimiter:      encode(delimiter),
		IsTruncated:    res.IsTruncated,
		Contents:       contentsXML(res.Objects, encode, &owner),
		CommonPrefixes: prefixesXML(res.CommonPrefixes, encode),
	}
	if r.URL.Query().Get("encoding-type") != "" {
		out.EncodingType = "url"
	}
	// S3 only returns NextMarker with a delimiter; without one, clients
	// continue from the last key. Returning it always is harmless and
	// spares clients that rely on it.
	if res.IsTruncated {
		out.NextMarker = encode(res.NextMarker)
	}
	return writeXML(w, http.StatusOK, out)
}

func contentsXML(objects []storage.ObjectInfo, encode func(string) string, owner *ownerXML) []objectXML {
	out := make([]objectXML, 0, len(objects))
	for _, o := range objects {
		out = append(out, objectXML{
			Key:          encode(o.Key),
			LastModified: timeXML(o.ModTime),
			ETag:         quoteETag(o.ETag),
			Size:         o.Size,
			Owner:        owner,
			StorageClass: "STANDARD",
		})
	}
	return out
}

func prefixesXML(prefixes []string, encode func(string) string) []commonPrefixXML {
	out := make([]commonPrefixXML, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, commonPrefixXML{Prefix: encode(p)})
	}
	return out
}

// urlEncodeKey applies encoding-type=url: every byte outside the unreserved
// set is percent-encoded, spaces as %20, and "/" stays literal. Clients that
// decode with plain URL decoding and those that use form decoding ("+" as a
// space) read it the same way.
func urlEncodeKey(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}
