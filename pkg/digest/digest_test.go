// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package digest_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// TestContentDigestGolden pins the exact wire format against hardcoded literals
// rather than re-deriving the algorithm, so a change to the spelling (uppercase
// hex, a dropped "sha256:" prefix, a different prefix) fails loudly here instead
// of changing the production output and a mirrored test oracle together. The
// literals are over raw byte inputs (toolchain-stable), not compressed
// archives, so they do not drift across Go versions.
func TestContentDigestGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "hello newline",
			in:   []byte("hello\n"),
			want: "sha256:5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03",
		},
		{
			name: "empty input",
			in:   []byte{},
			want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := digest.ContentDigest(tc.in); got != tc.want {
				t.Errorf("ContentDigest(%q): got %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
