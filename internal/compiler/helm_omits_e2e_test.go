// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end coverage of leaving documents a chart renders out of a build, through
// the real pipeline and the embedded helm provider.
//
// The fixture's Recipe declares two omits entries, each active only where the
// Release asks for it through infra, so every cell below is one Release over one
// catalog and every left-out document is reached by the route an author uses: the
// Release writes infra, the Recipe's entry reads it. Each cell is read against a
// control that differs from it only in that one value, so what a cell shows absent
// is shown present one Release over.

package compiler_test

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// helmOmitsInput compiles one Release of the omits fixture.
func helmOmitsInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/helm-omits/" + release,
		CatalogPaths: []string{"testdata/helm-omits/catalog"},
	}
}

// omitsDocument is one resource document a build wrote, as far as these proofs
// read it.
type omitsDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations"`
		Labels      map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
}

// omitsDocuments reads every document of every file a build wrote, keyed by the
// file it was written to, in path order.
func omitsDocuments(t *testing.T, artifacts map[string][]byte) map[string][]omitsDocument {
	t.Helper()
	out := map[string][]omitsDocument{}
	for _, p := range slices.Sorted(maps.Keys(artifacts)) {
		dec := yaml.NewDecoder(bytes.NewReader(artifacts[p]))
		for {
			var doc omitsDocument
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			out[p] = append(out[p], doc)
		}
	}
	return out
}

// emittedNames is every kind/name a build wrote, sorted, one entry per document.
func emittedNames(t *testing.T, artifacts map[string][]byte) []string {
	t.Helper()
	var names []string
	for _, docs := range omitsDocuments(t, artifacts) {
		for _, d := range docs {
			names = append(names, d.Kind+"/"+d.Metadata.Name)
		}
	}
	sort.Strings(names)
	return names
}

// factsRead returns the lines the fixture's fact-reading Feature printed about its
// host's render, sorted, or fails when no file carries them.
func factsRead(t *testing.T, artifacts map[string][]byte) []string {
	t.Helper()
	for _, docs := range omitsDocuments(t, artifacts) {
		for _, d := range docs {
			body, printed := d.Data["read"]
			if d.Kind != "ConfigMap" || !printed {
				continue
			}
			lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
			if lines[0] != "host app" {
				t.Fatalf("the printed read opens %q, not the host it was attached to", lines[0])
			}
			return slices.Sorted(slices.Values(lines[1:]))
		}
	}
	t.Fatalf("no file carries the facts the Feature printed; files: %v", slices.Sorted(maps.Keys(artifacts)))
	return nil
}

// TestHelmOmits_ALeftOutDocumentReachesNoFileAndNoRenderFact builds the Release that
// leaves the chart's app-drop controller out and its control, which leaves nothing
// out. The control writes the controller and the Feature attached to the host reads
// it among the host's render facts; the left-out build writes no document of it, the
// Feature reads only the controller that stayed, and every other file the control
// writes is written under the same name with the same bytes — the identity of a
// kept document is computed over the documents as rendered, so leaving one out
// moves no other.
func TestHelmOmits_ALeftOutDocumentReachesNoFileAndNoRenderFact(t *testing.T) {
	t.Parallel()

	control := pipelinetest.Build(t, helmOmitsInput("release-facts.yaml")).Artifacts
	leftOut := pipelinetest.Build(t, helmOmitsInput("release-facts-left-out.yaml")).Artifacts

	if got, want := emittedNames(t, control), []string{
		"ConfigMap/app-facts", "ConfigMap/shared-settings", "Deployment/app-drop", "Deployment/app-keep",
	}; !slices.Equal(got, want) {
		t.Fatalf("the control writes %v, want %v — the rows below read a render the fixture no longer produces", got, want)
	}
	if got, want := emittedNames(t, leftOut), []string{
		"ConfigMap/app-facts", "ConfigMap/shared-settings", "Deployment/app-keep",
	}; !slices.Equal(got, want) {
		t.Errorf("the build leaving app-drop out writes %v, want %v", got, want)
	}

	if got, want := factsRead(t, control), []string{
		"controller_selectors Deployment/app-drop", "controller_selectors Deployment/app-keep",
		"pod_template_labels Deployment/app-drop", "pod_template_labels Deployment/app-keep",
	}; !slices.Equal(got, want) {
		t.Fatalf("the Feature on the control read %v, want %v", got, want)
	}
	if got, want := factsRead(t, leftOut), []string{
		"controller_selectors Deployment/app-keep", "pod_template_labels Deployment/app-keep",
	}; !slices.Equal(got, want) {
		t.Errorf("the Feature on the build leaving app-drop out read %v, want %v", got, want)
	}

	docs := omitsDocuments(t, leftOut)
	for path, body := range leftOut {
		if len(docs[path]) == 1 {
			if _, printed := docs[path][0].Data["read"]; printed {
				continue // the facts file: what it prints is the difference under test
			}
		}
		want, written := control[path]
		if !written {
			t.Errorf("the build leaving app-drop out writes %s, which the control does not", path)
			continue
		}
		if !bytes.Equal(body, want) {
			t.Errorf("%s differs from the control's:\n%s\nwant:\n%s", path, body, want)
		}
	}
}

