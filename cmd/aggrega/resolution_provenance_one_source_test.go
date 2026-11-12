// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// This file is the one-source proof for resolution provenance. The pull detail
// column and the plugin resolver's diagnostics both name the source that served
// something, and they used to spell the same sources differently
// because each carried its own word list. The tests below fail if either surface
// stops reading the shared vocabulary, which is the only thing keeping them in
// step.
//
// Every expectation here is derived from the shared constants rather than written
// as a literal. That is deliberate and is what makes these tests a wiring proof:
// respell a constant and both surfaces follow it, but hardcode a word at either
// surface and that surface stops matching the constant the other still reads.
// The literal spellings are asserted independently in pull_detail_test.go, which
// stays literal on purpose so both sides are never blinded at once.

// TestPullColumnRendersSharedProvenanceWords proves the pull surface reads the
// shared vocabulary for every resolution tag a Lookup can stamp.
func TestPullColumnRendersSharedProvenanceWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  string
		want closure.ResolutionSource
	}{
		{"registry fetch", closure.ResolutionPathRegistry, closure.SourceFetchedFresh},
		{"cache reuse", closure.ResolutionPathCache, closure.SourceCacheReuse},
		{"local catalog", closure.ResolutionPathCatalog, closure.SourceLocal},
		{"from the binary", closure.ResolutionPathEmbedded, closure.SourceInBinary},
		{"external input", closure.ResolutionPathExternal, closure.SourceExternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pullEventTier(tc.tag); got != string(tc.want) {
				t.Errorf("pullEventTier(%q) = %q; want %q — the column must read the shared vocabulary, not its own copy",
					tc.tag, got, tc.want)
			}
		})
	}
}

// TestPullColumnNeverPrintsAnInternalTag is the regression guard for the defect
// this work repaired: an entity served from inside the binary reached the terminal
// as the raw internal tag, because the column had no word for it and passed
// unrecognised tags through verbatim. Every tag a Lookup stamps must now render as
// a word, so no internal tag spelling can reach a user.
func TestPullColumnNeverPrintsAnInternalTag(t *testing.T) {
	for _, tag := range []string{
		closure.ResolutionPathEmbedded,
		closure.ResolutionPathCatalog,
		closure.ResolutionPathCache,
		closure.ResolutionPathRegistry,
		closure.ResolutionPathExternal,
	} {
		// Recognised, so the column renders a word rather than falling to the
		// passthrough that let the raw tag reach a terminal.
		if _, ok := closure.ResolutionSourceFor(tag); !ok {
			t.Errorf("resolution tag %q has no word, so the column would print it verbatim", tag)
		}
		if got := pullEventTier(tag); strings.Contains(got, "tier-") {
			t.Errorf("pullEventTier(%q) = %q, which carries an internal tag spelling", tag, got)
		}
	}
}

// TestEveryResolverSourceHasAWord proves the plugin resolver's cascade renders a
// word for every source it declares. A source added with no mapping would render
// as the empty string in a diagnostic, naming nothing.
func TestEveryResolverSourceHasAWord(t *testing.T) {
	for _, tier := range []pluginpath.Tier{
		pluginpath.TierEmbed,
		pluginpath.TierColocated,
		pluginpath.TierLocal,
		pluginpath.TierCache,
	} {
		if tier.Source() == "" {
			t.Errorf("resolver source %q renders no word; a diagnostic would name nothing", tier)
		}
	}
}

// TestBothProvenanceSurfacesNameASourceIdentically is the cross-surface half of
// the proof. For every source both axes represent, the word the pull column
// prints and the word the resolver's diagnostics print must be the same value,
// read from the shared vocabulary. Two independently-maintained word lists would
// diverge here the moment either was edited.
func TestBothProvenanceSurfacesNameASourceIdentically(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  string
		tier pluginpath.Tier
	}{
		{"from the binary", closure.ResolutionPathEmbedded, pluginpath.TierEmbed},
		{"found locally", closure.ResolutionPathCatalog, pluginpath.TierLocal},
		{"found locally, beside its entity", closure.ResolutionPathCatalog, pluginpath.TierColocated},
		{"cache reuse", closure.ResolutionPathCache, pluginpath.TierCache},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pullSide := pullEventTier(tc.tag)
			renderSide := string(tc.tier.Source())
			if pullSide != renderSide {
				t.Errorf("the two surfaces name one source differently: pull column says %q, resolver diagnostics say %q",
					pullSide, renderSide)
			}
		})
	}
}

// TestResolverDiagnosticCarriesTheSharedWord reaches the real user-visible string
// rather than the mapper behind it: it drives an actual failing resolution and
// reads the message. The resolver's own spellings must not appear there — only
// the shared word — because those spellings are internal vocabulary the reference
// does not teach.
func TestResolverDiagnosticCarriesTheSharedWord(t *testing.T) {
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(guest, []byte("not a real module"), 0o644); err != nil {
		t.Fatalf("staging the guest: %v", err)
	}

	// A pin the bytes cannot match, so the resolution reaches the digest check
	// and reports which source it resolved from.
	r := pluginpath.NewResolver(
		pluginpath.ResolutionContext{},
		pluginpath.PinPostureLockfile,
		pluginpath.PinMaps{Pins: map[string]string{guest: "sha256:" + strings.Repeat("0", 64)}},
	)

	_, err := r.Resolve(guest)
	if err == nil {
		t.Fatal("a mismatched digest pin must halt the resolution")
	}
	msg := err.Error()

	want := string(pluginpath.TierLocal.Source())
	if !strings.Contains(msg, want) {
		t.Errorf("the diagnostic does not name the source in the shared vocabulary.\n  want to contain: %q\n  got: %s", want, msg)
	}
	if strings.Contains(msg, " local tier") {
		t.Errorf("the diagnostic still names the resolver's internal source spelling: %s", msg)
	}
}
