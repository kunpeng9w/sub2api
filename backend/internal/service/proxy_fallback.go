package service

import "time"

// ResolveProxyFallbackTarget retains the compatibility signature but never changes
// account bindings. Recovery is an explicit administrator action.
func ResolveProxyFallbackTarget(start Proxy, byID map[int64]Proxy, now time.Time) (*int64, bool) {
	return nil, false
}