// TestHelmOmits_ALeftOutDocumentReachesNoDuplicateNameCheck holds the comparison of
// declared names over two components of one chart placed in one namespace, each
// writing the ConfigMap shared-settings. The control halts under the derived-name
// condition naming both; the Release that leaves edge's copy out builds, and writes
// the one ConfigMap store rendered — so what a provider leaves out is absent from the
// names it declares for that comparison.
func TestHelmOmits_ALeftOutDocumentReachesNoDuplicateNameCheck(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, helmOmitsInput("release-collide.yaml"))
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the control did not halt on the shared name, so the cell below proves nothing: %v", err)
	}
	whole := strings.Join(collisionFindings(t, err), "\n")
	for _, want := range []string{
		`"shared-settings"`, `key space "resource-names:ConfigMap--shop"`,
		`derived by component "store"`, `derived by component "edge"`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("the control's halt does not carry %q:\n%s", want, whole)
		}
	}

	result, err := pipelinetest.BuildE(t, helmOmitsInput("release-collide-left-out.yaml"))
	if err != nil {
		t.Fatalf("leaving edge's shared-settings out must clear the collision: %s", cerrors.CauseChain(err))
	}
	var holders []string
	for _, docs := range omitsDocuments(t, result.Artifacts) {
		for _, d := range docs {
			if d.Kind == "ConfigMap" && d.Metadata.Name == "shared-settings" {
				holders = append(holders, d.Metadata.Labels["aggrega.ai/component-id"])
			}
		}
	}
	if !slices.Equal(holders, []string{"store"}) {
		t.Errorf("shared-settings is written by %v, want store's alone", holders)
	}
}

// TestHelmOmits_APipelineStepIsNeverHandedALeftOutDocument runs the Recipe carrying
// the patch step, which annotates every Deployment it is handed and removes none.
// The control shows both controllers annotated, so a handed controller is visible
// in the output; leaving app-drop out leaves app-keep annotated and no document of
// app-drop anywhere, while the step's own emitted object shows the step ran — a
// step that removes nothing cannot have been handed what the output lacks.
func TestHelmOmits_APipelineStepIsNeverHandedALeftOutDocument(t *testing.T) {
	catalogPaths := stageColocatedCatalog(t, []string{"testdata/helm-omits/catalog"}, map[string]string{
		"pipeline-step/patch": "frp-step-patch-rust.wasm",
	})
	t.Parallel()

	build := func(release string) map[string][]omitsDocument {
		return omitsDocuments(t, pipelinetest.Build(t, pipelinetest.Input{
			ReleasePath:  "testdata/helm-omits/" + release,
			CatalogPaths: catalogPaths,
		}).Artifacts)
	}
	patched := func(files map[string][]omitsDocument) map[string]bool {
		out := map[string]bool{}
		for _, docs := range files {
			for _, d := range docs {
				if d.Kind == "Deployment" {
					out[d.Metadata.Name] = d.Metadata.Annotations["aggrega.ai/pipeline-patched"] == "true"
				}
			}
		}
		return out
	}
	emitted := func(files map[string][]omitsDocument) bool {
		for _, docs := range files {
			for _, d := range docs {
				if d.Kind == "ConfigMap" && d.Metadata.Name == "pipeline-emitted" {
					return true
				}
			}
		}
		return false
	}

	control := build("release-pipeline.yaml")
	if got, want := patched(control), map[string]bool{"app-keep": true, "app-drop": true}; !maps.Equal(got, want) {
		t.Fatalf("the control's controllers read %v, want both written and annotated", got)
	}
	leftOut := build("release-pipeline-left-out.yaml")
	if got, want := patched(leftOut), map[string]bool{"app-keep": true}; !maps.Equal(got, want) {
		t.Errorf("leaving app-drop out, the controllers read %v, want app-keep alone, annotated", got)
	}
	if !emitted(leftOut) {
		t.Error("the step's own emitted object is missing, so the step did not run over the build leaving app-drop out")
	}
}

