package s3

import (
	"encoding/xml"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"bin/internal/s3/s3err"
)

type ownerXML struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type listAllMyBucketsResult struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListAllMyBucketsResult"`
	Owner   ownerXML `xml:"Owner"`
	Buckets struct {
		Bucket []bucketXML `xml:"Bucket"`
	} `xml:"Buckets"`
}

type bucketXML struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request, _ Request) error {
	buckets, err := h.opts.Store.ListBuckets(r.Context())
	if err != nil {
		return err
	}
	res := listAllMyBucketsResult{Owner: ownerXML(h.opts.Owner)}
	grants := grantsFrom(r.Context())
	for _, b := range buckets {
		if grants.AllowsBucket(b.Name) {
			res.Buckets.Bucket = append(res.Buckets.Bucket, bucketXML{Name: b.Name, CreationDate: timeXML(b.CreatedAt)})
		}
	}
	return writeXML(w, http.StatusOK, res)
}

type createBucketConfiguration struct {
	LocationConstraint string `xml:"LocationConstraint"`
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request, req Request) error {
	if strings.EqualFold(r.Header.Get("X-Amz-Bucket-Object-Lock-Enabled"), "true") {
		return s3err.ErrNotImplemented.WithMessage("Object lock is not supported yet.")
	}
	var cfg createBucketConfiguration
	if _, err := readXMLBody(r, &cfg); err != nil {
		return err
	}
	if lc := cfg.LocationConstraint; lc != "" && lc != h.opts.Region {
		return s3err.ErrIllegalLocationConstraint.WithMessage(
			"The " + lc + " location constraint is incompatible for the region specific endpoint this request was sent to.")
	}
	if err := h.opts.Store.CreateBucket(r.Context(), req.Bucket); err != nil {
		return storageError(err)
	}
	w.Header().Set("Location", "/"+req.Bucket)
	w.WriteHeader(http.StatusOK)
	return nil
}

func (h *Handler) headBucket(w http.ResponseWriter, r *http.Request, req Request) error {
	if _, err := h.opts.Store.GetBucket(r.Context(), req.Bucket); err != nil {
		return storageError(err)
	}
	w.Header().Set("X-Amz-Bucket-Region", h.opts.Region)
	w.WriteHeader(http.StatusOK)
	return nil
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, req Request) error {
	if err := h.opts.Store.DeleteBucket(r.Context(), req.Bucket); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type locationConstraint struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ LocationConstraint"`
	Value   string   `xml:",chardata"`
}

func (h *Handler) getBucketLocation(w http.ResponseWriter, r *http.Request, req Request) error {
	if _, err := h.opts.Store.GetBucket(r.Context(), req.Bucket); err != nil {
		return storageError(err)
	}
	res := locationConstraint{}
	if h.opts.Region != "us-east-1" { // S3 reports us-east-1 as an empty constraint
		res.Value = h.opts.Region
	}
	return writeXML(w, http.StatusOK, res)
}

type versioningConfiguration struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

func (h *Handler) getBucketVersioning(w http.ResponseWriter, r *http.Request, req Request) error {
	b, err := h.opts.Store.GetBucket(r.Context(), req.Bucket)
	if err != nil {
		return storageError(err)
	}
	return writeXML(w, http.StatusOK, versioningConfiguration{Status: b.Versioning})
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagging struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ Tagging"`
	TagSet  struct {
		Tag []tagXML `xml:"Tag"`
	} `xml:"TagSet"`
}

func (h *Handler) getBucketTagging(w http.ResponseWriter, r *http.Request, req Request) error {
	tags, err := h.opts.Store.BucketTags(r.Context(), req.Bucket)
	if err != nil {
		return storageError(err)
	}
	var res tagging
	for k, v := range tags {
		res.TagSet.Tag = append(res.TagSet.Tag, tagXML{Key: k, Value: v})
	}
	return writeXML(w, http.StatusOK, res)
}

func (h *Handler) putBucketTagging(w http.ResponseWriter, r *http.Request, req Request) error {
	var in tagging
	empty, err := readXMLBody(r, &in)
	if err != nil {
		return err
	}
	if empty {
		return s3err.ErrMalformedXML
	}
	tags, err := validateTags(in.TagSet.Tag)
	if err != nil {
		return err
	}
	if err := h.opts.Store.SetBucketTags(r.Context(), req.Bucket, tags); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) deleteBucketTagging(w http.ResponseWriter, r *http.Request, req Request) error {
	if err := h.opts.Store.SetBucketTags(r.Context(), req.Bucket, nil); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// validateTags applies the S3 tag limits: at most 50 tags, unique keys of
// 1-128 characters, values up to 256, letters, digits, spaces and + - = . _ : / @.
func validateTags(in []tagXML) (map[string]string, error) {
	if len(in) > 50 {
		return nil, s3err.ErrInvalidTag.WithMessage("Object tags cannot be greater than 50")
	}
	tags := make(map[string]string, len(in))
	for _, t := range in {
		if _, dup := tags[t.Key]; dup {
			return nil, s3err.ErrInvalidTag.WithMessage("Cannot provide multiple Tags with the same key")
		}
		if strings.HasPrefix(t.Key, "aws:") {
			return nil, s3err.ErrInvalidTag.WithMessage("System tags cannot be added/updated by requester")
		}
		if n := utf8.RuneCountInString(t.Key); n < 1 || n > 128 || !validTagText(t.Key) {
			return nil, s3err.ErrInvalidTag.WithMessage("The TagKey you have provided is invalid")
		}
		if utf8.RuneCountInString(t.Value) > 256 || !validTagText(t.Value) {
			return nil, s3err.ErrInvalidTag.WithMessage("The TagValue you have provided is invalid")
		}
		tags[t.Key] = t.Value
	}
	return tags, nil
}

func validTagText(s string) bool {
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !unicode.IsSpace(c) && !strings.ContainsRune("+-=._:/@", c) {
			return false
		}
	}
	return true
}
