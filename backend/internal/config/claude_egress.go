package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ClaudeEgressConfig is the opt-in default for official Claude accounts without
// an explicit proxy binding. Bound accounts always retain their own proxy.
type ClaudeEgressConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	ProxyURL string `mapstructure:"proxy_url"`
}

func (c ClaudeEgressConfig) ValidatedProxyURL() (string, error) {
	raw := strings.TrimSpace(c.ProxyURL)
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.Port() == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("gateway.claude_egress.proxy_url must be an HTTP loopback proxy with an explicit port and no credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("gateway.claude_egress.proxy_url must use a loopback IP")
	}
	_, port, err := net.SplitHostPort(u.Host)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("gateway.claude_egress.proxy_url has an invalid port")
	}
	u.Path = ""
	return u.String(), nil
}