// TestHelmOmits_AFeatureRecipeLeavesADocumentOutOfItsOwnChart holds the key on a
// FeatureRecipe at both attachment scopes: one attached to a component and one
// attached to a boundary, each sourcing a chart of its own that writes a -keep and a
// -drop object. Each control writes both; with the Release asking for -drop to be
// left out, each writes -keep alone, and the host's own documents are untouched.
func TestHelmOmits_AFeatureRecipeLeavesADocumentOutOfItsOwnChart(t *testing.T) {
	t.Parallel()

	host := []string{"ConfigMap/shared-settings", "Deployment/app-drop", "Deployment/app-keep"}
	for _, tc := range []struct {
		name, control, leftOut, prefix string
	}{
		{"a component-scope FeatureRecipe", "release-sidecar.yaml", "release-sidecar-left-out.yaml", "sidecar"},
		{"a boundary-scope FeatureRecipe", "release-guard.yaml", "release-guard-left-out.yaml", "guard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			control := emittedNames(t, pipelinetest.Build(t, helmOmitsInput(tc.control)).Artifacts)
			want := slices.Sorted(slices.Values(append(slices.Clone(host),
				"ConfigMap/"+tc.prefix+"-drop", "ConfigMap/"+tc.prefix+"-keep")))
			if !slices.Equal(control, want) {
				t.Fatalf("the control writes %v, want %v", control, want)
			}
			leftOut := emittedNames(t, pipelinetest.Build(t, helmOmitsInput(tc.leftOut)).Artifacts)
			want = slices.Sorted(slices.Values(append(slices.Clone(host), "ConfigMap/"+tc.prefix+"-keep")))
			if !slices.Equal(leftOut, want) {
				t.Errorf("leaving %s-drop out writes %v, want %v", tc.prefix, leftOut, want)
			}
		})
	}
}

// TestHelmOmits_AnEntryThatLeftNothingOutHalts holds the halt on an active entry the
// provider matched to nothing, on each document that can declare one: the node's
// Recipe, a component-scope FeatureRecipe and a boundary-scope one. Each names the
// node, the document, the entry's position and its resolved fields. The
// boundary-scope entry builds its name from a value its Feature declares sensitive,
// so that one field is shown as the placeholder and the value appears nowhere in the
// report.
func TestHelmOmits_AnEntryThatLeftNothingOutHalts(t *testing.T) {
	t.Parallel()

	const plaintext = "omits-withheld-plaintext"
	for _, tc := range []struct {
		name, release string
		want          []string
	}{
		{
			name:    "a Recipe",
			release: "release-stale.yaml",
			want:    []string{`node "app"`, `"omits-app" omits[0]`, `kind "Deployment"`, `name "app-missing"`},
		},
		{
			name:    "a component-scope FeatureRecipe",
			release: "release-sidecar-stale.yaml",
			want:    []string{`node "app"`, `"omits-sidecar" omits[0]`, `kind "ConfigMap"`, `name "sidecar-missing"`},
		},
		{
			name:    "a boundary-scope FeatureRecipe, its entry's name built from a sensitive value",
			release: "release-guard-stale.yaml",
			want:    []string{`"omits-guard" omits[0]`, `kind "ConfigMap"`, "name " + redact.Placeholder()},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, helmOmitsInput(tc.release))
			if err == nil {
				t.Fatalf("%s built; want the halt on an entry that left nothing out", tc.release)
			}
			if !errors.Is(err, renderer.ErrOmitMatchedNothing()) {
				t.Fatalf("%s failed for another reason: %s", tc.release, cerrors.CauseChain(err))
			}
			chain := cerrors.CauseChain(err)
			for _, want := range append(tc.want, renderer.ErrOmitMatchedNothing().Error()) {
				if !strings.Contains(chain, want) {
					t.Errorf("the halt does not carry %q:\n%s", want, chain)
				}
			}
			if strings.Contains(chain, plaintext) || strings.Contains(err.Error(), plaintext) {
				t.Errorf("the halt quotes the value declared sensitive:\n%s", chain)
			}
		})
	}
}

// TestHelmOmits_LeavingADocumentOutIsDeterministic builds the Release leaving a
// controller out repeatedly and requires byte-identical output.
func TestHelmOmits_LeavingADocumentOutIsDeterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, helmOmitsInput("release-facts-left-out.yaml"))
}
