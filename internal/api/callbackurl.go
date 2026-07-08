package api

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
)

// validateCallbackURL rejects client-supplied callback_url values that could be
// used to make this service issue requests against internal/private network
// targets (SSRF, D-03). Validation runs once at job creation; the resolved
// address is deliberately NOT re-checked before each webhook delivery attempt
// (accepted residual risk — clients are internal-only services, PROJECT.md).
func validateCallbackURL(raw string) error {
	if raw == "" {
		return errors.New("callback_url: empty")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("callback_url: invalid URL")
	}
	if u.Host == "" {
		return errors.New("callback_url: missing host")
	}

	allowInsecureHTTP := os.Getenv("WEBHOOK_ALLOW_INSECURE_HTTP") == "true"
	switch u.Scheme {
	case "https":
		// ok
	case "http":
		if !allowInsecureHTTP {
			return errors.New("callback_url: http scheme not allowed")
		}
	default:
		return errors.New("callback_url: unsupported scheme")
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("callback_url: missing host")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if isBlockedIP(addr) {
			return errors.New("callback_url: blocked address")
		}
		return nil
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return errors.New("callback_url: cannot resolve host")
	}
	for _, a := range addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		if isBlockedIP(addr) {
			return errors.New("callback_url: blocked address")
		}
	}
	return nil
}

// isBlockedIP reports whether addr falls in a range this service refuses to
// deliver webhooks to: loopback, link-local (which covers the 169.254.0.0/16
// cloud metadata endpoint, e.g. 169.254.169.254), or unspecified — these stay
// hard-blocked unconditionally, no configuration can unblock them (D-01).
// RFC1918 private space is blocked by default too, but operators whose
// internal clients live on private addressing can opt in to allowing it via
// WEBHOOK_ALLOW_PRIVATE_IPS=true (D-02/D-03); this narrowly relaxes only the
// private-IP range check, nothing else.
func isBlockedIP(addr netip.Addr) bool {
	allowPrivate := os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"
	return addr.IsLoopback() ||
		(!allowPrivate && addr.IsPrivate()) ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified()
}
