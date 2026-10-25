package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Identity providers let people sign in to the console with an OpenID Connect
// provider or an LDAP directory. Their configuration lives in the settings
// table as JSON, with secrets encrypted by the master key; the API never
// returns a secret, only whether one is set. Only members of the configured
// admin group (claim value or group DN) may sign in, with full access, as
// root does. Root can always sign in, so a broken configuration cannot lock
// anyone out.

const (
	settingOpenID = "identity_openid"
	settingLDAP   = "identity_ldap"

	wellKnownSuffix = "/.well-known/openid-configuration"
)

// openIDConfig is the stored OpenID Connect configuration.
type openIDConfig struct {
	Enabled     bool   `json:"enabled"`
	ConfigURL   string `json:"configUrl"` // the provider's .well-known/openid-configuration
	ClientID    string `json:"clientId"`
	SecretEnc   string `json:"clientSecretEnc,omitempty"`
	ClaimName   string `json:"claimName"`   // claim listing the user's groups or roles
	AdminValue  string `json:"adminValue"`  // value of that claim that grants console access
	DisplayName string `json:"displayName"` // label of the sign-in button
	Scopes      string `json:"scopes"`      // space separated
	RedirectURI string `json:"redirectUri"` // empty: <console>/oauth_callback
}

// ldapConfig is the stored LDAP configuration.
type ldapConfig struct {
	Enabled            bool   `json:"enabled"`
	ServerAddr         string `json:"serverAddr"` // host:port
	TLSMode            string `json:"tlsMode"`    // "tls" (ldaps), "starttls" or "none"
	TLSSkipVerify      bool   `json:"tlsSkipVerify"`
	LookupBindDN       string `json:"lookupBindDn"`
	LookupPasswordEnc  string `json:"lookupBindPasswordEnc,omitempty"`
	UserDNSearchBase   string `json:"userDnSearchBase"`
	UserDNSearchFilter string `json:"userDnSearchFilter"` // %s: the username
	GroupSearchBase    string `json:"groupSearchBase"`
	GroupSearchFilter  string `json:"groupSearchFilter"` // %d: the user's DN, %s: the username
	AdminGroupDN       string `json:"adminGroupDn"`
}

func (c openIDConfig) withDefaults() openIDConfig {
	if c.ClaimName == "" {
		c.ClaimName = "groups"
	}
	if c.DisplayName == "" {
		c.DisplayName = "SSO"
	}
	if c.Scopes == "" {
		c.Scopes = "openid profile email"
	}
	return c
}

func (c ldapConfig) withDefaults() ldapConfig {
	if c.TLSMode == "" {
		c.TLSMode = "tls"
	}
	return c
}

// usable reports whether sign-in can be offered: enabled and complete.
func (c openIDConfig) usable() bool {
	return c.Enabled && c.ConfigURL != "" && c.ClientID != "" && c.AdminValue != ""
}

func (c ldapConfig) usable() bool {
	return c.Enabled && c.ServerAddr != "" && c.UserDNSearchBase != "" && c.UserDNSearchFilter != "" &&
		c.GroupSearchBase != "" && c.GroupSearchFilter != "" && c.AdminGroupDN != ""
}

