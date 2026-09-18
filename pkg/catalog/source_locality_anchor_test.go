// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests which scalar `source:` values a catalog load anchors against the
// declaring entity's own directory, for both kinds that carry a source.
//
// The property under test is that anchoring is gated on locality: a value
// naming something the workspace already holds is joined to the entity's
// directory, and a value naming something fetched over a transport is carried
// through untouched. Joining a transport reference to a directory would both
// destroy the reference and leave a value every later pass reads as local — the
// pass that pins a Release's dependencies would pin nothing, and the render
// would then stat a directory nobody authored.
//
// The load runs through LoadAll over a real directory tree rather than through
// the per-entity parse, and the assertions read the loaded entity back: a value
// built by hand into the struct would satisfy a check on the field while a load
// that rewrote it went unnoticed. Every case chdirs into an unrelated directory
// first, so anchoring against the entity's directory is distinguishable from
// anchoring against the directory the process runs in.
//
// Both kinds come back from every case together. The two parses carry the same
// gate, and a repair reaching one and not the other would pass every assertion
// made against the first alone.
//
// The rows are the authoring axis for a scalar source, so a form nobody covers
// is a missing row rather than an absent function.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anchorRelativeSource is the relative value the local rows author, kept in one
// place so the fixture and the expectation cannot come to disagree.
const anchorRelativeSource = "./charts/app"

