package admin

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"bin/internal/auth"
)

// Translating S3 (IAM) policy documents into bin's simpler model. bin grants
// access per bucket, read-only or read-write, and anonymous access per
// prefix; anything a document says that bin cannot express without granting
// more (Deny, conditions, part of a bucket, write-only) makes it untranslatable,
// with a reason the import reports. Granting less is never silent either.

// stringList accepts the IAM forms "x" and ["x", "y"].
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

type iamStatement struct {
	Effect      string          `json:"Effect"`
	Principal   json.RawMessage `json:"Principal"`
	Action      stringList      `json:"Action"`
	NotAction   stringList      `json:"NotAction"`
	Resource    stringList      `json:"Resource"`
	NotResource stringList      `json:"NotResource"`
	Condition   json.RawMessage `json:"Condition"`
}

type iamDocument struct {
	Statement []iamStatement `json:"Statement"`
}

func parseIAMDocument(doc []byte) (iamDocument, bool) {
	var d iamDocument
	if err := json.Unmarshal(doc, &d); err != nil {
		// Some documents have one statement object instead of a list.
		var one struct {
			Statement iamStatement `json:"Statement"`
		}
		if json.Unmarshal(doc, &one) != nil {
			return d, false
		}
		d.Statement = []iamStatement{one.Statement}
	}
	return d, true
}

func hasCondition(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}"
}

const s3ARN = "arn:aws:s3:::"

// isReadAction reports whether an S3 action only reads.
func isReadAction(action string) bool {
	a := strings.ToLower(strings.TrimPrefix(strings.ToLower(action), "s3:"))
	return a != "*" && (strings.HasPrefix(a, "get") || strings.HasPrefix(a, "list") || strings.HasPrefix(a, "head"))
}

// grantsObjectRead reports whether an action lets a user read objects.
func grantsObjectRead(action string) bool {
	switch strings.ToLower(action) {
	case "*", "s3:*", "s3:get*", "s3:getobject":
		return true
	}
	return false
}

// scopeFromIAM translates a user policy into a bin scope.
func scopeFromIAM(doc []byte) (auth.Scope, string, bool) {
	d, ok := parseIAMDocument(doc)
	if !ok {
		return auth.Scope{}, "the policy document could not be read", false
	}
	all, write, read := false, map[string]bool{}, map[string]bool{}
	allWrite, any := false, false
	// Which buckets the policy lets read objects in: both bin access levels
	// do, so a bucket it only lets list, inspect or write would gain that.
	allObjRead, objRead := false, map[string]bool{}
	for _, st := range d.Statement {
		if !strings.EqualFold(st.Effect, "Allow") {
			return auth.Scope{}, "it uses Deny, which bin cannot express", false
		}
		if len(st.NotAction) > 0 || len(st.NotResource) > 0 {
			return auth.Scope{}, "it uses NotAction or NotResource", false
		}
		var actions []string
		for _, a := range st.Action {
			la := strings.ToLower(a)
			switch {
			case la == "*" || la == "s3:*":
				actions = append(actions, a)
			case !strings.HasPrefix(la, "s3:"):
				// admin:, kms:, sts: ... manage the server; bin keeps that for root.
			case la == "s3:listallmybuckets" || la == "s3:getbucketlocation":
				// Account-level: bin always lets keys list the buckets they can use.
			default:
				actions = append(actions, a)
			}
		}
		if len(actions) == 0 {
			continue
		}
		if hasCondition(st.Condition) {
			return auth.Scope{}, "it has conditions, which bin cannot express", false
		}
		writes, readsObjects := false, false
		for _, a := range actions {
			if !isReadAction(a) {
				writes = true
			}
			if grantsObjectRead(a) {
				readsObjects = true
			}
		}
		for _, r := range st.Resource {
			bucket, rest, isAll, ok := splitS3Resource(r)
			if !ok {
				return auth.Scope{}, "it names buckets with wildcards (" + r + ")", false
			}
			if rest != "" && rest != "*" {
				return auth.Scope{}, "it limits access to part of a bucket (" + r + ")", false
			}
			any = true
			switch {
			case isAll && writes:
				all, allWrite = true, true
			case isAll:
				all = true
			case writes:
				write[bucket] = true
			default:
				read[bucket] = true
			}
			if readsObjects {
				if isAll {
					allObjRead = true
				} else {
					objRead[bucket] = true
				}
			}
		}
	}
	if !any {
		return auth.Scope{}, "it grants no access to buckets", false
	}
	// Checked per bucket: reading objects in one bucket says nothing about
	// another.
	readsIn := func(b string) bool { return allObjRead || objRead[b] }
	if allWrite && !allObjRead {
		return auth.Scope{}, "it is write-only, which bin cannot express", false
	}
	for _, b := range sortedKeys(write) {
		if !readsIn(b) {
			return auth.Scope{}, "it is write-only on " + b + ", which bin cannot express", false
		}
	}
	if all && !allObjRead {
		return auth.Scope{}, "it lets list or inspect every bucket without reading its files, which bin cannot express", false
	}
	for _, b := range sortedKeys(read) {
		if !readsIn(b) {
			return auth.Scope{}, "it lets list or inspect " + b + " without reading its files, which bin cannot express", false
		}
	}
	hasWrite := allWrite || len(write) > 0
	if !hasWrite {
		if all {
			return auth.Scope{ReadOnly: true}, "", true
		}
		return auth.Scope{Buckets: sortedKeys(read), ReadOnly: true}, "", true
	}
	// bin gives one access level to all the scope's buckets: reading more
	// buckets than it may write is not expressible.
	if allWrite {
		return auth.Scope{}, "", true
	}
	if all {
		return auth.Scope{}, "it gives read access to every bucket but write access to some", false
	}
	for b := range read {
		if !write[b] {
			return auth.Scope{}, "it gives read-only access to some buckets and read-write to others", false
		}
	}
	return auth.Scope{Buckets: sortedKeys(write)}, "", true
}

