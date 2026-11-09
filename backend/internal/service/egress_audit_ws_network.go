package service

import (
	"context"
	"encoding/json"
	"fmt"
)

type egressWriteAuditKey struct{}

func withEgressWriteAudit(ctx context.Context, audit *EgressAuditAttempt) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, egressWriteAuditKey{}, audit)
}

func egressWriteAuditFromContext(ctx context.Context) *EgressAuditAttempt {
	if ctx == nil {
		return nil
	}
	audit, _ := ctx.Value(egressWriteAuditKey{}).(*EgressAuditAttempt)
	return audit
}

type egressAuditJSONWriter struct {
	audit *EgressAuditAttempt
	write func([]byte) error
}

func (w egressAuditJSONWriter) Write(payload []byte) (int, error) {
	// Encoded payload size includes the Encoder newline, as wsjson.Write does.
	// A failed WS write has unknown partial wire delivery; this is attempted data.
	w.audit.observeRequestBytes(len(payload))
	if err := w.write(payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

// Match wsjson.Write's Encoder, one-frame write and error wrapping. Do not log
// serialization error text, which can be supplied by a custom JSON marshaler.
func writeEgressAuditJSON(value any, audit *EgressAuditAttempt, write func([]byte) error) error {
	if err := json.NewEncoder(egressAuditJSONWriter{audit: audit, write: write}).Encode(value); err != nil {
		return fmt.Errorf("failed to write JSON message: failed to marshal JSON: %w", err)
	}
	return nil
}
