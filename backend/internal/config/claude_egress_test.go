package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaudeEgressRequiresExplicitLoopbackProxy(t *testing.T) {
	for _, raw := range []string{"", "http://proxy.invalid:7890", "http://127.0.0.1", "http://user:secret@127.0.0.1:7890", "http://127.0.0.1:0", "http://127.0.0.1:70000", "http://127.0.0.1:abc", "http://127.0.0.1:7890/?proxy=x"} {
		_, err := (ClaudeEgressConfig{Enabled: true, ProxyURL: raw}).ValidatedProxyURL()
		require.Error(t, err, raw)
	}
	for _, raw := range []string{"http://127.0.0.1:7890", "http://[::1]:7890"} {
		_, err := (ClaudeEgressConfig{Enabled: true, ProxyURL: raw}).ValidatedProxyURL()
		require.NoError(t, err)
	}
	require.False(t, (ClaudeEgressConfig{}).Enabled)
}
