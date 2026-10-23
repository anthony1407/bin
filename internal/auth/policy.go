// Package auth decides what a request may do: the scope of access keys and
// the anonymous-access policy of buckets. It also holds the keyring that
// resolves access keys to secrets for signature verification.
package auth

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Action groups S3 operations by the permission they need.
type Action int

const (
	ReadObject   Action = iota // GetObject, HeadObject
	ListBucket                 // ListObjects, ListObjectsV2 (the resource is the listed prefix)
	BucketInfo                 // HeadBucket, GetBucketLocation, GetBucketVersioning, GetBucketTagging
	WriteObject                // PutObject, CopyObject target, multipart uploads
	DeleteObject               // DeleteObject, DeleteObjects
	ManageBucket               // CreateBucket, DeleteBucket, bucket tagging changes
	ListBuckets                // ListBuckets (results are filtered by scope)
)

func (a Action) writes() bool { return a == WriteObject || a == DeleteObject || a == ManageBucket }

// --- access key scope -------------------------------------------------------------

// Scope restricts what an access key may do. The zero value is full access.
type Scope struct {
	Buckets  []string `json:"buckets,omitempty"` // empty: every bucket
	ReadOnly bool     `json:"readOnly,omitempty"`
}

// ParseScope reads a stored scope; "" is full access.
func ParseScope(s string) (Scope, error) {
	var sc Scope
	if s == "" {
		return sc, nil
	}
	err := json.Unmarshal([]byte(s), &sc)
	return sc, err
}

// String is the stored form of the scope.
func (s Scope) String() string {
	if len(s.Buckets) == 0 && !s.ReadOnly {
		return ""
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// AllowsBucket reports whether the key may touch bucket at all.
func (s Scope) AllowsBucket(bucket string) bool {
	return len(s.Buckets) == 0 || slices.Contains(s.Buckets, bucket)
}

// Allows reports whether the key may perform a on bucket.
func (s Scope) Allows(a Action, bucket string) bool {
	if s.ReadOnly && a.writes() {
		return false
	}
	return a == ListBuckets || s.AllowsBucket(bucket)
}

// Grants is the effective access of a key: everything any one of its scopes
// allows. A key outside groups has its own scope; a group member has the
// scopes of its enabled groups. No grants means no access.
type Grants []Scope

// FullAccess is what root has.
var FullAccess = Grants{{}}

// AllowsBucket reports whether some scope lets the key touch bucket.
func (g Grants) AllowsBucket(bucket string) bool {
	return slices.ContainsFunc(g, func(s Scope) bool { return s.AllowsBucket(bucket) })
}

// Allows reports whether some scope lets the key perform a on bucket.
func (g Grants) Allows(a Action, bucket string) bool {
	return slices.ContainsFunc(g, func(s Scope) bool { return s.Allows(a, bucket) })
}

// --- bucket policy ---------------------------------------------------------------

// Access is what an anonymous rule grants under its prefix.
type Access string

const (
	ReadOnly  Access = "readonly"
	WriteOnly Access = "writeonly"
	ReadWrite Access = "readwrite"
)

// Rule grants anonymous access to keys starting with Prefix, or with Exact
// to the one object whose key is Prefix. Exact rules never allow listing.
type Rule struct {
	Prefix string `json:"prefix"`
	Access Access `json:"access"`
	Exact  bool   `json:"exact,omitempty"`
}

// covers reports whether the rule applies to resource.
func (r Rule) covers(resource string) bool {
	if r.Exact {
		return resource == r.Prefix
	}
	return strings.HasPrefix(resource, r.Prefix)
}

// Policy is a bucket's anonymous-access policy.
type Policy struct {
	Mode  string `json:"mode"` // "private", "public" (anonymous read of everything) or "custom"
	Rules []Rule `json:"rules,omitempty"`
}

// ParsePolicy reads a stored policy; "" is private.
func ParsePolicy(s string) (Policy, error) {
	if s == "" {
		return Policy{Mode: "private"}, nil
	}
	var p Policy
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Policy{}, err
	}
	return p, p.Validate()
}

// Validate checks a policy before it is stored.
func (p Policy) Validate() error {
	switch p.Mode {
	case "private", "public":
		if len(p.Rules) > 0 {
			return fmt.Errorf("%s buckets take no rules", p.Mode)
		}
	case "custom":
		if len(p.Rules) == 0 {
			return fmt.Errorf("custom policies need at least one rule")
		}
		for _, r := range p.Rules {
			if r.Exact && r.Prefix == "" {
				return fmt.Errorf("an object rule needs the object key")
			}
			switch r.Access {
			case ReadOnly, WriteOnly, ReadWrite:
			default:
				return fmt.Errorf("unknown access %q (use readonly, writeonly or readwrite)", r.Access)
			}
		}
	default:
		return fmt.Errorf("unknown mode %q (use private, public or custom)", p.Mode)
	}
	return nil
}

// String is the stored form; private is "".
func (p Policy) String() string {
	if p.Mode == "" || p.Mode == "private" {
		return ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func (p Policy) rules() []Rule {
	if p.Mode == "public" {
		return []Rule{{Prefix: "", Access: ReadOnly}}
	}
	if p.Mode == "custom" {
		return p.Rules
	}
	return nil
}

// AllowsAnonymous reports whether an unauthenticated request may perform a.
// resource is the object key for object actions and the requested prefix
// for ListBucket; it is ignored for BucketInfo.
func (p Policy) AllowsAnonymous(a Action, resource string) bool {
	for _, r := range p.rules() {
		reads := r.Access == ReadOnly || r.Access == ReadWrite
		writes := r.Access == WriteOnly || r.Access == ReadWrite
		under := r.covers(resource)
		switch a {
		case BucketInfo:
			// One public object does not reveal the bucket.
			if reads && !r.Exact {
				return true
			}
		case ReadObject:
			if reads && under {
				return true
			}
		case ListBucket:
			if reads && under && !r.Exact {
				return true
			}
		case WriteObject:
			if writes && under {
				return true
			}
		case DeleteObject:
			if r.Access == ReadWrite && under {
				return true
			}
		}
	}
	return false
}
