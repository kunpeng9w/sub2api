package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/google/uuid"
)

// EgressAuditorProvider shares the same bounded probe cache across HTTP and WS.
// Optional so alternate/test HTTPUpstream implementations remain compatible.
type EgressAuditorProvider interface{ EgressAuditor() *EgressAuditor }

func EgressAuditorFrom(upstream HTTPUpstream) *EgressAuditor {
	if p, ok := upstream.(EgressAuditorProvider); ok {
		return p.EgressAuditor()
	}
	return nil
}

type exitObservation struct {
	IP        string
	ProbeID   string
	CheckedAt time.Time
	Status    string
	pending   bool
}

type EgressAuditor struct {
	managedRoute string
	prober       ProxyExitInfoProber
	mu           sync.Mutex
	cache        map[[32]byte]exitObservation
	slots        chan struct{}
}

func NewEgressAuditor(managedRoute string, prober ProxyExitInfoProber) *EgressAuditor {
	return &EgressAuditor{managedRoute: managedRoute, prober: prober, cache: make(map[[32]byte]exitObservation), slots: make(chan struct{}, 2)}
}

// EgressAuditAttempt is an immutable routing snapshot for one upstream attempt.
// Credentials, headers, query strings and request/response bodies are never logged.
type EgressAuditAttempt struct {
	auditor *EgressAuditor
	route   string
	fields  []any
	started time.Time
	once    sync.Once
	noProbe bool
}

func (a *EgressAuditor) Begin(ctx context.Context, account *Account, route, target, protocol string, extra ...any) *EgressAuditAttempt {
	return a.begin(ctx, account, route, target, protocol, false, extra...)
}

func (a *EgressAuditor) Rejected(ctx context.Context, account *Account, route, target, protocol string) {
	a.begin(ctx, account, route, target, protocol, true, "outcome", "route_rejected")
}

func (a *EgressAuditor) begin(ctx context.Context, account *Account, route, target, protocol string, noProbe bool, extra ...any) *EgressAuditAttempt {
	if a == nil {
		return nil
	}
	name, kind, endpoint := "直连", "direct", ""
	var proxyID any
	accountID, accountName := int64(0), ""
	if account != nil {
		accountID, accountName = account.ID, account.Name
	}
	if route != "" {
		name, kind = "未命名代理", "proxy"
		if _, u, err := proxyurl.Parse(route); err == nil && u != nil {
			endpoint = u.Scheme + "://" + u.Host
		}
		if account != nil && account.ProxyID != nil {
			proxyID = *account.ProxyID
			kind = "account_proxy"
			if account.Proxy != nil && account.Proxy.ID == *account.ProxyID {
				name = account.Proxy.Name
			}
		}
		if a.isMihomo(route) {
			name, kind = "Mihomo", "claude_mihomo"
		}
	}
	host := ""
	if u, err := url.Parse(target); err == nil {
		host = u.Host
	}
	fields := []any{"attempt_id", uuid.NewString(), "account_id", accountID, "account_name", accountName,
		"proxy_id", proxyID, "proxy_name", name, "proxy_endpoint_name", name, "proxy_endpoint", endpoint, "route_kind", kind, "target_host", host, "protocol", protocol}
	for _, key := range []ctxkey.Key{ctxkey.RequestID, ctxkey.ClientRequestID, ctxkey.UserID, ctxkey.APIKeyID, ctxkey.RetryCount, ctxkey.AccountSwitchCount} {
		if v := ctx.Value(key); v != nil {
			labels := map[ctxkey.Key]string{ctxkey.RequestID: "request_id", ctxkey.ClientRequestID: "client_request_id", ctxkey.UserID: "user_id", ctxkey.APIKeyID: "api_key_id", ctxkey.RetryCount: "retry_count", ctxkey.AccountSwitchCount: "account_switch_count"}
			fields = append(fields, labels[key], v)
		}
	}
	if p, ok := monitorrequest.From(ctx); ok {
		fields = append(fields, "monitor_id", p.MonitorID, "actor_id", p.ActorID, "trigger", p.Trigger)
	}
	fields = append(fields, extra...)
	p := &EgressAuditAttempt{auditor: a, route: route, fields: fields, started: time.Now(), noProbe: noProbe || monitorrequest.Active(ctx)}
	if noProbe {
		p.log("upstream_egress_blocked")
	} else {
		p.log("upstream_egress_started")
	}
	return p
}

func (a *EgressAuditor) isMihomo(route string) bool {
	if a.managedRoute == "" {
		return false
	}
	expected, _, err := proxyurl.Parse(a.managedRoute)
	actual, _, routeErr := proxyurl.Parse(route)
	return err == nil && routeErr == nil && actual != "" && actual == expected
}

