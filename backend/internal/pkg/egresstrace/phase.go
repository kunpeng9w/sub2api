// Package egresstrace carries optional phase observations across custom
// transports. It performs no logging, probing, buffering or routing decisions.
package egresstrace

import "context"

type observerKey struct{}
type Observer func(phase string, started bool, err error)

func WithObserver(ctx context.Context, observer Observer) context.Context {
	return context.WithValue(ctx, observerKey{}, observer)
}

// Start returns a completion callback for one transport operation. Callers must
// invoke it once, including on error. Without an observer this is a no-op.
func Start(ctx context.Context, phase string) func(error) {
	observer, _ := ctx.Value(observerKey{}).(Observer)
	if observer == nil {
		return func(error) {}
	}
	observer(phase, true, nil)
	return func(err error) { observer(phase, false, err) }
}
