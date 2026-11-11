package admin

import (
	"context"
	"net/http"
	"time"

	"bin/internal/auth"
	"bin/internal/storage"
)

// Console sessions are of two kinds. Admin sessions (root, LDAP and OpenID
// sign-ins) may do anything. User sessions belong to an access key whose user
// signed in with its console password: they are limited to that key's access
// (its scope or its groups', plus the buckets it created), exactly what the
// key may do through the S3 API, and never reach the administration pages.
//
// Routes are admin-only unless registered for users with what they need on
// the {bucket} of their path: a route added later is closed to users until
// someone decides otherwise.

// need is what a route open to users requires on the bucket in its path.
type need int

const (
	needSession need = iota // any session; the handler checks what it touches
	needRead                // read objects of {bucket}
	needWrite               // write or delete objects of {bucket}
)

type grantsCtxKey struct{}

// sessionOf returns the request's console session.
func sessionOf(r *http.Request) storage.Session {
	s, _ := r.Context().Value(sessionKey{}).(storage.Session)
	return s
}

// userGrants returns the access of a user session; ok is false for admins.
func userGrants(r *http.Request) (auth.Grants, bool) {
	g, ok := r.Context().Value(grantsCtxKey{}).(auth.Grants)
	return g, ok
}

// may reports whether the session may read (write=false) or write objects of
// bucket. Admins may always.
func may(r *http.Request, bucket string, write bool) bool {
	g, user := userGrants(r)
	if !user {
		return true
	}
	if write {
		return g.Allows(auth.WriteObject, bucket)
	}
	return g.Allows(auth.ReadObject, bucket)
}

func denyUser(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "access_denied", "Your account has no access to this.")
}

// adminOnly refuses user sessions.
func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, user := userGrants(r); user {
			denyUser(w)
			return
		}
		next(w, r)
	}
}

// forUsers lets user sessions through when they have what n needs on the
// path's bucket. A missing bucket is refused like a forbidden one, so its
// existence is not revealed.
func forUsers(n need, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if n != needSession && !may(r, r.PathValue("bucket"), n == needWrite) {
			denyUser(w)
			return
		}
		next(w, r)
	}
}

// userSession checks that a user session's key may still sign in and loads
// its access. A key disabled, expired, deleted or without a password since
// the sign-in ends the session at once.
func (h *Handler) userSession(ctx context.Context, sess storage.Session) (auth.Grants, bool) {
	k, err := h.opts.Store.GetAccessKey(ctx, sess.AccessKey)
	if err != nil || !k.Enabled || k.Internal || k.PasswordHash == "" ||
		(!k.Expires.IsZero() && time.Now().After(k.Expires)) {
		return nil, false
	}
	g, err := h.opts.Keys.Grants(ctx, sess.AccessKey)
	return g, err == nil
}
