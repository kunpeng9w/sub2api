package service

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/egresstrace"
)

// These are application/transport observations, not wire bytes or proof that
// the remote application received a request. Trace callbacks may run concurrently.
type egressNetworkMetrics struct {
	mu     sync.Mutex
	stages map[string]*egressNetworkStage

	getConn, gotConn, wroteHeaders, wroteRequest, firstByte, headers time.Time
	firstRead, lastRead                                              time.Time
	finishedAt                                                       time.Time
	writeStarted                                                     time.Time

	requestBytes, responseBytes, reads, writes  int64
	requestContentLength                        *int64
	requestBytesKind                            string
	requestBytesObserved, responseBytesObserved bool

	maxReadGap, writeDuration      time.Duration
	writeDurationObserved          bool
	writeFailures                  int64
	lastErrorClass, lastErrorPhase string
	finished                       bool
}

type egressNetworkStage struct {
	active                     map[string][]time.Time
	count, completed, failures int64
	duration                   time.Duration
}

func (p *EgressAuditAttempt) networkStageStart(stage, key string) {
	if p == nil {
		return
	}
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	if m.stages == nil {
		m.stages = make(map[string]*egressNetworkStage)
	}
	s := m.stages[stage]
	if s == nil {
		s = &egressNetworkStage{active: make(map[string][]time.Time)}
		m.stages[stage] = s
	}
	s.active[key] = append(s.active[key], time.Now())
	s.count++
}

func (p *EgressAuditAttempt) networkStageDone(stage, key string, err error) {
	if p == nil {
		return
	}
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	if s := m.stages[stage]; s != nil {
		starts := s.active[key]
		if len(starts) > 0 {
			s.duration += time.Since(starts[0])
			s.completed++
			if len(starts) == 1 {
				delete(s.active, key)
			} else {
				s.active[key] = starts[1:]
			}
			if err != nil {
				s.failures++
				m.recordError(stage, err)
			}
		}
	}
}

func (m *egressNetworkMetrics) recordError(phase string, err error) {
	if err != nil {
		m.lastErrorPhase, m.lastErrorClass = phase, egressNetworkErrorClass(err)
	}
}

// Never include err.Error(): transport errors may contain URLs or credentials.
func egressNetworkErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection_reset"
	}
	if errors.Is(err, syscall.EPIPE) {
		return "broken_pipe"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	return "transport"
}

func (p *EgressAuditAttempt) networkTraceContext(ctx context.Context) context.Context {
	if p == nil {
		return ctx
	}
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			p.network.mu.Lock()
			defer p.network.mu.Unlock()
			if !p.network.finished {
				p.network.getConn = time.Now()
			}
		},
		DNSStart:          func(httptrace.DNSStartInfo) { p.networkStageStart("dns", "") },
		DNSDone:           func(i httptrace.DNSDoneInfo) { p.networkStageDone("dns", "", i.Err) },
		ConnectStart:      func(network, addr string) { p.networkStageStart("connect", network+"|"+addr) },
		ConnectDone:       func(network, addr string, err error) { p.networkStageDone("connect", network+"|"+addr, err) },
		TLSHandshakeStart: func() { p.networkStageStart("tls", "") },
		TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { p.networkStageDone("tls", "", err) },
		GotConn: func(i httptrace.GotConnInfo) {
			p.network.mu.Lock()
			if !p.network.finished {
				p.network.gotConn = time.Now()
			}
			p.network.mu.Unlock()
			p.logConnection(i)
		},
		WroteHeaders: func() {
			p.network.mu.Lock()
			defer p.network.mu.Unlock()
			if !p.network.finished {
				p.network.wroteHeaders = time.Now()
			}
		},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			p.network.mu.Lock()
			defer p.network.mu.Unlock()
			m := &p.network
			if m.finished {
				return
			}
			m.writes++
			if !m.wroteHeaders.IsZero() {
				m.writeDurationObserved = true
				m.writeDuration += time.Since(m.wroteHeaders)
				m.wroteHeaders = time.Time{}
			}
			if i.Err == nil {
				m.wroteRequest = time.Now()
			} else {
				m.writeFailures++
				m.recordError("request_write", i.Err)
			}
		},
		GotFirstResponseByte: func() {
			p.network.mu.Lock()
			defer p.network.mu.Unlock()
			if !p.network.finished && p.network.firstByte.IsZero() {
				p.network.firstByte = time.Now()
			}
		},
	}
	ctx = egresstrace.WithObserver(ctx, func(phase string, started bool, err error) {
		if started {
			p.networkStageStart(phase, "")
		} else {
			p.networkStageDone(phase, "", err)
		}
	})
	return httptrace.WithClientTrace(ctx, trace)
}

func (p *EgressAuditAttempt) observeRequestBytes(n int) {
	if p == nil {
		return
	}
	p.network.mu.Lock()
	defer p.network.mu.Unlock()
	if !p.network.finished {
		p.network.requestBytesObserved = true
		p.network.requestBytes += int64(n)
	}
}

func (p *EgressAuditAttempt) observeResponseBytes(n int) {
	if p == nil {
		return
	}
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	m.responseBytesObserved = true
	if n <= 0 {
		return
	}
	now := time.Now()
	if m.firstRead.IsZero() {
		m.firstRead = now
	}
	if !m.lastRead.IsZero() && now.Sub(m.lastRead) > m.maxReadGap {
		m.maxReadGap = now.Sub(m.lastRead)
	}
	m.lastRead = now
	m.reads++
	m.responseBytes += int64(n)
}

func (p *EgressAuditAttempt) beginWSWrite() {
	if p == nil {
		return
	}
	p.network.mu.Lock()
	defer p.network.mu.Unlock()
	if !p.network.finished {
		p.network.writeStarted = time.Now()
		p.network.requestBytesKind = "websocket_payload_attempted"
	}
}

