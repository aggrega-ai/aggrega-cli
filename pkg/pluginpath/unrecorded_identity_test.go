// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// captureUnrecordedAdvisory redirects the advisory stream into a buffer and
// clears the once-per-reference memo, so each case starts from a state in which
// the advisory is free to fire. Without the clear a case would inherit whichever
// references an earlier case already advised on, and its silence would measure
// the memo rather than the rule.
func captureUnrecordedAdvisory(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := unrecordedIdentityWriter
	unrecordedIdentityWriter = &buf
	advisedUnrecorded.Range(func(k, _ any) bool {
		advisedUnrecorded.Delete(k)
		return true
	})
	t.Cleanup(func() {
		unrecordedIdentityWriter = prev
		advisedUnrecorded.Range(func(k, _ any) bool {
			advisedUnrecorded.Delete(k)
			return true
		})
	})
	return &buf
}

// writeLocalGuest writes bytes to a temp file and returns the absolute path, so
// a case can resolve real bytes through the local tier.
func writeLocalGuest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	return path
}

// TestUnrecordedIdentityAdvisory drives the real cascade and asserts what a
// reader is told about the identity of the bytes it resolved.
//
// The rule under test is one branch of one function, and the branch that stays
// SILENT is the half worth guarding: a resolution that says nothing about an
// unverified guest is indistinguishable, from the outside, from one that checked
// it. So every row states which of the two it expects and why, and the fixture
// resolves real bytes through a real tier rather than asserting on a constructed
// Resolver.
func TestUnrecordedIdentityAdvisory(t *testing.T) {
	guest := writeLocalGuest(t, "guest-bytes")
	pin := digest.ContentDigest([]byte("guest-bytes"))

	cases := []struct {
		name    string
		posture PinPosture
		pins    PinMaps
		uri     string
		// advises is whether the reader is told the bytes ran unchecked.
		advises bool
		why     string
	}{
		{
			name:    "pinned posture with no pin for the reference advises",
			posture: PinPostureLockfile,
			pins:    PinMaps{},
			uri:     guest,
			advises: true,
			why:     "the construction consumes recorded digests and this reference has none",
		},
		{
			name:    "pinned posture with a pin for the reference stays silent",
			posture: PinPostureLockfile,
			pins:    PinMaps{Pins: map[string]string{guest: pin}},
			uri:     guest,
			advises: false,
			why:     "the recorded digest governed the bytes, so nothing went unchecked",
		},
		{
			name:    "verify-on-read posture with no pin advises",
			posture: PinPostureVerifyOnRead,
			pins:    PinMaps{},
			uri:     guest,
			advises: true,
			why:     "this posture also consumes recorded digests, from a source other than the lockfile",
		},
		{
			name:    "pin-free posture stays silent",
			posture: PinPostureNone,
			pins:    PinMaps{},
			uri:     guest,
			advises: false,
			why:     "the construction declared that no recorded digest participates",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureUnrecordedAdvisory(t)
			r := NewResolver(ResolutionContext{}, tc.posture, tc.pins)
			if _, err := r.Resolve(tc.uri); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			advised := strings.Contains(buf.String(), "no recorded digest")
			if advised != tc.advises {
				t.Fatalf("advised=%v want %v (%s); output: %q", advised, tc.advises, tc.why, buf.String())
			}
			if tc.advises && !strings.Contains(buf.String(), "aggrega pull") {
				t.Errorf("the advisory does not say what to run to fix it: %q", buf.String())
			}
		})
	}
}

// TestUnrecordedIdentityAdvisorySkipsBytesInsideTheBinary proves the carve-out
// that keeps the advisory usable at all: nothing the compiler records ever stands
// at a built-in's identity, so advising on one would fire on an ordinary,
// fully-resolved build for a condition no operator could repair.
//
// The case is calibrated against the same posture and an ordinary reference,
// because a silence measured with no positive control beside it cannot be told
// from an advisory that never fires.
func TestUnrecordedIdentityAdvisorySkipsBytesInsideTheBinary(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Skip("this build embeds no guests, so the carve-out has nothing to exercise")
	}

	buf := captureUnrecordedAdvisory(t)
	r := NewResolver(ResolutionContext{}, PinPostureLockfile, PinMaps{})
	if _, err := r.Resolve(uris[0]); err != nil {
		t.Fatalf("resolve embedded %q: %v", uris[0], err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("advised on bytes that ship inside the binary: %q", got)
	}

	// Positive control on the same Resolver: an ordinary reference with no pin
	// still advises, so the silence above is the carve-out rather than a rule that
	// never fires.
	guest := writeLocalGuest(t, "other-bytes")
	if _, err := r.Resolve(guest); err != nil {
		t.Fatalf("resolve local: %v", err)
	}
	if !strings.Contains(buf.String(), "no recorded digest") {
		t.Fatalf("the positive control did not advise, so the embed silence measured nothing: %q", buf.String())
	}
}

// TestUnrecordedIdentityAdvisoryIsRaisedOncePerReference proves the advisory does
// not repeat per resolution. A render resolves the same provider for every node
// and a daemon resolves the same attachment on every request, so a per-resolution
// advisory would bury the condition it exists to surface.
func TestUnrecordedIdentityAdvisoryIsRaisedOncePerReference(t *testing.T) {
	buf := captureUnrecordedAdvisory(t)
	guest := writeLocalGuest(t, "repeat-bytes")
	r := NewResolver(ResolutionContext{}, PinPostureLockfile, PinMaps{})

	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(guest); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if n := strings.Count(buf.String(), "no recorded digest"); n != 1 {
		t.Fatalf("three resolutions of one reference raised %d advisories, want 1: %q", n, buf.String())
	}
}
