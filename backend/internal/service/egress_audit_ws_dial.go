package service

import (
	"context"
	"errors"
	"net/http"
)

// Audit each physical dial separately from response turns, including retries
// and pool prewarming. Failed handshakes never reach the per-turn audit.
func dialWithEgressAudit(ctx context.Context, dialer openAIWSClientDialer, auditor *EgressAuditor, account *Account, wsURL string, headers http.Header, proxyURL, protocol string) (openAIWSClientConn, int, http.Header, error) {
	attempt := auditor.Begin(ctx, account, proxyURL, wsURL, protocol, "phase", "dial")
	conn, status, responseHeaders, err := dialer.Dial(attempt.networkTraceContext(ctx), wsURL, headers, proxyURL)
	if status > 0 {
		attempt.log("upstream_egress_response", "status_code", status)
	}
	auditErr := err
	if conn == nil && auditErr == nil {
		auditErr = errors.New("websocket dialer returned nil connection")
	}
	attempt.Finish(auditErr)
	return conn, status, responseHeaders, err
}
