// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "testing"

func TestMatchImplements_ExactMatchWins(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server:1.0.0":  "exact",
		"core/api-server:^1.0.0": "caret",
		"core/api-server":        "unversioned",
	}

	got, ok := matchImplements(index, "core/api-server:1.0.0")
	if !ok {
		t.Fatal("expected match, got none")
	}
	if got != "exact" {
		t.Errorf("expected exact match precedence, got %q", got)
	}
}

func TestMatchImplements_CaretMatchInRange(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server:^1.0.0": "caret-1.0",
	}

	got, ok := matchImplements(index, "core/api-server:1.5.3")
	if !ok {
		t.Fatal("caller version 1.5.3 should satisfy ^1.0.0 caret")
	}
	if got != "caret-1.0" {
		t.Errorf("expected caret-1.0, got %q", got)
	}
}

func TestMatchImplements_CaretMatchMultipleHighestBaselineWins(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server:^1.0.0": "caret-1.0.0-baseline",
		"core/api-server:^1.2.0": "caret-1.2.0-baseline",
	}

	// 1.5.0 satisfies both ^1.0.0 and ^1.2.0; the higher baseline (^1.2.0)
	// wins as the more-specific match.
	got, ok := matchImplements(index, "core/api-server:1.5.0")
	if !ok {
		t.Fatal("expected caret match, got none")
	}
	if got != "caret-1.2.0-baseline" {
		t.Errorf("expected highest-baseline caret to win, got %q", got)
	}
}

func TestMatchImplements_CaretOutOfRange(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server:^1.0.0": "caret-1.0",
	}

	// 2.0.0 is outside the major-1 range that ^1.0.0 expresses.
	_, ok := matchImplements(index, "core/api-server:2.0.0")
	if ok {
		t.Error("expected miss; 2.0.0 should not satisfy ^1.0.0")
	}
}

func TestMatchImplements_UnversionedFallback(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server": "unversioned",
	}

	got, ok := matchImplements(index, "core/api-server:1.0.0")
	if !ok {
		t.Fatal("expected unversioned fallback to match")
	}
	if got != "unversioned" {
		t.Errorf("expected unversioned, got %q", got)
	}
}

func TestMatchImplements_PrecedenceExactBeatsCaretBeatsUnversioned(t *testing.T) {
	t.Parallel()

	full := map[string]string{
		"core/api-server:1.0.0":  "exact",
		"core/api-server:^1.0.0": "caret",
		"core/api-server":        "unversioned",
	}

	caretAndUnversioned := map[string]string{
		"core/api-server:^1.0.0": "caret",
		"core/api-server":        "unversioned",
	}

	unversionedOnly := map[string]string{
		"core/api-server": "unversioned",
	}

	cases := []struct {
		name      string
		index     map[string]string
		callerURI string
		want      string
	}{
		// 1.0.0 exactly matches the exact key; exact wins.
		{"exact wins when present", full, "core/api-server:1.0.0", "exact"},
		// 1.5.0 doesn't exact-match 1.0.0; caret iteration wins over
		// unversioned fallback.
		{"caret wins over unversioned", caretAndUnversioned, "core/api-server:1.5.0", "caret"},
		// Only unversioned in index; falls through to unversioned.
		{"unversioned-only fallback", unversionedOnly, "core/api-server:1.5.0", "unversioned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchImplements(tc.index, tc.callerURI)
			if !ok {
				t.Fatalf("expected match, got none")
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestMatchImplements_UnversionedCallerNoVersionedMatch(t *testing.T) {
	t.Parallel()

	// When the caller's URI carries no version segment, only an unversioned
	// index key can match. Versioned + caret keys do not match.
	index := map[string]string{
		"core/api-server:1.0.0":  "exact",
		"core/api-server:^1.0.0": "caret",
	}

	_, ok := matchImplements(index, "core/api-server")
	if ok {
		t.Error("expected miss; unversioned caller has no unversioned key to match")
	}
}

func TestMatchImplements_MissEntirely(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"core/api-server:1.0.0": "exact",
	}

	_, ok := matchImplements(index, "different/component:1.0.0")
	if ok {
		t.Error("expected miss for unrelated base")
	}
}

func TestMatchImplements_InvalidSemverFallsThroughToUnversioned(t *testing.T) {
	t.Parallel()

	// A caller URI with a non-SemVer version segment cannot satisfy any
	// caret constraint; the helper should fall through to the unversioned
	// fallback rather than crash.
	index := map[string]string{
		"core/api-server:^1.0.0": "caret",
		"core/api-server":        "unversioned",
	}

	got, ok := matchImplements(index, "core/api-server:not-a-version")
	if !ok {
		t.Fatal("expected unversioned fallback after invalid-SemVer caret miss")
	}
	if got != "unversioned" {
		t.Errorf("expected unversioned, got %q", got)
	}
}

func TestMatchImplements_PrereleaseInCaretRange(t *testing.T) {
	t.Parallel()

	// Pre-release versions satisfy caret constraints per Masterminds/semver/v3
	// when the constraint pins to a pre-release baseline. A non-prerelease
	// caret baseline should not match a pre-release caller version per
	// SemVer's pre-release-precedes-release rule.
	index := map[string]string{
		"core/api-server:^1.0.0-beta": "caret-prerelease-baseline",
	}

	got, ok := matchImplements(index, "core/api-server:1.0.0-rc1")
	if !ok {
		t.Fatal("expected pre-release caret match")
	}
	if got != "caret-prerelease-baseline" {
		t.Errorf("expected caret-prerelease-baseline, got %q", got)
	}
}

func TestSplitVersionedURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uri          string
		wantBase     string
		wantVersion  string
		wantHasVer   bool
	}{
		{"core/api-server:1.0.0", "core/api-server", "1.0.0", true},
		{"core/api-server:^1.0.0", "core/api-server", "^1.0.0", true},
		{"core/api-server", "core/api-server", "", false},
		{"hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server", "1.0.0", true},
		{"hub.aggrega.ai/component/core/api-server", "hub.aggrega.ai/component/core/api-server", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			base, version, hasVer := splitVersionedURI(tc.uri)
			if base != tc.wantBase || version != tc.wantVersion || hasVer != tc.wantHasVer {
				t.Errorf("splitVersionedURI(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.uri, base, version, hasVer, tc.wantBase, tc.wantVersion, tc.wantHasVer)
			}
		})
	}
}
