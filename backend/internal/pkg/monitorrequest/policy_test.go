package monitorrequest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

func TestMonitorProxyProtocolsAndNoRedirect(t *testing.T) {
	for _, protocol := range []string{"http", "https", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://must-not-follow.invalid")
				w.WriteHeader(302)
			})
			var route string
			var base *http.Client
			if protocol == "socks5" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				route = "socks5://" + listener.Addr().String()
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					defer func(){_ = conn.Close()}()
					_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
					hdr := make([]byte, 2)
					if _, err := io.ReadFull(conn, hdr); err != nil {
						return
					}
					methods := make([]byte, int(hdr[1]))
					if _, err := io.ReadFull(conn, methods); err != nil {
						return
					}
					_, _ = conn.Write([]byte{5, 0})
					request := make([]byte, 4)
					if _, err := io.ReadFull(conn, request); err != nil {
						return
					}
					size := 0
					switch request[3] {
					case 1:
						size = 4
					case 4:
						size = 16
					case 3:
						if _, err := io.ReadFull(conn, hdr[:1]); err != nil {
							return
						}
						size = int(hdr[0])
					}
					address := make([]byte, size+2)
					if _, err := io.ReadFull(conn, address); err != nil {
						return
					}
					_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
					buffer := make([]byte, 4096)
					if _, err := conn.Read(buffer); err != nil {
						return
					}
					calls.Add(1)
					_, _ = io.WriteString(conn, "HTTP/1.1 302 Found\r\nLocation: http://must-not-follow.invalid\r\nContent-Length: 0\r\n\r\n")
				}()
				base, err = httpclient.GetClient(httpclient.Options{ProxyURL: route, Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				server := httptest.NewUnstartedServer(handler)
				if protocol == "https" {
					server.StartTLS()
				} else {
					server.Start()
				}
				t.Cleanup(server.Close)
				route = server.URL
				var err error
				base, err = httpclient.GetClient(httpclient.Options{ProxyURL: route, Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				if protocol == "https" {
					transport := base.Transport
					// The production pool uses wrappers; a fixture-only standard proxy transport trusts this fixture CA.
					_ = transport
					pool := x509.NewCertPool()
					pool.AddCert(server.Certificate())
					sourceTransport,ok := server.Client().Transport.(*http.Transport)
                    if !ok {t.Fatal("fixture transport unavailable")}
                    fixture := sourceTransport.Clone()
					fixture.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
					parsed := server.URL
					_ = parsed
					proxyURL, _ := http.NewRequest(http.MethodGet, route, nil)
					fixture.Proxy = http.ProxyURL(proxyURL.URL)
					base = &http.Client{Transport: fixture, Timeout: 3 * time.Second}
				}
			}
			ctx := With(context.Background(), Policy{Route: route, Validate: func(context.Context) error { return nil }})
			client, err := Client(ctx, base, route)
			if err != nil {
				t.Fatal(err)
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.invalid/quota", nil)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 302 || calls.Load() != 1 {
				t.Fatalf("unexpected redirect/query: status=%d calls=%d", response.StatusCode, calls.Load())
			}
			if base.CheckRedirect != nil {
				t.Fatal("shared client mutated")
			}
		})
	}
}
func TestMonitorBrokenBindingNeverDials(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	valid := true
	ctx := With(context.Background(), Policy{Route: server.URL, Validate: func(context.Context) error {
		if !valid {
			return errors.New("binding revoked")
		}
		return nil
	}})
	base, err := httpclient.GetClient(httpclient.Options{ProxyURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client, err := Client(ctx, base, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	valid = false
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://upstream.invalid", nil)
	if _, err := client.Do(req); err == nil || !strings.Contains(err.Error(), "binding revoked") {
		t.Fatalf("want revoked binding, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("revoked binding sent traffic")
	}
	if _, err := Client(ctx, base, ""); err == nil {
		t.Fatal("empty proxy accepted")
	}
	if _, err := Client(ctx, base, "http://alternate.invalid:1234"); err == nil {
		t.Fatal("alternate proxy accepted")
	}
}
