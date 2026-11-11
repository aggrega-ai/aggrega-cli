// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package waitbound

import (
	"context"
	"testing"
	"time"
)

// TestTheBoundIsToldApartFromTheCallersWithdrawal drives the three ways a bounded
// wait ends — its own bound, its caller withdrawing, its caller's own deadline —
// and the one way it has not ended, and reads which of them reports the bound.
func TestTheBoundIsToldApartFromTheCallersWithdrawal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// run returns whether the bounded context reported its own bound.
		run  func(t *testing.T) bool
		want bool
	}{
		{
			name: "the bound elapsing while the caller is live is the bound",
			run: func(t *testing.T) bool {
				ctx, cancel, expired := Within(t.Context(), time.Millisecond)
				defer cancel()
				<-ctx.Done()
				return expired()
			},
			want: true,
		},
		{
			name: "the caller withdrawing is not the bound",
			run: func(t *testing.T) bool {
				parent, stop := context.WithCancel(t.Context())
				ctx, cancel, expired := Within(parent, time.Hour)
				defer cancel()
				stop()
				<-ctx.Done()
				return expired()
			},
			want: false,
		},
		{
			name: "the caller's own deadline is not the bound",
			run: func(t *testing.T) bool {
				parent, stop := context.WithTimeout(t.Context(), time.Millisecond)
				defer stop()
				ctx, cancel, expired := Within(parent, time.Hour)
				defer cancel()
				<-ctx.Done()
				return expired()
			},
			want: false,
		},
		{
			name: "a wait not yet ended has not reached the bound",
			run: func(t *testing.T) bool {
				_, cancel, expired := Within(t.Context(), time.Hour)
				defer cancel()
				return expired()
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.run(t); got != tc.want {
				t.Errorf("expired() = %v, want %v", got, tc.want)
			}
		})
	}
}
