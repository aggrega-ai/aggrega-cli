// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import "testing"

// TestIsLocalURLReadsTheReferenceAlone covers the rule over every reference shape
// it has to place. The empty row is the one worth stating on its own: it is the
// answer both callers depend on, because a reference naming nothing is not a
// reference naming something near, and calling it local would hand the empty
// string onward as a path.
func TestIsLocalURLReadsTheReferenceAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{"names nothing", "", false},
		{"a relative path", "./chart", true},
		{"a parent-relative path", "../chart", true},
		{"an absolute path", "/srv/chart", true},
		{"a bare name", "chart", true},
		{"a registry reference", "oci://example.test/chart", false},
		{"a secure web reference", "https://example.test/chart.tgz", false},
		{"a plain web reference", "http://example.test/chart.tgz", false},
		{"a repository over secure shell", "git+ssh://example.test/repo", false},
		{"a repository over its own transport", "git://example.test/repo", false},
		{"a secure shell reference", "ssh://example.test/repo", false},
		{"a repository named by suffix", "example.test/repo.git", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLocalURL(tc.ref); got != tc.want {
				t.Errorf("IsLocalURL(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestIsLocalReadsTheShapeItIsGiven covers how each source shape reaches the rule
// above. The structured rows are what the shared home buys: both callers read the
// same field of a structured source and treat an unfamiliar shape the same way,
// which two implementations agreeing today would not guarantee tomorrow.
func TestIsLocalReadsTheShapeItIsGiven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		source any
		want   bool
	}{
		{"absent", nil, false},
		{"a scalar naming nothing", "", false},
		{"a scalar path", "./chart", true},
		{"a scalar registry reference", "oci://example.test/chart", false},
		{"structured, naming a path", map[string]any{"url": "./chart"}, true},
		{"structured, naming a registry reference", map[string]any{"url": "oci://example.test/c"}, false},
		{"structured, naming no url", map[string]any{"chart": "api"}, false},
		{"structured, url is not text", map[string]any{"url": 7}, false},
		{"a shape the rule cannot place", 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLocal(tc.source); got != tc.want {
				t.Errorf("IsLocal(%#v) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}
