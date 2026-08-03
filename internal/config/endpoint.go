package config

import (
	"net"
	"net/url"
	"strings"
)

// loopbackProviders target a local server by design. The SDK's own defaults for
// these enable plain HTTP and loopback addresses (ollama defaults to
// http://localhost:11434/v1), so requiring an opt-in for them would reject the
// documented local-model workflow — including the provider's own default URL.
var loopbackProviders = map[string]bool{
	"ollama":   true,
	"llamacpp": true,
}

// needsPrivateEndpoint reports whether a base URL would reach a plain-HTTP or
// private address, given the provider it belongs to.
//
// This is a configuration-time convenience so the failure names its own fix; it
// is not the security control. The SDK enforces the real policy at request
// time, including for hostnames that only resolve to a private address later —
// which is why a public hostname is not treated as safe here so much as
// not-locally-decidable.
func needsPrivateEndpoint(provider, raw string) bool {
	if loopbackProviders[strings.ToLower(strings.TrimSpace(provider))] {
		return false
	}

	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		// Malformed URLs are reported separately; do not double-report here.
		return false
	}

	if u.Scheme == "http" {
		return true
	}

	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}

	// A bare hostname may still resolve to a private address. That is checked
	// by the SDK at request time, not here: resolving during validation would
	// make config loading depend on DNS and would still be racy.
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return isPrivateIP(ip)
}

// isPrivateIP reports whether an address is one a reviewer should not be
// reaching without an explicit opt-in.
func isPrivateIP(ip net.IP) bool {
	// Cloud metadata services live on link-local addresses and are the classic
	// SSRF target, so they are called out rather than left to IsLinkLocal.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}

	// Carrier-grade NAT, 100.64.0.0/10 — used by Tailscale and some cloud
	// networks, and not covered by IsPrivate.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}

	// IPv4-mapped IPv6 (::ffff:127.0.0.1) would otherwise slip past the checks
	// above when written in its IPv6 form.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(v4) {
		return isPrivateIP(v4)
	}

	return false
}
