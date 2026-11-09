package egresstrace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPhaseObservationPreservesContextAndError(t *testing.T) {
	Start(context.Background(), "proxy_tunnel")(nil)
	wantErr := errors.New("transport failure")
	var starts, dones int
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithObserver(ctx, func(phase string, started bool, err error) {
		require.Equal(t, "proxy_tunnel", phase)
		if started {
			starts++
			require.NoError(t, err)
		} else {
			dones++
			require.ErrorIs(t, err, wantErr)
		}
	})
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	done := Start(ctx, "proxy_tunnel")
	done(wantErr)
	require.Equal(t, 1, starts)
	require.Equal(t, 1, dones)
}
