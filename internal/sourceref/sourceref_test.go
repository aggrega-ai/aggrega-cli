// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import (
	"path/filepath"
	"testing"
)

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

// TestIsTemplatedReadsWhetherTheReferenceIsStillText covers the reading that
// decides WHEN a reference is anchored. The rows pair each templated form with
// the plain form nearest to it, because the failure this reading exists to
// prevent is a plain reference deferred (it would then never be anchored) or a
// templated one anchored early (its value would be resolved against text).
func TestIsTemplatedReadsWhetherTheReferenceIsStillText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{"names nothing", "", false},
		{"a relative path", "./charts/app", false},
		{"a bare name", "chart", false},
		{"a registry reference", "oci://example.test/chart", false},
		{"the whole reference is one action", "{{ .locals.chart_dir }}", true},
		{"an action inside a path", "charts/{{ .locals.flavor }}/app", true},
		{"an action inside a registry reference", "oci://{{ .locals.repo }}/chart:1.0.0", true},
		{"a trailing action", "./charts/{{ .values.name }}", true},
		// A closing delimiter alone is not an action. The reading is of the opening
		// one because that is what the evaluation parses on, and a reference the
		// evaluation would not treat as a template must not be deferred by this.
		{"a closing delimiter alone", "./charts/app}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTemplated(tc.ref); got != tc.want {
				t.Errorf("IsTemplated(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestAnchorToDocumentResolvesOnlyWhatItShould covers the one statement of what
// a local relative source is resolved against, over every reference shape and
// both document states.
//
// The four pass-through rows are the point of the table rather than edge cases:
// each is a value that joining a directory onto would destroy, and each is a
// value a source template can produce. A resolution that anchored them would be
// indistinguishable, at this level, from the text-anchoring this replaces.
func TestAnchorToDocumentResolvesOnlyWhatItShould(t *testing.T) {
	t.Parallel()
	const document = "/srv/catalog/recipes/app.yaml"
	for _, tc := range []struct {
		name     string
		document string
		ref      string
		want     string
	}{
		{"a relative path is anchored to the declaring directory", document, "./charts/app", "/srv/catalog/recipes/charts/app"},
		{"a bare name is anchored too", document, "charts/app", "/srv/catalog/recipes/charts/app"},
		{"a parent-relative path resolves out of the directory", document, "../shared/charts/app", "/srv/catalog/shared/charts/app"},
		{"an absolute path already names where it is", document, "/opt/charts/app", "/opt/charts/app"},
		{"a reference naming nothing stays naming nothing", document, "", ""},
		{"a registry reference is carried through", document, "oci://example.test/chart:1.0.0", "oci://example.test/chart:1.0.0"},
		{"a web reference is carried through", document, "https://example.test/chart.tgz", "https://example.test/chart.tgz"},
		{"a repository reference is carried through", document, "example.test/repo.git", "example.test/repo.git"},
		// No declaring document: an entity resolved from a registry lives in a
		// content-addressed slot rather than a file, so there is no directory for a
		// relative reference to be relative to and it is handed back.
		{"no declaring document leaves a relative path alone", "", "./charts/app", "./charts/app"},
		{"no declaring document leaves an absolute path alone", "", "/opt/charts/app", "/opt/charts/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AnchorToDocument(tc.document, tc.ref)
			if err != nil {
				t.Fatalf("AnchorToDocument(%q, %q): %v", tc.document, tc.ref, err)
			}
			if got != tc.want {
				t.Errorf("AnchorToDocument(%q, %q) = %q, want %q", tc.document, tc.ref, got, tc.want)
			}
		})
	}
}

// TestAnchorToDocumentAnchorsARelativeDocumentAgainstTheProcessDirectory pins
// the one case the table above cannot state as a literal: a declaring document
// named relatively still yields an absolute anchor, so a value resolved through
// it does not change meaning when the build is run from elsewhere.
func TestAnchorToDocumentAnchorsARelativeDocumentAgainstTheProcessDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := AnchorToDocument("catalog/recipes/app.yaml", "./charts/app")
	if err != nil {
		t.Fatalf("AnchorToDocument: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("a relative document must still yield an absolute anchor; got %q", got)
	}
	// EvalSymlinks on the expectation: a temp directory is reached through a
	// symlink on some platforms, and the property under test is which directory
	// was anchored to rather than which spelling of it the process reports.
	want := filepath.Join(dir, "catalog", "recipes", "charts", "app")
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		want = filepath.Join(resolved, "catalog", "recipes", "charts", "app")
	}
	if got != want && got != filepath.Join(dir, "catalog", "recipes", "charts", "app") {
		t.Errorf("anchored against the wrong base: got %q, want %q", got, want)
	}
}
