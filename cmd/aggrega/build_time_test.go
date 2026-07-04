// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"testing"
	"time"
)

// TestFormatBuildDuration pins the summary's build-time rendering: whole
// milliseconds below one second, seconds to one decimal at or above one second,
// and the boundary exactly at one second crossing from the ms form to the s form.
func TestFormatBuildDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "typical-sub-second", d: 87 * time.Millisecond, want: "87 ms"},
		{name: "typical-multi-second", d: 1400 * time.Millisecond, want: "1.4 s"},
		{name: "just-below-one-second", d: 999 * time.Millisecond, want: "999 ms"},
		{name: "exactly-one-second", d: 1000 * time.Millisecond, want: "1.0 s"},
		{name: "just-above-one-second", d: 1001 * time.Millisecond, want: "1.0 s"},
		{name: "zero", d: 0, want: "0 ms"},
		{name: "sub-millisecond", d: 500 * time.Microsecond, want: "0 ms"},
		{name: "one-millisecond", d: 1 * time.Millisecond, want: "1 ms"},
		{name: "rounds-to-one-decimal", d: 2450 * time.Millisecond, want: "2.5 s"},
		{name: "large", d: 65 * time.Second, want: "65.0 s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBuildDuration(tc.d); got != tc.want {
				t.Errorf("formatBuildDuration(%s) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}
