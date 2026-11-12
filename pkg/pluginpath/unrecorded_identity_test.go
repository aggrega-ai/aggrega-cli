// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// captureUnrecordedAdvisory returns a buffer and a receiver writing into it, so
// each case observes exactly the advisories its own construction raised. Each
// call returns a fresh receiver, so a case never inherits the references an
// earlier case already advised on, and its silence measures the rule rather than
// a memo.
func captureUnrecordedAdvisory(t *testing.T) (*bytes.Buffer, func(UnrecordedIdentity)) {
	t.Helper()
	var buf bytes.Buffer
	return &buf, ReportUnrecordedTo(&buf)
}

// TestUnrecordedIdentityAdvisoryReachesItsReceiverAndNotStandardError holds the
// advisory to the receiver the construction supplied: the receiver gets it, as a
// structured value naming the reference and its source, and the process's
// standard error gets nothing. A resolution has no business choosing a stream —
// a run records the advisory on its channel and a command writes it where its
// reader looks — so a write of its own would reach a stream nobody reading the
// run's advisories reads.
func TestUnrecordedIdentityAdvisoryReachesItsReceiverAndNotStandardError(t *testing.T) {
	guest := writeLocalGuest(t, "receiver-bytes")
	var got []UnrecordedIdentity
	r := NewResolver(ResolutionContext{Unrecorded: func(a UnrecordedIdentity) { got = append(got, a) }},
		PinPostureLockfile, PinMaps{})

	stderr := captureProcessStderr(t, func() {
		if _, err := r.Resolve(guest); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	})
	if len(got) != 1 || got[0].URI != guest || got[0].Tier != TierLocal {
		t.Fatalf("the receiver got %+v, want one advisory for %q from the local tier", got, guest)
	}
	if !strings.Contains(got[0].Message(), "no recorded digest") {
		t.Errorf("the advisory's sentence does not say what it found: %q", got[0].Message())
	}
	if stderr != "" {
		t.Fatalf("the resolution wrote to standard error: %q", stderr)
	}
}

// captureProcessStderr runs fn with the process's standard error redirected and
// returns what was written to it.
func captureProcessStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	func() {
		defer func() { os.Stderr = saved }()
		fn()
	}()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
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
			buf, report := captureUnrecordedAdvisory(t)
			r := NewResolver(ResolutionContext{Unrecorded: report}, tc.posture, tc.pins)
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

// TestUnrecordedIdentityAdvisorySaysWhatHoldsForItsSource proves the advisory's
// remedy is the one that is true of the source the bytes came from. Bytes read
// from the content cache are an acquisition a pull records, so the advisory names
// that pull. Bytes read from beside a Plugin entity in a catalog directory are
// recorded by a pull only for a plugin attached at a transform role, and the
// resolver cannot tell which role a reference is attached at, so naming the pull
// command would send the reader of any other plugin to a command that changes
// nothing — the advisory says instead that the bytes are read from the catalog on
// every build, that a pull records them only for a transform role, and names
// publishing as the way to have any other plugin recorded. Both rows resolve real
// bytes through the real tier.
func TestUnrecordedIdentityAdvisorySaysWhatHoldsForItsSource(t *testing.T) {
	const uri = "pipeline-step/patch"

	cases := []struct {
		name    string
		sources func(t *testing.T) ResolutionContext
		want    []string
		mustNot []string
	}{
		{
			name: "bytes beside the entity in a catalog directory",
			sources: func(t *testing.T) ResolutionContext {
				dir := writeColocated(t, filepath.Join(t.TempDir(), "catalog", "patch"), uri, []byte("colocated-guest"))
				return ResolutionContext{ColocatedDirs: map[string]ColocatedDir{uri: {Dir: dir}}}
			},
			want:    []string{"resolved from local", "beside its Plugin entity in a catalog directory", "only for a plugin attached at a transform role", "publish the plugin"},
			mustNot: []string{"aggrega pull"},
		},
		{
			name: "bytes from the content cache",
			sources: func(t *testing.T) ResolutionContext {
				ws := t.TempDir()
				if err := oci.WriteCached(ws, ".aggrega/cache", oci.PluginArtifactKind(), uri, "", []byte("cached-guest")); err != nil {
					t.Fatalf("seed cache: %v", err)
				}
				return ResolutionContext{WorkspaceRoot: ws, CacheDir: ".aggrega/cache"}
			},
			want: []string{"resolved from cache", "run `aggrega pull --release <release>` to record it"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, report := captureUnrecordedAdvisory(t)
			sources := tc.sources(t)
			sources.Unrecorded = report
			r := NewResolver(sources, PinPostureLockfile, PinMaps{})
			if _, err := r.Resolve(uri); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got := buf.String()
			if !strings.Contains(got, "no recorded digest") {
				t.Fatalf("no advisory was raised for bytes no recorded digest governs: %q", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("advisory does not say %q: %q", want, got)
				}
			}
			for _, not := range tc.mustNot {
				if strings.Contains(got, not) {
					t.Errorf("advisory names %q, which does not record bytes from this source: %q", not, got)
				}
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

	buf, report := captureUnrecordedAdvisory(t)
	r := NewResolver(ResolutionContext{Unrecorded: report}, PinPostureLockfile, PinMaps{})
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

// TestUnrecordedIdentityAdvisoryIsRaisedOncePerReference proves the terminal
// receiver does not repeat the advisory per resolution. A command resolves the
// same plugin for every node it renders and every command it registers, so a
// per-resolution advisory would bury the condition it exists to surface.
func TestUnrecordedIdentityAdvisoryIsRaisedOncePerReference(t *testing.T) {
	buf, report := captureUnrecordedAdvisory(t)
	guest := writeLocalGuest(t, "repeat-bytes")
	r := NewResolver(ResolutionContext{Unrecorded: report}, PinPostureLockfile, PinMaps{})

	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(guest); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if n := strings.Count(buf.String(), "no recorded digest"); n != 1 {
		t.Fatalf("three resolutions of one reference raised %d advisories, want 1: %q", n, buf.String())
	}
}
