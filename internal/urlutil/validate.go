// Package urlutil provides helpers for validating user-supplied URLs.
package urlutil

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// privateRanges is the list of CIDR blocks that are considered internal.
// Requests to these addresses are blocked to prevent SSRF attacks.
var privateRanges []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",   // IPv4 loopback
		"10.0.0.0/8",    // RFC-1918 private
		"172.16.0.0/12", // RFC-1918 private
		"192.168.0.0/16", // RFC-1918 private
		"169.254.0.0/16", // link-local / AWS metadata (169.254.169.254)
		"100.64.0.0/10", // shared address space (CGNAT)
		"0.0.0.0/8",     // "this" network
		"::1/128",       // IPv6 loopback
		"fc00::/7",      // IPv6 unique-local
		"fe80::/10",     // IPv6 link-local
	}
	for _, cidr := range cidrs {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil {
			privateRanges = append(privateRanges, block)
		}
	}
}

// ValidateWebhookURL checks that rawURL is a safe http(s) address that does
// not point to a private, loopback, link-local, or cloud-metadata endpoint.
// An empty rawURL is allowed (feature is simply disabled).
//
// This prevents SSRF attacks where a user configures a webhook or Chatwoot
// URL pointing to internal infrastructure (Redis, PostgreSQL, cloud metadata, etc.).
func ValidateWebhookURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	// Block known dangerous hostnames regardless of DNS resolution.
	lower := strings.ToLower(host)
	blockedNames := []string{
		"localhost",
		"metadata.google.internal",
		"169.254.169.254", // AWS / Azure / GCP instance metadata
	}
	for _, b := range blockedNames {
		if lower == b {
			return fmt.Errorf("URL points to a blocked host (%s)", b)
		}
	}
	if strings.HasSuffix(lower, ".local") ||
		strings.HasSuffix(lower, ".internal") ||
		strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("URL must not point to an internal host")
	}

	// Resolve hostname and check every resulting IP.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// Cannot resolve → reject for safety.
		return fmt.Errorf("could not resolve hostname %q", host)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		for _, block := range privateRanges {
			if block.Contains(ip) {
				return fmt.Errorf("URL resolves to a private or reserved IP address (%s)", addr)
			}
		}
	}
	return nil
}
