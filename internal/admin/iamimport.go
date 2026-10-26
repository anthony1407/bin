package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"

	"bin/internal/auth"
	"bin/internal/storage"
)

// --- bucket settings --------------------------------------------------------------------

// importBucketSettings copies what bin can keep of a remote bucket's
// configuration onto the local bucket just created: versioning, tags,
// anonymous access and, through the MinIO admin API when adm is set, the
// quota. It returns what was applied and notes on what was not.
func (h *Handler) importBucketSettings(ctx context.Context, client *minio.Client, adm *minioAdmin, bucket string) (applied, notes []string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unreadable := func(what string, err error) {
		switch minio.ToErrorResponse(err).Code {
		case "NotImplemented", "NoSuchTagSet", "NoSuchBucketPolicy", "NoSuchLifecycleConfiguration":
			return // the remote has none, or no such feature
		}
		h.opts.Log.Warn("import: reading bucket settings", "bucket", bucket, "setting", what, "err", err)
		notes = append(notes, "The "+what+" could not be read from the remote server.")
	}

	if v, err := client.GetBucketVersioning(ctx, bucket); err != nil {
		unreadable("versioning", err)
	} else if v.Status == storage.VersioningEnabled || v.Status == storage.VersioningSuspended {
		if err := h.opts.Store.SetBucketVersioning(ctx, bucket, v.Status); err != nil {
			notes = append(notes, "Versioning could not be set.")
		} else {
			applied = append(applied, "versioning "+strings.ToLower(v.Status))
		}
	}

	if t, err := client.GetBucketTagging(ctx, bucket); err != nil {
		unreadable("tags", err)
	} else if m := t.ToMap(); len(m) > 0 {
		if err := h.opts.Store.SetBucketTags(ctx, bucket, m); err != nil {
			notes = append(notes, "The tags could not be set.")
		} else {
			applied = append(applied, fmt.Sprintf("%d tag%s", len(m), plural(len(m))))
		}
	}

	if doc, err := client.GetBucketPolicy(ctx, bucket); err != nil {
		unreadable("bucket policy", err)
	} else if doc != "" {
		policy, policyNotes, ok := anonymousPolicy(bucket, doc)
		notes = append(notes, dedupe(policyNotes)...)
		switch {
		case !ok:
			notes = append(notes, "The bucket was left private.")
		case policy.Mode == "private" || policy.Validate() != nil:
		default:
			if err := h.opts.Store.SetBucketPolicy(ctx, bucket, policy.String()); err != nil {
				notes = append(notes, "The anonymous access could not be set; the bucket was left private.")
				break
			}
			h.opts.Log.Warn("bucket opened to anonymous access by an import", "bucket", bucket, "mode", policy.Mode)
			if policy.Mode == "public" {
				applied = append(applied, "public read access")
			} else {
				applied = append(applied, fmt.Sprintf("anonymous access on %d path%s", len(policy.Rules), plural(len(policy.Rules))))
			}
		}
	}

	if adm != nil {
		// Quotas live in the admin API; servers without one simply have none.
		if q, err := adm.bucketQuota(ctx, bucket); err == nil && q > 0 {
			if err := h.opts.Store.SetBucketQuota(ctx, bucket, q); err == nil {
				applied = append(applied, "quota")
			}
		}
	}

	if lc, err := client.GetBucketLifecycle(ctx, bucket); err != nil {
		unreadable("lifecycle rules", err)
	} else if lc != nil && len(lc.Rules) > 0 {
		notes = append(notes, "Lifecycle rules were not imported: bin does not expire objects.")
	}
	return applied, notes
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dedupe(s []string) []string {
	out := []string{}
	for _, x := range s {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// --- users, groups and policies ---------------------------------------------------------

// importedKey is an access key created from a MinIO user or access key. Its
// secret is new (MinIO never reveals secrets) and appears only in this result.
type importedKey struct {
	AccessKey string   `json:"accessKey"`
	SecretKey string   `json:"secretKey"`
	Kind      string   `json:"kind"`             // "user" or "accessKey"
	Parent    string   `json:"parent,omitempty"` // the user an access key belongs to
	Enabled   bool     `json:"enabled"`
	Groups    []string `json:"groups"`
	Note      string   `json:"note,omitempty"`
}

// importedGroup is a group created from a MinIO policy or group.
type importedGroup struct {
	Name     string   `json:"name"`
	From     string   `json:"from"`   // "policy" or "group"
	Source   string   `json:"source"` // its name on the remote server
	Buckets  []string `json:"buckets"`
	ReadOnly bool     `json:"readOnly"`
	Note     string   `json:"note,omitempty"`
}

type iamSkipped struct {
	Kind   string `json:"kind"` // "policy", "group", "user" or "accessKey"
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type iamImportResult struct {
	Keys    []importedKey   `json:"keys"`
	Groups  []importedGroup `json:"groups"`
	Skipped []iamSkipped    `json:"skipped"`
}

// iamSummary is what the connection test tells about a remote's identities.
type iamSummary struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Users      int    `json:"users"`
	Groups     int    `json:"groups"`
	Policies   int    `json:"policies"`
	AccessKeys int    `json:"accessKeys"`
}

// builtinPolicies come with every MinIO server; they are only imported when
// someone uses them.
var builtinPolicies = []string{"consoleAdmin", "diagnostics", "readonly", "readwrite", "writeonly"}

func summarizeIAM(iam *remoteIAM) iamSummary {
	s := iamSummary{Available: true, Users: len(iam.Users), Groups: len(iam.Groups), AccessKeys: len(iam.ServiceAccounts)}
	for name := range iam.Policies {
		if !slices.Contains(builtinPolicies, name) {
			s.Policies++
		}
	}
	return s
}

// iamErrorMessage explains why identities could not be read.
func iamErrorMessage(err error) string {
	switch {
	case errors.Is(err, errAdminDenied):
		return "These credentials may not read users and policies. Connect with the root user or an admin access key to import them."
	case errors.Is(err, errNoAdminAPI):
		return "The remote server is not MinIO (it has no admin API): only buckets can be imported."
	case errors.Is(err, errBlockedAddress) || errors.Is(err, errInvalidEndpoint):
		_, msg := remoteErrorMessage(err)
		return msg
	}
	return "Users and policies could not be read from the remote server."
}

// validImportedKeyName reports whether a MinIO user or access key name can
// serve as a bin access key: SigV4 credentials may not hold "/" or spaces.
func validImportedKeyName(name string) bool {
	if len(name) < 3 || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == '/' || r == 0x7f {
			return false
		}
	}
	return true
}

// importIAM recreates a MinIO server's identities in bin:
//
//   - a policy becomes the group "policy-<name>", with the policy's buckets
//     and read-only or read-write access, created when someone uses it;
//   - a group with one policy becomes a group of the same name; a group with
//     several gives its members the group of each policy instead;
//   - a user becomes an access key named like it, in the groups above;
//   - a user's access key keeps its ID and gets its user's access, or its
//     own narrower policy.
//
// Secrets cannot be read from MinIO, so every key gets a new one. Whatever
// bin cannot express without granting more is skipped with the reason.
func (h *Handler) importIAM(ctx context.Context, iam *remoteIAM) (iamImportResult, error) {
	res := iamImportResult{Keys: []importedKey{}, Groups: []importedGroup{}, Skipped: []iamSkipped{}}
	skip := func(kind, name, reason string) {
		res.Skipped = append(res.Skipped, iamSkipped{kind, name, reason})
	}
	buckets, err := h.opts.Store.ListBuckets(ctx)
	if err != nil {
		return res, err
	}
	local := map[string]bool{}
	for _, b := range buckets {
		local[b.Name] = true
	}

	// localize keeps a scope to the buckets that exist here.
	localize := func(s auth.Scope) (auth.Scope, string, string, bool) {
		if len(s.Buckets) == 0 {
			return s, "", "", true
		}
		var kept, missing []string
		for _, b := range s.Buckets {
			if local[b] {
				kept = append(kept, b)
			} else {
				missing = append(missing, b)
			}
		}
		if len(kept) == 0 {
			return s, "none of its buckets exist here (import them too)", "", false
		}
		note := ""
		if len(missing) > 0 {
			note = "Buckets not imported were left out: " + strings.Join(missing, ", ") + "."
		}
		s.Buckets = kept
		return s, "", note, true
	}

	type translated struct {
		scope        auth.Scope
		reason, note string
		ok           bool
	}
	policies := map[string]translated{}
	policyScope := func(name string) translated {
		if t, done := policies[name]; done {
			return t
		}
		var t translated
		if doc, exists := iam.Policies[name]; !exists {
			t.reason = "the policy does not exist on the remote server"
		} else if t.scope, t.reason, t.ok = scopeFromIAM(doc); t.ok {
			t.scope, t.reason, t.note, t.ok = localize(t.scope)
		}
		policies[name] = t
		return t
	}

	groupScopes := map[string]auth.Scope{} // bin group -> scope, for access keys
	createGroup := func(name, description string, enabled bool, t translated, from, source string) bool {
		err := h.opts.Store.CreateGroup(ctx, storage.Group{
			Name: name, Description: description, Enabled: enabled, Scope: t.scope.String(),
		})
		if err != nil {
			reason := "it could not be created here"
			if errors.Is(err, storage.ErrGroupExists) {
				reason = "a group named " + name + " already exists here"
			} else {
				h.opts.Log.Error("import: creating group", "group", name, "err", err)
			}
			skip(from, source, reason)
			return false
		}
		groupScopes[name] = t.scope
		buckets := t.scope.Buckets
		if buckets == nil {
			buckets = []string{}
		}
		res.Groups = append(res.Groups, importedGroup{
			Name: name, From: from, Source: source, Buckets: buckets, ReadOnly: t.scope.ReadOnly, Note: t.note,
		})
		return true
	}

	policyGroups := map[string]string{} // policy -> bin group; "" when not created
	policyGroup := func(policy string) string {
		if g, done := policyGroups[policy]; done {
			return g
		}
		policyGroups[policy] = ""
		t := policyScope(policy)
		name := "policy-" + policy
		switch {
		case !t.ok:
			skip("policy", policy, t.reason)
		case !groupName.MatchString(name):
			skip("policy", policy, "its name cannot be used as a group name here")
		case createGroup(name, "Imported from the MinIO policy "+policy+".", true, t, "policy", policy):
			policyGroups[policy] = name
		}
		return policyGroups[policy]
	}

	// MinIO group -> the bin groups its members join.
	memberGroups := map[string][]string{}
	for _, gname := range sortedNames(iam.Groups) {
		g := iam.Groups[gname]
		pols := splitPolicies(g.Policy)
		switch {
		case g.Status == "disabled":
			skip("group", gname, "it is disabled on the remote server, so its members do not get its access")
			continue
		case len(pols) == 0:
			skip("group", gname, "it has no policy, so it grants nothing")
			continue
		case len(pols) == 1 && groupName.MatchString(gname):
			t := policyScope(pols[0])
			if !t.ok {
				skip("group", gname, "its policy "+pols[0]+" cannot be imported: "+t.reason)
				continue
			}
			if createGroup(gname, "Imported from the MinIO group "+gname+" (policy "+pols[0]+").", true, t, "group", gname) {
				memberGroups[gname] = []string{gname}
			}
			continue
		}
		for _, p := range pols {
			if bg := policyGroup(p); bg != "" {
				memberGroups[gname] = append(memberGroups[gname], bg)
			}
		}
	}

	createKey := func(kind, accessKey, name, parent string, enabled bool, expires *time.Time, groups []string, scope *auth.Scope, note string) {
		switch {
		case !validImportedKeyName(accessKey):
			skip(kind, accessKey, "the name cannot be used as an access key here")
			return
		case accessKey == h.opts.Keys.RootAccessKey:
			skip(kind, accessKey, "it is the same as bin's root access key")
			return
		case expires != nil && !expires.After(time.Now()):
			skip(kind, accessKey, "it has expired")
			return
		}
		if len(groups) == 0 && scope == nil {
			// Without groups a key has its own scope, and an empty one is full
			// access: never hand that out by accident.
			enabled = false
			note = "No access bin can grant: it was created disabled. Add it to a group before enabling it."
		}
		_, secret := auth.NewCredentials()
		k := storage.AccessKey{
			AccessKey: accessKey, SecretEnc: h.opts.Keys.Box.Encrypt(secret), Name: name, Enabled: enabled,
		}
		if scope != nil && len(groups) == 0 {
			k.Scope = scope.String()
		}
		if expires != nil {
			k.Expires = *expires
		}
		if err := h.opts.Store.CreateAccessKey(ctx, k); err != nil {
			reason := "it could not be created here"
			if errors.Is(err, storage.ErrAccessKeyExists) {
				reason = "an access key named " + accessKey + " already exists here"
			} else {
				h.opts.Log.Error("import: creating access key", "access_key", accessKey, "err", err)
			}
			skip(kind, accessKey, reason)
			return
		}
		if len(groups) > 0 {
			if err := h.opts.Store.SetKeyGroups(ctx, accessKey, groups); err != nil {
				h.opts.Store.DeleteAccessKey(ctx, accessKey) // never leave a key with the wrong access
				h.opts.Log.Error("import: setting key groups", "access_key", accessKey, "err", err)
				skip(kind, accessKey, "its groups could not be set")
				return
			}
		}
		h.opts.Log.Info("access key imported", "access_key", accessKey, "kind", kind, "groups", len(groups), "enabled", enabled)
		if groups == nil {
			groups = []string{}
		}
		res.Keys = append(res.Keys, importedKey{
			AccessKey: accessKey, SecretKey: secret, Kind: kind, Parent: parent, Enabled: enabled, Groups: groups, Note: note,
		})
	}

	// Users, and the bin groups that carry each one's access.
	members := map[string][]string{} // user -> MinIO groups, from both sides
	for gname, g := range iam.Groups {
		for _, m := range g.Members {
			members[m] = append(members[m], gname)
		}
	}
	userGroups := map[string][]string{}
	for _, name := range sortedNames(iam.Users) {
		u := iam.Users[name]
		var groups []string
		for _, p := range splitPolicies(u.PolicyName) {
			if bg := policyGroup(p); bg != "" {
				groups = append(groups, bg)
			}
		}
		for _, g := range append(slices.Clone(u.MemberOf), members[name]...) {
			groups = append(groups, memberGroups[g]...)
		}
		slices.Sort(groups)
		groups = slices.Compact(groups)
		userGroups[name] = groups
		createKey("user", name, name, "", u.Status != "disabled", nil, groups, nil, "")
	}

	// Access keys of users ("service accounts").
	for _, sa := range iam.ServiceAccounts {
		_, isUser := iam.Users[sa.ParentUser]
		rootParent := !isUser && sa.ParentUser == iam.Caller // the root user: full access
		enabled := sa.AccountStatus != "off" && sa.AccountStatus != "disabled"
		name := sa.Name
		if name == "" {
			name = "Access key of " + sa.ParentUser
		}
		if !isUser && !rootParent {
			skip("accessKey", sa.AccessKey, "its user "+sa.ParentUser+" is not a built-in MinIO user")
			continue
		}
		if sa.ImpliedPolicy {
			if rootParent {
				full := auth.Scope{}
				createKey("accessKey", sa.AccessKey, name, sa.ParentUser, enabled, sa.Expiration, nil, &full,
					"Full access, like its user, MinIO's root user.")
			} else {
				createKey("accessKey", sa.AccessKey, name, sa.ParentUser, enabled, sa.Expiration, userGroups[sa.ParentUser], nil, "")
			}
			continue
		}
		own, reason, ok := scopeFromIAM([]byte(sa.Policy))
		note := ""
		if ok {
			own, reason, note, ok = localize(own)
		}
		if !ok {
			skip("accessKey", sa.AccessKey, "its policy cannot be imported: "+reason)
			continue
		}
		// MinIO grants the key what both its policy and its user allow.
		if !rootParent {
			parent := userGroups[sa.ParentUser]
			if len(parent) != 1 {
				skip("accessKey", sa.AccessKey, "its user's access comes from "+fmt.Sprint(len(parent))+
					" groups here; bin cannot narrow that to the key's own policy")
				continue
			}
			if own, ok = intersectScopes(own, groupScopes[parent[0]]); !ok {
				skip("accessKey", sa.AccessKey, "its policy and its user's have no bucket in common")
				continue
			}
		}
		createKey("accessKey", sa.AccessKey, name, sa.ParentUser, enabled, sa.Expiration, nil, &own, note)
	}
	return res, nil
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
