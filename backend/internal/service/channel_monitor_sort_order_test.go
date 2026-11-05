//go:build unit

package service

import (
 "testing"

 "github.com/stretchr/testify/require"
)

func TestChannelMonitorSortOrderUpdate(t *testing.T) {
 m := &ChannelMonitor{Provider: "openai", CheckMode: "probe", Endpoint: "https://upstream.invalid", PrimaryModel: "model", SortOrder: 20}
 require.NoError(t, applyMonitorUpdate(m, ChannelMonitorUpdateParams{}))
 require.Equal(t, 20, m.SortOrder)
 for _, value := range []int{10, 0, 2147483647} {
  require.NoError(t, applyMonitorUpdate(m, ChannelMonitorUpdateParams{SortOrder: &value}))
  require.Equal(t, value, m.SortOrder)
 }
 for _, value := range []int{-1, 2147483648} {
  require.ErrorIs(t, applyMonitorUpdate(m, ChannelMonitorUpdateParams{SortOrder: &value}), ErrChannelMonitorInvalidSortOrder)
  require.Equal(t, 2147483647, m.SortOrder)
  require.ErrorIs(t, validateCreateParams(ChannelMonitorCreateParams{SortOrder: value}), ErrChannelMonitorInvalidSortOrder)
 }
}