// sourceAnchorCatalog writes a catalog directory holding a Recipe and a
// FeatureRecipe that each declare the same scalar source, drops a chart
// directory beside them carrying the beside sentinel, plants a decoy copy at
// the same relative path in the directory the test then runs from, and returns
// the catalog directory.
func sourceAnchorCatalog(t *testing.T, authored string) string {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	runDir := filepath.Join(root, "elsewhere")
	for dir, body := range map[string]string{catalogDir: beside, runDir: decoy} {
		target := filepath.Join(dir, "charts", "app", "Chart.yaml")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entities := map[string]string{
		"recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: anchored-source
  version: "1.0.0"
spec:
  capability: helm-chart
  implements: "core/compute/api-server"
  source: ` + authored + `
  locals:
    chart_dir: ./charts/app
  valuesMapping: |
    { "image": "app:latest" }
`,
		"featurerecipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: anchored-source-fr
  version: "1.0.0"
spec:
  capability: helm-chart
  implements: "core/monitoring"
  source: ` + authored + `
  locals:
    chart_dir: ./charts/app
  valuesMapping: |
    { "image": "app:latest" }
`,
	}
	for name, body := range entities {
		if err := os.WriteFile(filepath.Join(catalogDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(runDir)
	return catalogDir
}

// loadedScalarSources returns the single Recipe's and the single
// FeatureRecipe's post-load scalar source from a loaded catalog, keyed by the
// kind for the failure messages.
func loadedScalarSources(t *testing.T, catalogDir string) map[string]string {
	t.Helper()
	c, err := LoadAll("", catalogDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(c.Recipes) != 1 {
		t.Fatalf("expected 1 Recipe; got %d", len(c.Recipes))
	}
	if len(c.FeatureRecipes) != 1 {
		t.Fatalf("expected 1 FeatureRecipe; got %d", len(c.FeatureRecipes))
	}
	out := make(map[string]string, 2)
	for _, r := range c.Recipes {
		s, ok := r.Spec.Source.(string)
		if !ok {
			t.Fatalf("Recipe: source did not come back as a scalar: %#v", r.Spec.Source)
		}
		out["Recipe"] = s
	}
	for _, fr := range c.FeatureRecipes {
		s, ok := fr.Spec.Source.(string)
		if !ok {
			t.Fatalf("FeatureRecipe: source did not come back as a scalar: %#v", fr.Spec.Source)
		}
		out["FeatureRecipe"] = s
	}
	return out
}

// TestScalarSourceCarriesTransportReferencesThrough pins that a scalar source
// naming something fetched over a transport survives a catalog load exactly as
// authored, for both kinds.
//
// Every row is a reference the workspace does not already hold. Joining one to
// the entity's directory collapses its scheme separator and prefixes a
// directory, producing a value that names neither what the author wrote nor
// anything on disk, and that every later pass reads as local.
func TestScalarSourceCarriesTransportReferencesThrough(t *testing.T) {
	cases := []struct {
		name     string
		authored string
	}{
		{name: "oci chart reference", authored: "oci://ghcr.io/example/chart:1.0.0"},
		{name: "https archive reference", authored: "https://example.com/chart.tgz"},
		{name: "http archive reference", authored: "http://example.com/chart.tgz"},
		{name: "git+ssh repository reference", authored: "git+ssh://git@example.com/org/repo"},
		{name: "git repository reference", authored: "git://example.com/org/repo"},
		{name: "ssh repository reference", authored: "ssh://git@example.com/org/repo"},
		{name: "dot-git suffix reference", authored: "https://example.com/org/repo.git"},
		// A template whose text already carries the scheme is readable as a
		// transport reference before it is evaluated, so it is gated on the same
		// reading as a plain one.
		{name: "scheme-bearing template", authored: `"oci://{{ .locals.repo }}/chart:1.0.0"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := strings.Trim(tc.authored, `"`)
			catalogDir := sourceAnchorCatalog(t, tc.authored)
			for kind, got := range loadedScalarSources(t, catalogDir) {
				if got != want {
					t.Errorf("%s: transport reference was rewritten by the load: %q became %q",
						kind, want, got)
				}
				if filepath.IsAbs(got) {
					t.Errorf("%s: transport reference came back as a filesystem path: %q", kind, got)
				}
				if strings.Contains(got, catalogDir) {
					t.Errorf("%s: transport reference carries the catalog location: %q", kind, got)
				}
			}
		})
	}
}

// TestScalarSourceAnchorsLocalPathsAgainstTheEntityDirectory is the negative
// control for the gate above: a value naming something the workspace already
// holds is still anchored against the declaring entity's own directory, so the
// gate cannot be satisfied by carrying everything through.
//
// The assertion opens the resolved directory and reads the sentinel back,
// rather than comparing strings, because the property is which base the value
// was resolved against — the test runs from a directory holding a decoy at the
// same relative path.
func TestScalarSourceAnchorsLocalPathsAgainstTheEntityDirectory(t *testing.T) {
	for _, authored := range []string{anchorRelativeSource, "charts/app"} {
		t.Run(authored, func(t *testing.T) {
			catalogDir := sourceAnchorCatalog(t, authored)
			for kind, got := range loadedScalarSources(t, catalogDir) {
				if !filepath.IsAbs(got) {
					t.Errorf("%s: local path was not anchored: %q", kind, got)
					continue
				}
				body, err := os.ReadFile(filepath.Join(got, "Chart.yaml"))
				if err != nil {
					t.Errorf("%s: anchored path unreadable: %v", kind, err)
					continue
				}
				if read := strings.TrimSpace(string(body)); read != beside {
					t.Errorf("%s: anchored against the wrong base: read %q from %q, want %q",
						kind, read, got, beside)
				}
			}
		})
	}
}

// TestScalarSourceAbsoluteLocalPathIsHandedBackUnchanged pins that an
// already-absolute local path is neither re-anchored nor refused: it passes the
// locality reading, reaches the resolution, and comes back as written.
func TestScalarSourceAbsoluteLocalPathIsHandedBackUnchanged(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "charts", "app")
	catalogDir := sourceAnchorCatalog(t, absolute)
	for kind, got := range loadedScalarSources(t, catalogDir) {
		if got != absolute {
			t.Errorf("%s: absolute local path was rewritten: %q became %q", kind, absolute, got)
		}
	}
}

// TestScalarSourceTemplateYieldingAPathIsAnchoredOnItsText records the reading a
// source written as a template gets today, so the behaviour is pinned rather
// than merely true.
//
// A template carrying no transport scheme reads as naming something the
// workspace already holds, so it is anchored — and it is anchored against the
// template's own text, because the value is read as the document is loaded and
// nothing has evaluated it yet. The resolution of a template against the path
// its text evaluates to is a separate property, and this test asserts only what
// the load does.
func TestScalarSourceTemplateYieldingAPathIsAnchoredOnItsText(t *testing.T) {
	const authored = `"{{ .locals.chart_dir }}"`
	catalogDir := sourceAnchorCatalog(t, authored)
	for kind, got := range loadedScalarSources(t, catalogDir) {
		if !filepath.IsAbs(got) {
			t.Errorf("%s: templated source was not anchored: %q", kind, got)
			continue
		}
		if !strings.Contains(got, "{{ .locals.chart_dir }}") {
			t.Errorf("%s: templated source no longer carries its own text: %q", kind, got)
		}
		if want := filepath.Join(catalogDir, "{{ .locals.chart_dir }}"); got != want {
			t.Errorf("%s: templated source anchored against an unexpected base: got %q, want %q",
				kind, got, want)
		}
	}
}
