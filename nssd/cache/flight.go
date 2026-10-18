package cache

import (
	"context"
	"fmt"

	"golang.org/x/sync/singleflight"
)

// Flight runs at most one fetch per key at a time and lets callers stop
// waiting without stopping the fetch. The zero value is ready to use.
type Flight struct {
	group singleflight.Group
}

// Await runs fn for key, or joins the run already in flight for it, and
// waits for its result until ctx ends. fn runs on a goroutine of its own
// and is never cancelled by ctx: a fetch still running when the caller's
// answer deadline passes finishes and stores its result for the next
// caller (README U5). When ctx ends first, Await returns an error wrapping
// ErrStillRunning.
func Await[T any](ctx context.Context, f *Flight, key string, fn func() (T, error)) (T, error) {
	ch := f.group.DoChan(key, func() (any, error) { return fn() })
	select {
	case res := <-ch:
		// Val is fn's T, boxed; it fails to assert only for a nil
		// interface value, which is T's zero value anyway.
		v, ok := res.Val.(T)
		if !ok {
			var zero T
			return zero, res.Err
		}
		return v, res.Err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("%w: %w", ErrStillRunning, ctx.Err())
	}
}