// policyDenies reports whether a policy document takes access away (or
// cannot be read, so might). Such a policy restricts what a user's other
// policies grant: dropping it as untranslatable would leave them granting
// more than MinIO did.
func policyDenies(doc []byte) bool {
	d, ok := parseIAMDocument(doc)
	if !ok {
		return true
	}
	for _, st := range d.Statement {
		if !strings.EqualFold(st.Effect, "Allow") {
			return true
		}
	}
	return false
}

// listPrefixes reads the condition of an anonymous ListBucket statement:
// the prefixes it may list, when the condition only limits s3:prefix with
// StringEquals or StringLike (as MinIO's "mc anonymous set" writes it).
func listPrefixes(raw json.RawMessage) ([]string, bool) {
	var cond map[string]map[string]stringList
	if json.Unmarshal(raw, &cond) != nil || len(cond) != 1 {
		return nil, false
	}
	for op, keys := range cond {
		like := strings.EqualFold(op, "StringLike")
		if (!like && !strings.EqualFold(op, "StringEquals")) || len(keys) != 1 {
			return nil, false
		}
		for k, values := range keys {
			if !strings.EqualFold(k, "s3:prefix") {
				return nil, false
			}
			var out []string
			for _, v := range values {
				if like && strings.HasSuffix(v, "*") {
					v = strings.TrimSuffix(v, "*")
				}
				if strings.ContainsAny(v, "*?") {
					return nil, false
				}
				out = append(out, v)
			}
			return out, true
		}
	}
	return nil, false
}

