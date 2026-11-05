// Package monitorrequest pins monitor-initiated traffic to one validated proxy.
package monitorrequest

import (
 "context"
 "crypto/sha256"
 "fmt"
 "net/http"
)

type key struct{}
type Policy struct {
 Route string
 AccountID int64
 // Validate reloads the binding before each request; it never probes a proxy.
 Validate func(context.Context) error
}
func With(ctx context.Context, p Policy) context.Context { return context.WithValue(ctx, key{}, p) }
func From(ctx context.Context) (Policy, bool) { p, ok := ctx.Value(key{}).(Policy); return p, ok }
func Active(ctx context.Context) bool { _, ok := From(ctx); return ok }
func CacheKey(ctx context.Context) string {
 p, ok := From(ctx); if !ok { return "" }
 return fmt.Sprintf("monitor:%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", p.AccountID, p.Route))))
}
func Check(ctx context.Context, route string) error {
 p, ok := From(ctx); if !ok { return nil }
 if p.Route == "" || route != p.Route { return fmt.Errorf("monitor proxy binding unavailable") }
 if p.Validate == nil { return fmt.Errorf("monitor proxy validation unavailable") }
 return p.Validate(ctx)
}
// Client clones rather than mutates a shared client and rejects redirect hops.
func Client(ctx context.Context, base *http.Client, route string) (*http.Client, error) {
 if err := Check(ctx, route); err != nil { return nil, err }
 if !Active(ctx) { return base, nil }
 c := *base
 c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
 c.Transport = &transport{base: base.Transport, route: route}
 return &c, nil
}
type transport struct { base http.RoundTripper; route string }
func (t *transport) RoundTrip(req *http.Request) (*http.Response,error) {
 if err := Check(req.Context(), t.route); err != nil { return nil, err }
 if t.base == nil { return nil, fmt.Errorf("monitor transport unavailable") }
 return t.base.RoundTrip(req)
}
