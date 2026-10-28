// Package claudedomain owns the official-domain list used by application and network policy.
package claudedomain

import (
	_ "embed"
	"net/url"
	"strings"
)

//go:embed domains.txt
var domains string

func Domains() []string { return strings.Fields(domains) }

func ContainsHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, domain := range strings.Fields(domains) {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func ContainsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && ContainsHost(u.Hostname())
}
