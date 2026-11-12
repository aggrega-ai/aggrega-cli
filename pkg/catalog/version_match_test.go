// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selectEntityVersion resolves a caller CONSTRAINT against a concrete-version
// index (the inverse of implementsSatisfies). These cases pin the three-form
// rule and the unsatisfiable-version sentinel.
func TestSelectEntityVersion(t *testing.T) {
	t.Parallel()

	type ent struct{ name, version string }
	meta := func(e ent) (string, string) { return e.name, e.version }
	idx := map[string]ent{
		"core/x:1.0.0":     {"core/x", "1.0.0"},
		"core/x:1.1.0":     {"core/x", "1.1.0"},
		"core/x:2.0.0-rc1": {"core/x", "2.0.0-rc1"},
	}

	cases := []struct {
		name          string
		ref           string
		wantKey       string
		wantOK        bool
		wantUnsat     bool
		wantMalformed bool
	}{
		{"unversioned resolves to highest non-prerelease", "core/x", "core/x:1.1.0", true, false, false},
		{"exact pins that version", "core/x:1.0.0", "core/x:1.0.0", true, false, false},
		{"caret picks highest in range", "core/x:^1.0.0", "core/x:1.1.0", true, false, false},
		{"exact matches a prerelease literally", "core/x:2.0.0-rc1", "core/x:2.0.0-rc1", true, false, false},
		{"caret matching nothing is unsatisfiable", "core/x:^2.0.0", "", false, true, false},
		{"exact absent version is unsatisfiable", "core/x:9.9.9", "", false, true, false},
		{"absent base is a plain miss", "core/y", "", false, false, false},
		// A malformed pin names no version, so the select reports the malformation
		// in place of the unsatisfiable condition — and only there, after the scan
		// found nothing, so a pinned base the catalog does not hold stays a plain miss.
		{"caret whose range does not parse is malformed", "core/x:^garbage", "", false, false, true},
		{"exact wildcard is malformed", "core/x:1.x", "", false, false, true},
		{"malformed pin on an absent base is a plain miss", "core/y:^garbage", "", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok, err := selectEntityVersion(idx, tc.ref, meta)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (key %q err %v)", ok, tc.wantOK, key, err)
			}
			if key != tc.wantKey {
				t.Errorf("key=%q want %q", key, tc.wantKey)
			}
			switch {
			case tc.wantUnsat:
				if !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
					t.Errorf("want ErrUnsatisfiableEntityVersion, got %v", err)
				}
				if errors.Is(err, types.ErrMalformedVersionPin()) {
					t.Errorf("a well-formed unsatisfiable pin was reported as malformed: %v", err)
				}
			case tc.wantMalformed:
				if !errors.Is(err, types.ErrMalformedVersionPin()) {
					t.Errorf("want ErrMalformedVersionPin, got %v", err)
				}
				if errors.Is(err, ErrUnsatisfiableEntityVersion()) {
					t.Errorf("a malformed pin was reported as unsatisfiable: %v", err)
				}
			default:
				if err != nil {
					t.Errorf("unexpected err %v", err)
				}
			}
		})
	}
}

// A version-less entry (empty metadata.version) is the fallback for an
// unversioned reference, so version-less catalogs keep resolving.
func TestSelectEntityVersion_VersionlessFallback(t *testing.T) {
	t.Parallel()
	type ent struct{ name, version string }
	meta := func(e ent) (string, string) { return e.name, e.version }
	idx := map[string]ent{"core/z": {"core/z", ""}}
	key, ok, err := selectEntityVersion(idx, "core/z", meta)
	if !ok || err != nil || key != "core/z" {
		t.Fatalf("versionless unversioned resolve = (%q, %v, %v); want (core/z, true, nil)", key, ok, err)
	}
}

// The caret-semantics table for the implements matcher itself moved with the
// matcher, into the package that declares the rule reading it. The inverse
// direction above stays here, because the index lookup it exercises is this
// package's.
