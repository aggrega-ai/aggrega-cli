// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"testing"
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
		name      string
		ref       string
		wantKey   string
		wantOK    bool
		wantUnsat bool
	}{
		{"unversioned resolves to highest non-prerelease", "core/x", "core/x:1.1.0", true, false},
		{"exact pins that version", "core/x:1.0.0", "core/x:1.0.0", true, false},
		{"caret picks highest in range", "core/x:^1.0.0", "core/x:1.1.0", true, false},
		{"exact matches a prerelease literally", "core/x:2.0.0-rc1", "core/x:2.0.0-rc1", true, false},
		{"caret matching nothing is unsatisfiable", "core/x:^2.0.0", "", false, true},
		{"exact absent version is unsatisfiable", "core/x:9.9.9", "", false, true},
		{"absent base is a plain miss", "core/y", "", false, false},
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
			if tc.wantUnsat {
				if !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
					t.Errorf("want ErrUnsatisfiableEntityVersion, got %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected err %v", err)
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

// implementsSatisfies is the single caret-aware per-entry implements matcher:
// given one recipe's spec.implements (impl) and a concrete component-type
// request (componentType), it reports whether impl satisfies the request under
// the three-form rule (exact, caret-range, unversioned base). These cases pin
// the caret semantics — the implements SEARCH and the manifest-mapping
// consistency check both consume it, so this coverage guards both surfaces.
func TestImplementsSatisfies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                string
		impl, componentType string
		want                bool
	}{
		{"exact match", "core/api-server:1.0.0", "core/api-server:1.0.0", true},
		{"caret in range", "core/api-server:^1.0.0", "core/api-server:1.5.3", true},
		{"caret out of range", "core/api-server:^1.0.0", "core/api-server:2.0.0", false},
		{"caret base mismatch", "other/thing:^1.0.0", "core/api-server:1.5.0", false},
		{"unversioned impl matches versioned request", "core/api-server", "core/api-server:1.0.0", true},
		{"unversioned impl and unversioned request", "core/api-server", "core/api-server", true},
		{"versioned impl cannot match unversioned request", "core/api-server:1.0.0", "core/api-server", false},
		{"non-caret version mismatch same base", "core/api-server:1.0.0", "core/api-server:2.0.0", false},
		{"prerelease caller within prerelease caret baseline", "core/api-server:^1.0.0-beta", "core/api-server:1.0.0-rc1", true},
		{"caret cannot match unparseable request version", "core/api-server:^1.0.0", "core/api-server:not-a-version", false},
		{"unrelated base is a miss", "core/api-server:1.0.0", "different/component:1.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := implementsSatisfies(tc.impl, tc.componentType); got != tc.want {
				t.Errorf("implementsSatisfies(%q, %q) = %v; want %v", tc.impl, tc.componentType, got, tc.want)
			}
		})
	}
}

func TestSplitVersionedURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uri         string
		wantBase    string
		wantVersion string
		wantHasVer  bool
	}{
		{"core/api-server:1.0.0", "core/api-server", "1.0.0", true},
		{"core/api-server:^1.0.0", "core/api-server", "^1.0.0", true},
		{"core/api-server", "core/api-server", "", false},
		// This pair differs ONLY in hasVersion: a trailing colon is a present
		// but empty version, an absent colon is no version at all. A caller
		// reading base and version alone cannot tell them apart.
		{"core/api-server:", "core/api-server", "", true},
		{"hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server", "1.0.0", true},
		{"hub.aggrega.ai/component/core/api-server", "hub.aggrega.ai/component/core/api-server", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			base, version, hasVer := SplitVersionedURI(tc.uri)
			if base != tc.wantBase || version != tc.wantVersion || hasVer != tc.wantHasVer {
				t.Errorf("SplitVersionedURI(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.uri, base, version, hasVer, tc.wantBase, tc.wantVersion, tc.wantHasVer)
			}
		})
	}
}