// loadSetting decodes a JSON setting into v; a missing setting leaves v as is.
func (h *Handler) loadSetting(ctx context.Context, key string, v any) error {
	raw, ok, err := h.opts.Store.Setting(ctx, key)
	if err != nil || !ok {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

func (h *Handler) saveSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.opts.Store.SetSetting(ctx, key, string(raw))
}

func (h *Handler) openIDConfig(ctx context.Context) (openIDConfig, error) {
	var c openIDConfig
	err := h.loadSetting(ctx, settingOpenID, &c)
	return c.withDefaults(), err
}

func (h *Handler) ldapConfig(ctx context.Context) (ldapConfig, error) {
	var c ldapConfig
	err := h.loadSetting(ctx, settingLDAP, &c)
	return c.withDefaults(), err
}

// --- console API -------------------------------------------------------------------------

type openIDJSON struct {
	Enabled      bool   `json:"enabled"`
	ConfigURL    string `json:"configUrl"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"` // write only; empty keeps the stored one
	SecretSet    bool   `json:"clientSecretSet"`        // read only
	ClaimName    string `json:"claimName"`
	AdminValue   string `json:"adminValue"`
	DisplayName  string `json:"displayName"`
	Scopes       string `json:"scopes"`
	RedirectURI  string `json:"redirectUri"`
	// DefaultRedirectURI is what to register at the provider when RedirectURI is empty.
	DefaultRedirectURI string `json:"defaultRedirectUri"`
}

func (h *Handler) getOpenID(w http.ResponseWriter, r *http.Request) {
	c, err := h.openIDConfig(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, openIDJSON{
		Enabled: c.Enabled, ConfigURL: c.ConfigURL, ClientID: c.ClientID, SecretSet: c.SecretEnc != "",
		ClaimName: c.ClaimName, AdminValue: c.AdminValue, DisplayName: c.DisplayName, Scopes: c.Scopes,
		RedirectURI: c.RedirectURI, DefaultRedirectURI: consoleBaseURL(r) + oauthCallbackPath,
	})
}

func (h *Handler) putOpenID(w http.ResponseWriter, r *http.Request) {
	var in openIDJSON
	if !readJSON(w, r, &in) {
		return
	}
	old, err := h.openIDConfig(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	c := openIDConfig{
		Enabled: in.Enabled, ConfigURL: strings.TrimSpace(in.ConfigURL), ClientID: strings.TrimSpace(in.ClientID),
		SecretEnc: old.SecretEnc, ClaimName: strings.TrimSpace(in.ClaimName), AdminValue: strings.TrimSpace(in.AdminValue),
		DisplayName: strings.TrimSpace(in.DisplayName), Scopes: strings.Join(strings.Fields(in.Scopes), " "),
		RedirectURI: strings.TrimSpace(in.RedirectURI),
	}
	if in.ClientSecret != "" {
		c.SecretEnc = h.opts.Keys.Box.Encrypt(in.ClientSecret)
	}
	c = c.withDefaults()
	if field, msg := validateOpenID(c); msg != "" {
		writeFieldError(w, "invalid_openid_config", field, msg)
		return
	}
	if err := h.saveSetting(r.Context(), settingOpenID, c); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("openid configuration updated", "enabled", c.Enabled, "config_url", c.ConfigURL, "client_id", c.ClientID)
	h.getOpenID(w, r)
}

// writeFieldError answers 400 naming the JSON field at fault, so the console
// shows the message under that field.
func writeFieldError(w http.ResponseWriter, code, field, message string) {
	if sw, ok := w.(*statusWriter); ok {
		sw.errCode = code // for the audit log
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "message": message, "field": field})
}

// validateOpenID returns the JSON name of the first invalid field and why.
func validateOpenID(c openIDConfig) (field, message string) {
	if c.ConfigURL != "" {
		if _, err := issuerFromConfigURL(c.ConfigURL); err != nil {
			return "configUrl", err.Error()
		}
	}
	if c.RedirectURI != "" {
		if u, err := url.Parse(c.RedirectURI); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "redirectUri", "The redirect URI must be an http:// or https:// URL."
		}
	}
	if !strings.Contains(" "+c.Scopes+" ", " openid ") {
		return "scopes", "The scopes must include openid."
	}
	if c.Enabled {
		for _, f := range []struct{ name, value, label string }{
			{"configUrl", c.ConfigURL, "configuration URL"},
			{"clientId", c.ClientID, "client ID"},
			{"adminValue", c.AdminValue, "admin claim value"},
		} {
			if f.value == "" {
				return f.name, "Required to enable sign-in: enter the " + f.label + "."
			}
		}
	}
	return "", ""
}

