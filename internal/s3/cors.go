package s3

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"bin/internal/cors"
	"bin/internal/s3/s3err"
)

// --- bucket CORS configuration ------------------------------------------------------

type corsRuleXML struct {
	ID             string   `xml:"ID,omitempty"`
	AllowedHeaders []string `xml:"AllowedHeader"`
	AllowedMethods []string `xml:"AllowedMethod"`
	AllowedOrigins []string `xml:"AllowedOrigin"`
	ExposeHeaders  []string `xml:"ExposeHeader"`
	MaxAgeSeconds  *int     `xml:"MaxAgeSeconds,omitempty"`
}

type corsConfiguration struct {
	XMLName xml.Name      `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CORSConfiguration"`
	Rules   []corsRuleXML `xml:"CORSRule"`
}

func (h *Handler) getBucketCors(w http.ResponseWriter, r *http.Request, req Request) error {
	b, err := h.opts.Store.GetBucket(r.Context(), req.Bucket)
	if err != nil {
		return storageError(err)
	}
	rules, err := cors.Parse(b.CORS)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return s3err.ErrNoSuchCORSConfiguration
	}
	var out corsConfiguration
	for _, rule := range rules {
		out.Rules = append(out.Rules, corsRuleXML{
			ID: rule.ID, AllowedHeaders: rule.AllowedHeaders, AllowedMethods: rule.AllowedMethods,
			AllowedOrigins: rule.AllowedOrigins, ExposeHeaders: rule.ExposeHeaders, MaxAgeSeconds: rule.MaxAgeSeconds,
		})
	}
	return writeXML(w, http.StatusOK, out)
}

func (h *Handler) putBucketCors(w http.ResponseWriter, r *http.Request, req Request) error {
	var in corsConfiguration
	empty, err := readXMLBody(r, &in)
	if err != nil {
		return err
	}
	if empty || len(in.Rules) == 0 {
		return s3err.ErrMalformedXML
	}
	rules := make([]cors.Rule, 0, len(in.Rules))
	for _, x := range in.Rules {
		rules = append(rules, cors.Rule{
			ID: x.ID, AllowedOrigins: trimAll(x.AllowedOrigins), AllowedMethods: trimAll(x.AllowedMethods),
			AllowedHeaders: trimAll(x.AllowedHeaders), ExposeHeaders: trimAll(x.ExposeHeaders), MaxAgeSeconds: x.MaxAgeSeconds,
		})
	}
	if err := cors.Validate(rules); err != nil {
		return s3err.ErrInvalidRequest.WithMessage("Invalid CORS configuration: " + err.Error() + ".")
	}
	if err := h.opts.Store.SetBucketCORS(r.Context(), req.Bucket, cors.Encode(rules)); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (h *Handler) deleteBucketCors(w http.ResponseWriter, r *http.Request, req Request) error {
	if err := h.opts.Store.SetBucketCORS(r.Context(), req.Bucket, ""); err != nil {
		return storageError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func trimAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.TrimSpace(v))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- CORS requests --------------------------------------------------------------------

var errCORSDisabled = s3err.ErrAccessForbidden.WithMessage("CORSResponse: CORS is not enabled for this bucket.")

// preflight answers a browser's OPTIONS request from the bucket's CORS rules.
// It is unauthenticated, so a missing bucket looks like one without CORS.
func (h *Handler) preflight(w http.ResponseWriter, r *http.Request, req Request) {
	origin := r.Header.Get("Origin")
	method := r.Header.Get("Access-Control-Request-Method")
	switch {
	case origin == "":
		h.writeError(w, r, req, s3err.ErrBadRequest)
		return
	case !validCORSMethod(method):
		h.writeError(w, r, req, s3err.ErrBadRequest.WithMessage("Invalid Access-Control-Request-Method: "+method))
		return
	}
	rules := h.bucketCORS(r, req.Bucket)
	if len(rules) == 0 {
		h.writeError(w, r, req, errCORSDisabled)
		return
	}
	requested := cors.RequestHeaders(r.Header.Get("Access-Control-Request-Headers"))
	rule, ok := cors.Match(rules, origin, method, requested)
	if !ok {
		h.writeError(w, r, req, s3err.ErrAccessForbidden)
		return
	}
	hdr := w.Header()
	allowHeaders(hdr, rule, origin)
	if len(requested) > 0 {
		hdr.Set("Access-Control-Allow-Headers", strings.Join(requested, ", "))
	}
	hdr.Set("Vary", "Origin, Access-Control-Request-Headers, Access-Control-Request-Method")
	hdr.Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

// setCORSHeaders adds the CORS response headers to a request from a browser
// origin that a rule of the bucket allows. They are set before the request is
// handled so that errors carry them too, letting the page read the error.
func (h *Handler) setCORSHeaders(w http.ResponseWriter, r *http.Request, bucket string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	rules := h.bucketCORS(r, bucket)
	if len(rules) == 0 {
		return
	}
	w.Header().Set("Vary", "Origin")
	if rule, ok := cors.Match(rules, origin, r.Method, nil); ok {
		allowHeaders(w.Header(), rule, origin)
	}
}

// bucketCORS returns the bucket's CORS rules; none if it has none, does not
// exist, or the request names no bucket.
func (h *Handler) bucketCORS(r *http.Request, bucket string) []cors.Rule {
	if bucket == "" || h.opts.Store == nil {
		return nil
	}
	b, err := h.opts.Store.GetBucket(r.Context(), bucket)
	if err != nil {
		return nil
	}
	rules, err := cors.Parse(b.CORS)
	if err != nil {
		h.opts.Log.Error("unreadable CORS configuration", "bucket", bucket, "err", err)
		return nil
	}
	return rules
}

func allowHeaders(hdr http.Header, rule cors.Rule, origin string) {
	if rule.AnyOrigin() {
		hdr.Set("Access-Control-Allow-Origin", "*")
	} else {
		hdr.Set("Access-Control-Allow-Origin", origin)
		hdr.Set("Access-Control-Allow-Credentials", "true")
	}
	hdr.Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
	if len(rule.ExposeHeaders) > 0 {
		hdr.Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
	}
	if rule.MaxAgeSeconds != nil {
		hdr.Set("Access-Control-Max-Age", strconv.Itoa(*rule.MaxAgeSeconds))
	}
}

func validCORSMethod(m string) bool {
	for _, v := range cors.Methods {
		if m == v {
			return true
		}
	}
	return false
}