// observe never waits for network I/O. A separate probe is evidence about that
// probe only, not proof of the NAT address of the model request's connection.
func (a *EgressAuditor) observe(route string) exitObservation {
	if a.isMihomo(route) {
		return exitObservation{Status: "unavailable"}
	} // Claude-only rules reject generic IP-check hosts.
	if a.prober == nil {
		return exitObservation{Status: "unknown"}
	}
	key := sha256.Sum256([]byte(route)) // includes session credentials; never logged
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, exists := a.cache[key]
	ttl := 5 * time.Minute
	if entry.Status == "failed" {
		ttl = 30 * time.Second
	}
	if exists && !entry.CheckedAt.IsZero() && now.Sub(entry.CheckedAt) < ttl {
		return entry
	}
	if entry.pending {
		if entry.IP == "" {
			entry.Status = "pending"
		} else {
			entry.Status = "stale"
		}
		return entry
	}
	if len(a.cache) >= 256 && !exists {
		for k, v := range a.cache {
			if !v.pending {
				delete(a.cache, k)
				break
			}
		}
		if len(a.cache) >= 256 {
			return exitObservation{Status: "unknown"}
		}
	}
	select {
	case a.slots <- struct{}{}:
	default:
		if entry.IP != "" {
			entry.Status = "stale"
		} else {
			entry.Status = "unknown"
		}
		return entry
	}
	entry.pending = true
	entry.ProbeID = uuid.NewString()
	if entry.IP == "" {
		entry.Status = "pending"
	} else {
		entry.Status = "stale"
	}
	a.cache[key] = entry
	go func() {
		defer func() { <-a.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		info, _, err := a.prober.ProbeProxy(ctx, route)
		result := exitObservation{CheckedAt: time.Now(), Status: "failed", ProbeID: entry.ProbeID}
		if err == nil && info != nil && isPublicExitIP(info.IP) {
			result.IP = info.IP
			result.Status = "fresh"
		}
		a.mu.Lock()
		a.cache[key] = result
		a.mu.Unlock()
		endpoint := ""
		if _, u, parseErr := proxyurl.Parse(route); parseErr == nil && u != nil {
			endpoint = u.Scheme + "://" + u.Host
		}
		slog.Info("upstream_egress_probe_completed", "exit_ip_probe_id", result.ProbeID, "proxy_endpoint", endpoint, "exit_ip", result.IP, "exit_ip_status", result.Status, "exit_ip_source", "probe", "exit_ip_checked_at", result.CheckedAt)
	}()
	return entry
}

func (p *EgressAuditAttempt) log(event string, extra ...any) {
	if p == nil {
		return
	}
	obs := exitObservation{Status: "unavailable"}
	if !p.noProbe {
		obs = p.auditor.observe(p.route)
	}
	fields := append([]any{}, p.fields...)
	var checkedAt any
	if !obs.CheckedAt.IsZero() {
		checkedAt = obs.CheckedAt
	}
	fields = append(fields, "exit_ip_probe_id", obs.ProbeID, "exit_ip", obs.IP, "exit_ip_source", "probe", "exit_ip_status", obs.Status, "exit_ip_checked_at", checkedAt, "elapsed_ms", time.Since(p.started).Milliseconds())
	fields = append(fields, extra...)
	slog.Info(event, fields...)
}

func (p *EgressAuditAttempt) Finish(err error) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		outcome := "completed"
		if err != nil {
			outcome = "transport_error"
			if errors.Is(err, context.Canceled) {
				outcome = "canceled"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				outcome = "timeout"
			}
		}
		p.log("upstream_egress_completed", "outcome", outcome)
	})
}

// FinishHTTP records headers separately from body completion, so long streams
// do not appear completed when only the response headers have arrived.
func (p *EgressAuditAttempt) FinishHTTP(resp *http.Response, err error) {
	if p == nil {
		return
	}
	if err != nil {
		p.Finish(err)
		return
	}
	if resp == nil {
		p.Finish(io.ErrUnexpectedEOF)
		return
	}
	p.log("upstream_egress_response", "status_code", resp.StatusCode)
	if resp.Body == nil {
		p.Finish(nil)
		return
	}
	resp.Body = &egressAuditBody{ReadCloser: resp.Body, attempt: p}
}

type egressAuditBody struct {
	io.ReadCloser
	attempt *EgressAuditAttempt
}

func (b *egressAuditBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if err == io.EOF {
		b.attempt.Finish(nil)
	} else if err != nil {
		b.attempt.Finish(err)
	}
	return n, err
}
func (b *egressAuditBody) Close() error {
	err := b.ReadCloser.Close()
	b.attempt.once.Do(func() {
		outcome := "body_closed"
		if err != nil {
			outcome = "close_error"
		}
		b.attempt.log("upstream_egress_completed", "outcome", outcome)
	})
	return err
}

type egressAuditorContextKey struct{}

func withEgressAuditor(ctx context.Context, upstream HTTPUpstream) context.Context {
	return context.WithValue(ctx, egressAuditorContextKey{}, EgressAuditorFrom(upstream))
}

// TraceRequest logs the local socket peer, which is not the public NAT exit.
func (p *EgressAuditAttempt) TraceRequest(req *http.Request) *http.Request {
	if p == nil {
		return req
	}
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if info.Conn == nil {
			return
		}
		local, peer := info.Conn.LocalAddr().String(), info.Conn.RemoteAddr().String()
		id := sha256.Sum256([]byte(local + "|" + peer))
		p.log("upstream_egress_connection", "conn_id", fmt.Sprintf("tcp_%x", id[:8]), "local_endpoint", local, "peer_endpoint", peer, "connection_reused", info.Reused)
	}}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}

func isPublicExitIP(value string) bool {
	ip := net.ParseIP(value)
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
}
