// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package digest_test

import (
	"strings"
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

// TestSegment proves the digest-segment spelling: the colon form renders to the
// dash form so the result is a single filesystem/tag segment (no ":"), and a
// dash-form or bare input is returned unchanged (idempotent — the transform
// never introduces a second dash spelling).
func TestSegment(t *testing.T) {
	t.Parallel()
	const hex = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "colon form to dash form", in: "sha256:" + hex, want: "sha256-" + hex},
		{name: "dash form unchanged", in: "sha256-" + hex, want: "sha256-" + hex},
		{name: "bare hex unchanged", in: hex, want: hex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := digest.Segment(tc.in)
			if got != tc.want {
				t.Errorf("Segment(%q): got %q; want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, ":") {
				t.Errorf("Segment(%q) result %q must not contain ':'", tc.in, got)
			}
		})
	}
}

// TestBareHex proves the three digest spellings in circulation — the colon
// form, the dash form, and bare hex — plus a mixed-case input all collapse to
// the same lowercased bare hex key, so a digest keyed or compared through
// BareHex joins regardless of the spelling or case it arrived in.
func TestBareHex(t *testing.T) {
	t.Parallel()
	const want = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	cases := []struct {
		name string
		in   string
	}{
		{name: "colon form", in: "sha256:5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"},
		{name: "dash form", in: "sha256-5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"},
		{name: "bare hex", in: "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"},
		{name: "mixed case colon form", in: "sha256:5891B5B522D5DF086D0FF0B110FBD9D21BB4FC7163AF34D08286A2E846F6BE03"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := digest.BareHex(tc.in); got != want {
				t.Errorf("BareHex(%q): got %q; want %q", tc.in, got, want)
			}
		})
	}
}
