// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package waitbound tells a bound an operation sets on its own wait apart from
// its caller withdrawing.
//
// An operation that bounds a wait of its own — a whole acquisition, a walk of a
// listing — runs it under a context with that deadline beneath its caller's. When
// the wait ends early the operation has to say which of the two ended it: its own
// bound is a refusal it names, while a caller that withdrew is reported as the
// stop it is. The test is the same wherever a bound is set, so it has one home.
package waitbound

import (
	"context"
	"errors"
	"time"
)

// Within returns a context that ends after d beneath parent, the function that
// releases it, and a function reporting whether that context ended because d
// elapsed while parent was still live — the bound this call set, rather than the
// caller's own withdrawal or deadline.
func Within(parent context.Context, d time.Duration) (ctx context.Context, cancel context.CancelFunc, expired func() bool) {
	ctx, cancel = context.WithTimeout(parent, d)
	return ctx, cancel, func() bool {
		return parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
}
