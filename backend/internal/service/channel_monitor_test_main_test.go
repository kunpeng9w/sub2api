//go:build unit

package service

import (
	"context"
	"net"
	"os"
	"testing"
)

// CI never resolves real upstream domains to validate synthetic monitor fixtures.
func TestMain(m *testing.M) {
	monitorLookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}
	os.Exit(m.Run())
}
