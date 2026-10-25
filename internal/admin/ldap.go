package admin

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"bin/internal/server"
)

// LDAP sign-in: bind with the lookup account, find the user with the user DN
// search filter, bind as that user with the typed password, then search the
// user's groups. Only members of the admin group get a session.

const ldapTimeout = 10 * time.Second

var (
	errLDAPCredentials = errors.New("wrong username or password")
	errLDAPNotAdmin    = errors.New("not in the admin group")
)

// validateLDAP returns the JSON name of the first invalid field and why.
// complete also requires every field sign-in needs (to save, not to test).
func validateLDAP(c ldapConfig, complete bool) (field, message string) {
	if c.ServerAddr != "" {
		if _, port, err := net.SplitHostPort(c.ServerAddr); err != nil || port == "" {
			return "serverAddr", "Use host:port, e.g. ldap.example.com:636."
		}
	}
	switch c.TLSMode {
	case "tls", "starttls", "none":
	default:
		return "tlsMode", `Choose "tls", "starttls" or "none".`
	}
	if c.UserDNSearchFilter != "" && !strings.Contains(c.UserDNSearchFilter, "%s") {
		return "userDnSearchFilter", "The filter must contain %s, replaced by the username."
	}
	if c.GroupSearchFilter != "" && !strings.Contains(c.GroupSearchFilter, "%d") && !strings.Contains(c.GroupSearchFilter, "%s") {
		return "groupSearchFilter", "The filter must contain %d (the user's DN) or %s (the username)."
	}
	for _, f := range []struct{ name, dn string }{
		{"lookupBindDn", c.LookupBindDN}, {"userDnSearchBase", c.UserDNSearchBase},
		{"groupSearchBase", c.GroupSearchBase}, {"adminGroupDn", c.AdminGroupDN},
	} {
		if f.dn != "" {
			if _, err := ldap.ParseDN(f.dn); err != nil {
				return f.name, "Not a valid DN, e.g. ou=people,dc=example,dc=com."
			}
		}
	}
	if !complete {
		return "", ""
	}
	for _, f := range []struct{ name, value, label string }{
		{"serverAddr", c.ServerAddr, "server address"},
		{"userDnSearchBase", c.UserDNSearchBase, "user DN search base"},
		{"userDnSearchFilter", c.UserDNSearchFilter, "user DN search filter"},
		{"groupSearchBase", c.GroupSearchBase, "group search base"},
		{"groupSearchFilter", c.GroupSearchFilter, "group search filter"},
		{"adminGroupDn", c.AdminGroupDN, "admin group DN"},
	} {
		if f.value == "" {
			return f.name, "Required: enter the " + f.label + "."
		}
	}
	return "", ""
}

