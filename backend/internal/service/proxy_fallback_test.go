//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkProxy(id int64, mode string, backup *int64, expiresInDays *int, now time.Time) Proxy {
	p := Proxy{ID: id, Status: StatusActive, FallbackMode: mode, BackupProxyID: backup}
	if expiresInDays != nil {
		t := now.AddDate(0, 0, *expiresInDays)
		p.ExpiresAt = &t
	}
	return p
}
func i64(v int64) *int64 { return &v }
func di(v int) *int      { return &v }

func TestResolveFallbackTarget(t *testing.T) {
	now := time.Now()
	for _, mode := range []string{FallbackModeNone, FallbackModeDirect, FallbackModeProxy} {
		t.Run(mode, func(t *testing.T) {
			a := mkProxy(1, mode, i64(2), di(-1), now)
			backup := mkProxy(2, FallbackModeNone, nil, di(30), now)
			target, change := ResolveProxyFallbackTarget(a, map[int64]Proxy{1: a, 2: backup}, now)
			require.False(t, change)
			require.Nil(t, target)
		})
	}
}
