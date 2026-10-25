package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP replaces the request's RemoteAddr with the client's address when the
// connection comes from a trusted reverse proxy (BIN_TRUSTED_PROXIES), so the
// audit log, sign-in rate limits, dropzone records and the access log all see
// the person, not the proxy.
//
// The client is the right-most X-Forwarded-For entry that is not itself a
// trusted proxy (entries further left can be forged by the client), or
// X-Real-IP when there is no X-Forwarded-For. Headers from any other source
// are ignored: anyone can send them. With no trusted proxies, RealIP does
// nothing.
func RealIP(trusted []netip.Prefix, next http.Handler) http.Handler {
	if len(trusted) == 0 {
		return next
	}
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.RemoteAddr)
		peer, perr := netip.ParseAddr(host)
		if err != nil || perr != nil || !isTrusted(peer) {
			next.ServeHTTP(w, r)
			return
		}
		client := ""
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			hops := strings.Split(strings.Join(xff, ","), ",")
			for i := len(hops) - 1; i >= 0; i-- {
				a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
				if err != nil {
					break // a malformed hop: trust nothing further left
				}
				client = a.Unmap().String()
				if !isTrusted(a) {
					break
				}
			}
		} else if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			client = a.Unmap().String()
		}
		if client != "" {
			r = r.Clone(r.Context())
			r.RemoteAddr = net.JoinHostPort(client, port)
		}
		next.ServeHTTP(w, r)
	})
}

// ParseTrustedProxies reads a comma or space separated list of IP addresses
// and CIDR ranges, e.g. "10.0.0.0/8, 172.16.0.0/12, ::1".
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
