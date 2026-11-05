package monitorrequest

import (
	"context"
	"net/http"

	"github.com/imroc/req/v3"
)

// ReqClient clones the transport so a monitoring run never inherits a pooled
// connection from a previous account/proxy revision or changes a shared client.
func ReqClient(ctx context.Context, base *req.Client, route string) (*req.Client, error) {
	if err := Check(ctx, route); err != nil {
		return nil, err
	}
	if !Active(ctx) {
		return base, nil
	}
	client := base.Clone().SetCommonRetryCount(0)
	client.GetClient().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.GetTransport().WrapRoundTripFunc(func(rt http.RoundTripper) req.HttpRoundTripFunc {
		return func(r *http.Request) (*http.Response, error) {
			if err := CheckRequest(r, route); err != nil {
				return nil, err
			}
			return rt.RoundTrip(r)
		}
	})
	return client, nil
}
