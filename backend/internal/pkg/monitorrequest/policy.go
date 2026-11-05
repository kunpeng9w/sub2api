// Package monitorrequest pins monitor-initiated traffic to one validated proxy.
package monitorrequest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
)

type key struct{}
type Policy struct {
	MonitorID int64
	ActorID   int64
	Trigger   string
	Revision  string
	Route     string
	AccountID int64
	// Validate reloads the binding before each request; it never probes a proxy.
	Validate       func(context.Context) error
	ValidateTarget func(context.Context, string) error
}

func With(ctx context.Context, p Policy) context.Context { return context.WithValue(ctx, key{}, p) }
func From(ctx context.Context) (Policy, bool)            { p, ok := ctx.Value(key{}).(Policy); return p, ok }
func Active(ctx context.Context) bool                    { _, ok := From(ctx); return ok }
func CacheKey(ctx context.Context) string {
	p, ok := From(ctx)
	if !ok {
		return ""
	}
	return fmt.Sprintf("monitor:%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", p.AccountID, p.Route+":"+p.Revision))))
}
func Check(ctx context.Context, route string) error {
	p, ok := From(ctx)
	if !ok {
		return nil
	}
	if p.Route == "" || route != p.Route {
		return fmt.Errorf("monitor proxy binding unavailable")
	}
	if p.Validate == nil {
		return fmt.Errorf("monitor proxy validation unavailable")
	}
	return p.Validate(ctx)
}

// Client clones rather than mutates a shared client and rejects redirect hops.
func Client(ctx context.Context, base *http.Client, route string) (*http.Client, error) {
	if err := Check(ctx, route); err != nil {
		return nil, err
	}
	if !Active(ctx) {
		return base, nil
	}
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Transport = &transport{base: base.Transport, route: route}
	return &c, nil
}

type transport struct {
	base  http.RoundTripper
	route string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return RoundTrip(req, t.route, t.base)
}

// RoundTrip records only routing identity and status; it never probes an exit IP.
func RoundTrip(r *http.Request, route string, base http.RoundTripper) (*http.Response, error) {
	if err := CheckRequest(r, route); err != nil {
		return nil, err
	}
	if base == nil {
		return nil, fmt.Errorf("monitor transport unavailable")
	}
	p, active := From(r.Context())
	if active {
		slog.Info("channel_monitor_egress_started", "monitor_id", p.MonitorID, "actor_id", p.ActorID, "trigger", p.Trigger, "route_revision", p.Revision, "target_host", r.URL.Host, "method", r.Method)
	}
	response, err := base.RoundTrip(r)
	if active {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		slog.Info("channel_monitor_egress_finished", "monitor_id", p.MonitorID, "actor_id", p.ActorID, "trigger", p.Trigger, "target_host", r.URL.Host, "status", status, "failed", err != nil)
	}
	return response, err
}

func CheckRequest(r *http.Request, route string) error {
	if err := Check(r.Context(), route); err != nil {
		return err
	}
	if p, ok := From(r.Context()); ok && p.ValidateTarget != nil {
		return p.ValidateTarget(r.Context(), r.URL.String())
	}
	return nil
}
