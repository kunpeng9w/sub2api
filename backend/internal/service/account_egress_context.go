package service

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

type accountEgressContextKey struct{}
type accountEgressCaptureKey struct{}
type accountEgressContext struct {
	URL string
	Err error
}
type accountEgressCapture struct {
	mu    sync.Mutex
	route accountEgressContext
	set   bool
}

// WithAccountEgress pins batch uploads, polling and downloads to the account's
// route without putting proxy credentials in persisted jobs or public DTOs.
func WithAccountEgress(ctx context.Context, account *Account) (context.Context, error) {
	route, err := ResolveAccountProxyURL(account)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, accountEgressContextKey{}, accountEgressContext{URL: route}), nil
}

func WithAccountEgressCapture(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountEgressCaptureKey{}, &accountEgressCapture{})
}

func RecordAccountEgress(ctx context.Context, route string) {
	if capture, ok := ctx.Value(accountEgressCaptureKey{}).(*accountEgressCapture); ok {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		capture.route = accountEgressContext{URL: route}
		capture.set = true
	}
}

func DetachedAccountEgress(ctx context.Context) context.Context {
	route := accountEgressContext{Err: ErrAccountProxyUnavailable}
	if capture, ok := ctx.Value(accountEgressCaptureKey{}).(*accountEgressCapture); ok {
		capture.mu.Lock()
		if capture.set {
			route = capture.route
		}
		capture.mu.Unlock()
	}
	return context.WithValue(context.Background(), accountEgressContextKey{}, route)
}

func accountEgressProxy(req *http.Request) (*url.URL, error) {
	route, ok := req.Context().Value(accountEgressContextKey{}).(accountEgressContext)
	if !ok {
		return nil, nil
	}
	if route.Err != nil {
		return nil, route.Err
	}
	_, parsed, err := proxyurl.Parse(route.URL)
	return parsed, err
}

type accountEgressBatchTransport struct{}

func (accountEgressBatchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok := req.Context().Value(accountEgressContextKey{}).(accountEgressContext)
	if !ok || route.Err != nil {
		return nil, ErrAccountProxyUnavailable
	}
	client, err := httpclient.GetClient(httpclient.Options{ProxyURL: route.URL, ResponseHeaderTimeout: 60 * time.Second})
	if err != nil {
		return nil, err
	}
	return client.Transport.RoundTrip(req)
}
