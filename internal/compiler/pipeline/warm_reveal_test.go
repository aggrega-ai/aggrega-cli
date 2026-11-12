// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// warmerFunc is a guestWarmer whose warm is the function itself.
type warmerFunc func(ctx context.Context) error

func (f warmerFunc) Warm(ctx context.Context) error { return f(ctx) }

// TestAWarmIsRevealedOnlyWhenItOutlastsTheRevealDelay pins the build's compile
// reveal: a warm still running after the reveal delay reports the compilation
// once, while it runs, and a warm over at once reports nothing, even after the
// delay has passed.
func TestAWarmIsRevealedOnlyWhenItOutlastsTheRevealDelay(t *testing.T) {
	t.Parallel()
	t.Run("a warm that outlasts the delay is revealed once, while it runs", func(t *testing.T) {
		t.Parallel()
		var revealed atomic.Int32
		seen := make(chan struct{})
		err := warmProviderGuest(t.Context(), warmerFunc(func(context.Context) error {
			select {
			case <-seen:
			case <-time.After(10 * time.Second): // liveness ceiling, not a budget
				t.Error("the warm was never revealed while it ran")
			}
			return nil
		}), func() {
			if revealed.Add(1) == 1 {
				close(seen)
			}
		})
		if err != nil {
			t.Fatalf("warm: %v", err)
		}
		if got := revealed.Load(); got != 1 {
			t.Errorf("revealed %d times, want once", got)
		}
	})
	t.Run("a warm over at once is never revealed", func(t *testing.T) {
		t.Parallel()
		var revealed atomic.Int32
		if err := warmProviderGuest(t.Context(), warmerFunc(func(context.Context) error { return nil }),
			func() { revealed.Add(1) }); err != nil {
			t.Fatalf("warm: %v", err)
		}
		time.Sleep(pluginCompileRevealDelay + 250*time.Millisecond)
		if got := revealed.Load(); got != 0 {
			t.Errorf("a warm over at once was revealed %d times", got)
		}
	})
}
