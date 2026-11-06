package service

import (
	"net/http"

	"github.com/imroc/req/v3"
)

// Clone before decorating: privacy clients are pooled and shared with unrelated
// operations. Auditing belongs to each HTTP attempt, including redirect hops.
func (s *OpenAIQuotaService) auditQuotaClient(base *req.Client, call *openAIQuotaUpstreamCall, queryAccountID int64, purpose, model string) *req.Client {
	if s.egressAuditor == nil {
		return base
	}
	client := base.Clone()
	client.GetTransport().WrapRoundTripFunc(func(rt http.RoundTripper) req.HttpRoundTripFunc {
		return func(r *http.Request) (*http.Response, error) {
			// QueryUsage can also read reset-credit details; those operations
			// are outside the usage/model auditing scope.
			if purpose == "quota_usage" && r.URL.Path != "/backend-api/wham/usage" {
				return rt.RoundTrip(r)
			}
			attempt := s.egressAuditor.Begin(r.Context(), call.account, call.proxyURL, r.URL.String(), "http",
				"method", r.Method, "purpose", purpose, "quota_account_id", queryAccountID, "model", model)
			RecordAccountEgress(r.Context(), call.proxyURL)
			response, err := rt.RoundTrip(attempt.TraceRequest(r))
			attempt.FinishHTTP(response, err)
			return response, err
		}
	})
	return client
}