// ldapConnect dials the server and binds with the lookup account.
func (h *Handler) ldapConnect(c ldapConfig) (*ldap.Conn, error) {
	host, _, _ := net.SplitHostPort(c.ServerAddr)
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: c.TLSSkipVerify, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: ldapTimeout}
	var conn *ldap.Conn
	var err error
	if c.TLSMode == "tls" {
		conn, err = ldap.DialURL("ldaps://"+c.ServerAddr, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(tlsConf))
	} else {
		conn, err = ldap.DialURL("ldap://"+c.ServerAddr, ldap.DialWithDialer(dialer))
	}
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(ldapTimeout)
	if c.TLSMode == "starttls" {
		if err := conn.StartTLS(tlsConf); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if err := h.ldapLookupBind(conn, c); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (h *Handler) ldapLookupBind(conn *ldap.Conn, c ldapConfig) error {
	if c.LookupBindDN == "" {
		return conn.UnauthenticatedBind("")
	}
	password, err := h.opts.Keys.Box.Decrypt(c.LookupPasswordEnc)
	if err != nil {
		return err
	}
	return conn.Bind(c.LookupBindDN, password)
}

// fillFilter replaces %s and %d with escaped values, so a username can never
// change the filter's structure.
func fillFilter(filter, username, userDN string) string {
	return strings.NewReplacer("%s", ldap.EscapeFilter(username), "%d", ldap.EscapeFilter(userDN)).Replace(filter)
}

// ldapAuthenticate checks a username and password and returns the user's DN
// if the user belongs to the admin group.
func (h *Handler) ldapAuthenticate(c ldapConfig, username, password string) (string, error) {
	// An empty password would make a successful anonymous bind.
	if username == "" || password == "" {
		return "", errLDAPCredentials
	}
	conn, err := h.ldapConnect(c)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	res, err := conn.Search(ldap.NewSearchRequest(c.UserDNSearchBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, int(ldapTimeout/time.Second), false, fillFilter(c.UserDNSearchFilter, username, ""), []string{"dn"}, nil))
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return "", err
	}
	if res == nil || len(res.Entries) != 1 {
		return "", errLDAPCredentials // none, or ambiguous
	}
	userDN := res.Entries[0].DN
	if err := conn.Bind(userDN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return "", errLDAPCredentials
		}
		return "", err
	}

	// Search groups as the lookup account: users often cannot read groups.
	if err := h.ldapLookupBind(conn, c); err != nil {
		return "", err
	}
	groups, err := conn.Search(ldap.NewSearchRequest(c.GroupSearchBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, int(ldapTimeout/time.Second), false, fillFilter(c.GroupSearchFilter, username, userDN), []string{"dn"}, nil))
	if err != nil {
		return "", err
	}
	admin, err := ldap.ParseDN(c.AdminGroupDN)
	if err != nil {
		return "", err
	}
	for _, g := range groups.Entries {
		if dn, err := ldap.ParseDN(g.DN); err == nil && dn.EqualFold(admin) {
			return userDN, nil
		}
	}
	return userDN, errLDAPNotAdmin
}

// loginLDAP signs in with directory credentials: POST {"username","password"}.
func (h *Handler) loginLDAP(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "bad_origin", "Cross-origin request refused.")
		return
	}
	ip := clientIP(r)
	if wait := h.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed sign-in attempts. Try again later.")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	setAuditActor(r, "ldap:"+in.Username)
	c, err := h.ldapConfig(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	if !c.usable() {
		writeError(w, http.StatusNotFound, "ldap_disabled", "LDAP sign-in is not configured.")
		return
	}
	userDN, err := h.ldapAuthenticate(c, in.Username, in.Password)
	switch {
	case errors.Is(err, errLDAPCredentials):
		h.limiter.fail(ip)
		server.Annotate(r.Context(), slog.String("login", "failed"))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong username or password.")
		return
	case errors.Is(err, errLDAPNotAdmin):
		h.opts.Log.Warn("ldap sign-in refused: not in the admin group", "user", userDN)
		writeError(w, http.StatusForbidden, "not_allowed", "Your account is not allowed to use this console.")
		return
	case err != nil:
		h.opts.Log.Error("ldap sign-in failed", "server", c.ServerAddr, "err", err)
		writeError(w, http.StatusBadGateway, "ldap_unreachable", "The directory server cannot be reached or refused the lookup account.")
		return
	}
	h.limiter.reset(ip)
	sess, err := h.startSession(w, r, "ldap:"+in.Username)
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("ldap sign-in", "user", userDN)
	writeJSON(w, http.StatusOK, sess)
}

// testLDAP connects with the configuration in the body (stored password if
// none is given) and, with a username, checks that it is found.
func (h *Handler) testLDAP(w http.ResponseWriter, r *http.Request) {
	c, ok := h.readLDAPConfig(w, r, false)
	if !ok {
		return
	}
	if c.ServerAddr == "" {
		writeFieldError(w, "invalid_ldap_config", "serverAddr", "Enter the server address to test the connection.")
		return
	}
	conn, err := h.ldapConnect(c)
	if err != nil {
		h.opts.Log.Warn("ldap test failed", "server", c.ServerAddr, "err", err)
		writeError(w, http.StatusBadGateway, "ldap_unreachable", "Could not connect and bind with the lookup account: "+ldapErrorText(err))
		return
	}
	conn.Close()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ldapErrorText describes an LDAP failure without echoing server responses
// beyond the standard result code.
func ldapErrorText(err error) string {
	var le *ldap.Error
	if errors.As(err, &le) {
		if text, ok := ldap.LDAPResultCodeMap[le.ResultCode]; ok {
			return text
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	return "connection failed"
}