func (p *EgressAuditAttempt) endWSWrite(err error) {
	if p == nil {
		return
	}
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	m.writes++
	if !m.writeStarted.IsZero() {
		m.writeDurationObserved = true
		m.writeDuration += time.Since(m.writeStarted)
		m.writeStarted = time.Time{}
	}
	if err == nil {
		m.wroteRequest = time.Now()
	} else {
		m.writeFailures++
		m.recordError("request_write", err)
	}
}

// Freeze before emitting completion: late callbacks cannot mutate its totals.
func (p *EgressAuditAttempt) finishNetwork(err error) {
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finished {
		return
	}
	if err != nil {
		phase := "transport"
		if !m.headers.IsZero() || !m.firstRead.IsZero() {
			phase = "response_read"
		} else if !m.wroteRequest.IsZero() {
			phase = "response_wait"
		}
		// Preserve the more specific callback phase for the same failure class.
		if m.lastErrorClass != egressNetworkErrorClass(err) {
			m.recordError(phase, err)
		}
	}
	m.finishedAt = time.Now()
	m.finished = true
}

func (p *EgressAuditAttempt) networkFields() []any {
	m := &p.network
	m.mu.Lock()
	defer m.mu.Unlock()
	fields := []any{"network_metrics_version", 1}
	ms := func(key string, d time.Duration) { fields = append(fields, key, float64(d.Microseconds())/1000) }
	point := func(key string, t time.Time) {
		if !t.IsZero() {
			ms(key, t.Sub(p.started))
		}
	}
	for _, name := range []string{"dns", "connect", "tls", "proxy_tunnel", "fingerprint_tls"} {
		if s := m.stages[name]; s != nil {
			fields = append(fields, name+"_attempts", s.count, name+"_completed", s.completed, name+"_failures", s.failures)
			ms(name+"_total_ms", s.duration)
			pending := 0
			var pendingDuration time.Duration
			now := m.finishedAt
			if now.IsZero() {
				now = time.Now()
			}
			for _, starts := range s.active {
				pending += len(starts)
				for _, start := range starts {
					pendingDuration += now.Sub(start)
				}
			}
			fields = append(fields, name+"_pending", pending)
			if pending > 0 {
				ms(name+"_pending_total_ms", pendingDuration)
			}
		}
	}
	if !m.getConn.IsZero() && !m.gotConn.IsZero() && !m.gotConn.Before(m.getConn) {
		ms("connection_acquire_ms", m.gotConn.Sub(m.getConn))
	}
	if m.writes > 0 {
		fields = append(fields, "request_write_attempts", m.writes, "request_write_failures", m.writeFailures)
		if m.writeDurationObserved {
			ms("request_write_total_ms", m.writeDuration)
		}
	}
	pendingWrite := m.writeStarted
	if pendingWrite.IsZero() {
		pendingWrite = m.wroteHeaders
	}
	if !pendingWrite.IsZero() {
		now := m.finishedAt
		if now.IsZero() {
			now = time.Now()
		}
		fields = append(fields, "request_write_pending", true)
		ms("request_write_pending_ms", now.Sub(pendingWrite))
	}
	point("request_written_ms", m.wroteRequest)
	point("first_response_byte_ms", m.firstByte)
	point("response_headers_ms", m.headers)
	point("first_response_body_read_ms", m.firstRead)
	if !m.wroteRequest.IsZero() {
		first := m.firstByte
		if first.IsZero() {
			first = m.firstRead
		}
		if !first.IsZero() && !first.Before(m.wroteRequest) {
			ms("wait_after_request_written_ms", first.Sub(m.wroteRequest))
		}
	}
	if m.requestBytesObserved {
		fields = append(fields, "request_body_bytes", m.requestBytes, "request_body_bytes_kind", m.requestBytesKind)
	}
	if m.requestContentLength != nil {
		fields = append(fields, "request_content_length", *m.requestContentLength)
	}
	if m.responseBytesObserved {
		fields = append(fields, "response_body_bytes", m.responseBytes, "response_read_chunks", m.reads)
	}
	if !m.firstRead.IsZero() {
		ms("response_body_observed_ms", m.lastRead.Sub(m.firstRead))
		ms("response_read_max_gap_ms", m.maxReadGap)
	}
	if m.lastErrorClass != "" {
		fields = append(fields, "last_network_error_class", m.lastErrorClass, "last_network_error_phase", m.lastErrorPhase)
	}
	return fields
}

type egressAuditRequestBody struct {
	io.ReadCloser
	attempt *EgressAuditAttempt
}

func (b *egressAuditRequestBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	b.attempt.observeRequestBytes(n)
	return n, err
}

// Clone without consuming/buffering the body; preserve GetBody for transport
// retries and redirects. Counts aggregate all body reads within this attempt.
func (p *EgressAuditAttempt) traceHTTPRequest(req *http.Request) *http.Request {
	if p == nil || req == nil {
		return req
	}
	p.network.mu.Lock()
	length := req.ContentLength
	p.network.requestContentLength = &length
	p.network.requestBytesKind = "http_body_read"
	p.network.mu.Unlock()
	r := req.WithContext(p.networkTraceContext(req.Context()))
	if req.Body != nil && req.Body != http.NoBody {
		r.Body = &egressAuditRequestBody{ReadCloser: req.Body, attempt: p}
	} else {
		p.observeRequestBytes(0)
	}
	if req.GetBody != nil {
		getBody := req.GetBody
		r.GetBody = func() (io.ReadCloser, error) {
			body, err := getBody()
			if err != nil || body == nil {
				return body, err
			}
			return &egressAuditRequestBody{ReadCloser: body, attempt: p}, nil
		}
	}
	return r
}