// splitS3Resource splits "arn:aws:s3:::bucket/rest". isAll is set for "*"
// and "arn:aws:s3:::*"; a wildcard elsewhere in the bucket name is not ok.
func splitS3Resource(r string) (bucket, rest string, isAll, ok bool) {
	if r == "*" {
		return "", "", true, true
	}
	if !strings.HasPrefix(r, s3ARN) {
		return "", "", false, false
	}
	bucket, rest, _ = strings.Cut(strings.TrimPrefix(r, s3ARN), "/")
	if bucket == "*" {
		return "", "", true, rest == "" || rest == "*"
	}
	if strings.ContainsAny(bucket, "*?") {
		return "", "", false, false
	}
	return bucket, rest, false, true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// intersectScopes is the access both scopes allow; ok is false when that is
// nothing (no bucket in common), which a scope cannot express.
func intersectScopes(a, b auth.Scope) (auth.Scope, bool) {
	out := auth.Scope{ReadOnly: a.ReadOnly || b.ReadOnly}
	switch {
	case len(a.Buckets) == 0:
		out.Buckets = b.Buckets
	case len(b.Buckets) == 0:
		out.Buckets = a.Buckets
	default:
		for _, x := range a.Buckets {
			if slices.Contains(b.Buckets, x) {
				out.Buckets = append(out.Buckets, x)
			}
		}
		if len(out.Buckets) == 0 {
			return out, false
		}
	}
	return out, true
}

// anonymousPolicy translates a bucket policy into bin's anonymous access.
// Statements for named principals are left out (bin grants those through
// access keys), and so are bucket-level actions: in bin, reading a prefix
// also allows listing it. notes say what was left out.
func anonymousPolicy(bucket, doc string) (auth.Policy, []string, bool) {
	d, ok := parseIAMDocument([]byte(doc))
	if !ok {
		return auth.Policy{}, []string{"the bucket policy could not be read"}, false
	}
	type grant struct{ read, write, del bool }
	grants := map[string]*grant{} // prefix, or "=key" for one object
	var notes []string
	// In bin, anonymous read of a prefix also lists it. A policy that lets
	// anyone download files by name, but not list them, cannot be imported:
	// everything in it would become discoverable.
	listAll, listable := false, map[string]bool{}
	for _, st := range d.Statement {
		if !isAnonymousPrincipal(st.Principal) {
			notes = append(notes, "rules for specific users were not imported")
			continue
		}
		if !strings.EqualFold(st.Effect, "Allow") {
			return auth.Policy{}, []string{"the bucket policy uses Deny, which bin cannot express"}, false
		}
		lists := false
		var objectActions []string
		for _, a := range st.Action {
			la := strings.ToLower(a)
			switch la {
			case "*", "s3:*", "s3:list*", "s3:listbucket":
				lists = true
			}
			if la == "s3:getbucketlocation" || strings.HasPrefix(la, "s3:listbucket") {
				continue
			}
			objectActions = append(objectActions, la)
		}
		if lists && len(st.NotResource) == 0 {
			for _, r := range st.Resource {
				b, rest, isAll, ok := splitS3Resource(r)
				if !ok || rest != "" || (!isAll && b != bucket) {
					continue // ListBucket applies to the bucket itself
				}
				if !hasCondition(st.Condition) {
					listAll = true
				} else if prefixes, ok := listPrefixes(st.Condition); ok {
					for _, p := range prefixes {
						listable[p] = true
					}
				}
			}
		}
		if len(objectActions) == 0 {
			continue
		}
		if hasCondition(st.Condition) || len(st.NotAction) > 0 || len(st.NotResource) > 0 {
			return auth.Policy{}, []string{"the bucket policy has conditions, which bin cannot express"}, false
		}
		for _, r := range st.Resource {
			b, rest, _, ok := splitS3Resource(r)
			if !ok || b != bucket {
				continue // another bucket's resource does not apply here
			}
			var key string
			switch {
			case strings.HasSuffix(rest, "*") && !strings.ContainsAny(rest[:len(rest)-1], "*?"):
				key = strings.TrimSuffix(rest, "*")
			case !strings.ContainsAny(rest, "*?") && rest != "":
				key = "=" + rest
			default:
				return auth.Policy{}, []string{"the bucket policy uses a pattern bin cannot express (" + r + ")"}, false
			}
			g := grants[key]
			if g == nil {
				g = &grant{}
				grants[key] = g
			}
			for _, a := range objectActions {
				switch {
				case a == "*" || a == "s3:*":
					g.read, g.write, g.del = true, true, true
				case a == "s3:getobject" || a == "s3:get*":
					g.read = true
				case a == "s3:putobject" || a == "s3:put*":
					g.write = true
				case a == "s3:deleteobject" || a == "s3:delete*":
					g.del = true
				}
			}
		}
	}
	keys := make([]string, 0, len(grants))
	for k := range grants {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rules []auth.Rule
	for _, key := range keys {
		g := grants[key]
		rule := auth.Rule{Prefix: key}
		if strings.HasPrefix(key, "=") {
			rule = auth.Rule{Prefix: key[1:], Exact: true}
		}
		if g.read && !rule.Exact && !listAll && !listable[rule.Prefix] {
			where := "under " + rule.Prefix
			if rule.Prefix == "" {
				where = "in the bucket"
			}
			return auth.Policy{}, []string{"anyone could list the files " + where +
				", which the bucket policy only lets download by name"}, false
		}
		switch {
		case g.read && g.write && g.del:
			rule.Access = auth.ReadWrite
		case g.read && !g.write && !g.del:
			rule.Access = auth.ReadOnly
		case g.write && !g.read && !g.del:
			rule.Access = auth.WriteOnly
		case !g.read && !g.write && !g.del:
			continue
		default:
			return auth.Policy{}, []string{"the bucket policy mixes reading, writing and deleting in a way bin cannot express"}, false
		}
		rules = append(rules, rule)
	}
	switch {
	case len(rules) == 0:
		return auth.Policy{Mode: "private"}, notes, true
	case len(rules) == 1 && rules[0].Prefix == "" && !rules[0].Exact && rules[0].Access == auth.ReadOnly:
		return auth.Policy{Mode: "public"}, notes, true
	}
	return auth.Policy{Mode: "custom", Rules: rules}, notes, true
}

// isAnonymousPrincipal reports whether a statement applies to everyone:
// "*" or {"AWS": "*"} / {"AWS": ["*"]}.
func isAnonymousPrincipal(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == "*"
	}
	var m map[string]stringList
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	aws := m["AWS"]
	return len(m) == 1 && len(aws) == 1 && aws[0] == "*"
}
