package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type egressFrameConn interface {
	ReadFrame(context.Context) (coderws.MessageType, []byte, error)
	WriteFrame(context.Context, coderws.MessageType, []byte) error
	Close() error
}

// Wrap the upstream side, after policy checks. Only response.create frames
// actually written upstream start an attempt; terminal events end that attempt.
type egressAuditFrameConn struct {
	inner                 egressFrameConn
	auditor               *EgressAuditor
	account               *Account
	route, target, connID string
	mu                    sync.Mutex
	turn                  int
	active                *EgressAuditAttempt
}

func newEgressAuditFrameConn(inner egressFrameConn, auditor *EgressAuditor, account *Account, route, target string) *egressAuditFrameConn {
	copy := *account
	if account.Proxy != nil {
		proxyCopy := *account.Proxy
		copy.Proxy = &proxyCopy
	}
	return &egressAuditFrameConn{inner: inner, auditor: auditor, account: &copy, route: route, target: target, connID: "ws_" + uuid.NewString()}
}
func (c *egressAuditFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	var attempt *EgressAuditAttempt
	if gjson.GetBytes(payload, "type").String() == "response.create" {
		c.mu.Lock()
		c.turn++
		attempt = c.auditor.Begin(ctx, c.account, c.route, c.target, "websocket_passthrough", "conn_id", c.connID, "turn_id", c.turn, "connection_reused", c.turn > 1)
		c.active = attempt
		c.mu.Unlock()
	}
	err := c.inner.WriteFrame(ctx, kind, payload)
	if err != nil {
		c.finish(err)
	}
	return err
}
func (c *egressAuditFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, payload, err := c.inner.ReadFrame(ctx)
	if err != nil {
		c.finish(err)
	} else {
		switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
		case "response.completed", "response.done":
			c.finish(nil)
		case "response.failed", "response.incomplete", "error":
			c.finish(errors.New("upstream terminal error"))
		case "response.cancelled", "response.canceled":
			c.finish(context.Canceled)
		}
	}
	return kind, payload, err
}
func (c *egressAuditFrameConn) finish(err error) {
	c.mu.Lock()
	active := c.active
	c.active = nil
	c.mu.Unlock()
	active.Finish(err)
}
func (c *egressAuditFrameConn) Close() error {
	err := c.inner.Close()
	c.finish(context.Canceled)
	return err
}
