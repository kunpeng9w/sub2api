package tlsfingerprint

import (
	"context"
	"net"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/egresstrace"
	"github.com/stretchr/testify/require"
)

func TestFingerprintEgressTraceHandshakeFailure(t *testing.T) {
	client, server := net.Pipe()
	require.NoError(t, server.Close())
	defer func() { _ = client.Close() }()
	var events []struct {
		phase   string
		started bool
		err     error
	}
	ctx := egresstrace.WithObserver(context.Background(), func(phase string, started bool, err error) {
		events = append(events, struct {
			phase   string
			started bool
			err     error
		}{phase, started, err})
	})
	_, err := performTLSHandshake(ctx, client, nil, "example.invalid:443")
	require.Error(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "fingerprint_tls", events[0].phase)
	require.True(t, events[0].started)
	require.False(t, events[1].started)
	require.ErrorIs(t, events[1].err, err)
}
