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

// The caret-semantics table for the implements matcher itself moved with the
// matcher, into the package that declares the rule reading it. The inverse
// direction above stays here, because the index lookup it exercises is this
// package's.
