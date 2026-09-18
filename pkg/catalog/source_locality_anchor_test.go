// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests which scalar `source:` values a catalog load anchors against the
// declaring entity's own directory, for both kinds that carry a source.
//
// The property under test is that anchoring is gated twice, on two independent
// readings of the value's own text. It runs only where the value names something
// the workspace already holds — joining a transport reference to a directory
// would both destroy the reference and leave a value every later pass reads as
// local, so the pass that pins a Release's dependencies would pin nothing and the
// render would then stat a directory nobody authored. And it runs only where the
// value is a value rather than a template, because a template names nothing until
// it has been evaluated and the directory it is relative to belongs to what it
// produces; the render performs that half, against the produced value.
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
		// A template whose text already carries the scheme is held back by BOTH
		// gates — it reads as a transport reference before it is evaluated, and it
		// is still text — so it is the row proving the two compose rather than
		// competing. The render evaluates it and the locality reading answers the
		// same way about what it produced.
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

// TestScalarSourceTemplateIsLeftForTheValueItProduces pins the half of the
// resolution the load deliberately does not perform.
//
// A source written as a template names nothing while it is still text, so the
// directory it is relative to belongs to the value it produces and the load hands
// it on exactly as authored. Anchoring it here instead reads as harmless whenever
// that value turns out to be relative — a directory joined to a relative path is
// the same directory either way — while settling every value that is not before
// it exists: an absolute path would be prefixed, a transport reference destroyed,
// and an empty result turned into the directory itself.
//
// Both kinds are asserted together because the two loads carry one gate, and a
// change reaching one of them would satisfy every assertion made against the
// other alone.
func TestScalarSourceTemplateIsLeftForTheValueItProduces(t *testing.T) {
	for _, authored := range []string{
		`"{{ .locals.chart_dir }}"`,
		`"charts/{{ .locals.flavor }}/app"`,
		`"oci://{{ .locals.repo }}/chart:1.0.0"`,
	} {
		t.Run(authored, func(t *testing.T) {
			want := strings.Trim(authored, `"`)
			catalogDir := sourceAnchorCatalog(t, authored)
			for kind, got := range loadedScalarSources(t, catalogDir) {
				if got != want {
					t.Errorf("%s: a templated source must reach the render as authored: %q became %q",
						kind, want, got)
				}
				if filepath.IsAbs(got) {
					t.Errorf("%s: a templated source was anchored at load: %q", kind, got)
				}
				if strings.Contains(got, catalogDir) {
					t.Errorf("%s: a templated source carries the catalog location: %q", kind, got)
				}
			}
		})
	}
}