type ldapJSON struct {
	Enabled            bool   `json:"enabled"`
	ServerAddr         string `json:"serverAddr"`
	TLSMode            string `json:"tlsMode"`
	TLSSkipVerify      bool   `json:"tlsSkipVerify"`
	LookupBindDN       string `json:"lookupBindDn"`
	LookupPassword     string `json:"lookupBindPassword,omitempty"` // write only; empty keeps the stored one
	LookupPasswordSet  bool   `json:"lookupBindPasswordSet"`        // read only
	UserDNSearchBase   string `json:"userDnSearchBase"`
	UserDNSearchFilter string `json:"userDnSearchFilter"`
	GroupSearchBase    string `json:"groupSearchBase"`
	GroupSearchFilter  string `json:"groupSearchFilter"`
	AdminGroupDN       string `json:"adminGroupDn"`
}

func (h *Handler) getLDAP(w http.ResponseWriter, r *http.Request) {
	c, err := h.ldapConfig(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ldapJSON{
		Enabled: c.Enabled, ServerAddr: c.ServerAddr, TLSMode: c.TLSMode, TLSSkipVerify: c.TLSSkipVerify,
		LookupBindDN: c.LookupBindDN, LookupPasswordSet: c.LookupPasswordEnc != "",
		UserDNSearchBase: c.UserDNSearchBase, UserDNSearchFilter: c.UserDNSearchFilter,
		GroupSearchBase: c.GroupSearchBase, GroupSearchFilter: c.GroupSearchFilter, AdminGroupDN: c.AdminGroupDN,
	})
}

func (h *Handler) putLDAP(w http.ResponseWriter, r *http.Request) {
	c, ok := h.readLDAPConfig(w, r)
	if !ok {
		return
	}
	if err := h.saveSetting(r.Context(), settingLDAP, c); err != nil {
		h.storageError(w, r, err)
		return
	}
	h.opts.Log.Info("ldap configuration updated", "enabled", c.Enabled, "server", c.ServerAddr, "tls", c.TLSMode)
	h.getLDAP(w, r)
}

// readLDAPConfig reads and validates an LDAP configuration from the body,
// keeping the stored lookup password when none is given.
func (h *Handler) readLDAPConfig(w http.ResponseWriter, r *http.Request) (ldapConfig, bool) {
	var in ldapJSON
	if !readJSON(w, r, &in) {
		return ldapConfig{}, false
	}
	old, err := h.ldapConfig(r.Context())
	if err != nil {
		h.storageError(w, r, err)
		return ldapConfig{}, false
	}
	c := ldapConfig{
		Enabled: in.Enabled, ServerAddr: strings.TrimSpace(in.ServerAddr), TLSMode: in.TLSMode,
		TLSSkipVerify: in.TLSSkipVerify, LookupBindDN: strings.TrimSpace(in.LookupBindDN),
		LookupPasswordEnc: old.LookupPasswordEnc, UserDNSearchBase: strings.TrimSpace(in.UserDNSearchBase),
		UserDNSearchFilter: strings.TrimSpace(in.UserDNSearchFilter), GroupSearchBase: strings.TrimSpace(in.GroupSearchBase),
		GroupSearchFilter: strings.TrimSpace(in.GroupSearchFilter), AdminGroupDN: strings.TrimSpace(in.AdminGroupDN),
	}
	if in.LookupPassword != "" {
		c.LookupPasswordEnc = h.opts.Keys.Box.Encrypt(in.LookupPassword)
	}
	c = c.withDefaults()
	if field, msg := validateLDAP(c); msg != "" {
		writeFieldError(w, "invalid_ldap_config", field, msg)
		return ldapConfig{}, false
	}
	return c, true
}

// --- login page --------------------------------------------------------------------------

// loginProviders tells the login page which sign-in methods to offer.
func (h *Handler) loginProviders(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"openid": nil, "ldap": false}
	if c, err := h.openIDConfig(r.Context()); err == nil && c.usable() {
		out["openid"] = map[string]string{"displayName": c.DisplayName}
	}
	if c, err := h.ldapConfig(r.Context()); err == nil && c.usable() {
		out["ldap"] = true
	}
	writeJSON(w, http.StatusOK, out)
}
