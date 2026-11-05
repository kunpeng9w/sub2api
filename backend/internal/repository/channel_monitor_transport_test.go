package repository

import (
 "context"
 "errors"
 "net/http"
 "net/http/httptest"
 "sync/atomic"
 "testing"

 "github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
 "github.com/Wei-Shaw/sub2api/internal/service"
 "github.com/stretchr/testify/require"
)
func TestMonitorPoolRevisionIsolation(t *testing.T){
 upstream,ok:=NewHTTPUpstream(nil).(*httpUpstreamService);require.True(t,ok)
 one,err:=upstream.getClientEntry("http://proxy.invalid:8080",1,1,service.HTTPUpstreamProfileDefault,false,false,"monitor:revision-one");require.NoError(t,err)
 two,err:=upstream.getClientEntry("http://proxy.invalid:8080",1,1,service.HTTPUpstreamProfileDefault,false,false,"monitor:revision-two");require.NoError(t,err)
 again,err:=upstream.getClientEntry("http://proxy.invalid:8080",1,1,service.HTTPUpstreamProfileDefault,false,false,"monitor:revision-one");require.NoError(t,err)
 require.NotSame(t,one.client,two.client);require.Same(t,one.client,again.client)
 one.client.CloseIdleConnections();two.client.CloseIdleConnections()
}
func TestMonitorTokenRefreshStaysOnProxyAndStopsOnRevocation(t *testing.T){
 var calls atomic.Int64
 proxy:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){calls.Add(1);require.Equal(t,"oauth.invalid",r.Host);w.Header().Set("Content-Type","application/json");_,_ = w.Write([]byte(`{"access_token":"fixture-token","refresh_token":"fixture-refresh","expires_in":3600,"token_type":"Bearer"}`))}));defer proxy.Close()
 revoked:=false
 ctx:=monitorrequest.With(context.Background(),monitorrequest.Policy{Route:proxy.URL,Validate:func(context.Context)error{if revoked{return errors.New("binding revoked")};return nil}})
 oauth:=&openaiOAuthService{tokenURL:"http://oauth.invalid/token"}
 token,err:=oauth.RefreshToken(ctx,"fixture-refresh",proxy.URL);require.NoError(t,err);require.Equal(t,"fixture-token",token.AccessToken);require.Equal(t,int64(1),calls.Load())
 revoked=true;_,err=oauth.RefreshToken(ctx,"fixture-refresh",proxy.URL);require.Error(t,err);require.Equal(t,int64(1),calls.Load())
}
