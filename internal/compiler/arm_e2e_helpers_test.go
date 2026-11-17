// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Shared helpers for the end-to-end tests of the arm provider. Every test builds
// through the real pipeline and the real compiled Wasm guest against the
// fixture-local catalog under testdata/arm-catalog, and reads the emitted
// arm-template.json.

package compiler_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// armTemplateFile is the file name the provider writes each boundary's template
// under, written as the literal an emitted artifact path carries.
const armTemplateFile = "arm-template.json"

// armProvenanceKey is the key each deployed resource carries its provenance
// under in its metadata, written as the literal the emitted template carries.
const armProvenanceKey = "aggrega.ai/provenance"

// armCatalogPaths are the entities every arm end-to-end test builds against:
// the rendering Plugin at a test version, the example Components and Features,
// and the fragment Recipes and FeatureRecipes that implement them. A test that
// needs a Recipe of its own adds its fixture directory with armCatalogWith.
func armCatalogPaths() []string {
	return []string{"testdata/arm-catalog"}
}

// armCatalogWith is armCatalogPaths with fixture directories added beside it.
func armCatalogWith(extra ...string) []string {
	return append(armCatalogPaths(), extra...)
}

// armInput is the build input for one Release of a fixture directory.
func armInput(release string, extraCatalogs ...string) pipelinetest.Input {
	return pipelinetest.Input{ReleasePath: release, CatalogPaths: armCatalogWith(extraCatalogs...)}
}

// findArmTemplate returns the assembled template of the boundary whose dist
// directory contains dirSegment, decoded as a generic JSON tree, with its raw
// bytes. It fails the test when no such artifact is present.
func findArmTemplate(t *testing.T, artifacts map[string][]byte, dirSegment string) (map[string]any, []byte) {
	t.Helper()
	for k, v := range artifacts {
		if strings.Contains(k, dirSegment) && strings.HasSuffix(k, armTemplateFile) {
			var doc map[string]any
			if err := json.Unmarshal(v, &doc); err != nil {
				t.Fatalf("template %q is not valid JSON: %v", k, err)
			}
			return doc, v
		}
	}
	t.Fatalf("no %s for boundary %q; got artifact keys %v", armTemplateFile, dirSegment, sortedKeys(artifacts))
	return nil, nil
}

// armSection returns one top-level object of a decoded template, failing the test
// when it is absent or not an object.
func armSection(t *testing.T, template map[string]any, section string) map[string]any {
	t.Helper()
	obj, ok := template[section].(map[string]any)
	if !ok {
		t.Fatalf("the template carries no object at %q; got %#v", section, template[section])
	}
	return obj
}

// armResourceBody returns one resource of a decoded template by symbolic name.
func armResourceBody(t *testing.T, template map[string]any, symbol string) map[string]any {
	t.Helper()
	resources := armSection(t, template, "resources")
	body, ok := resources[symbol].(map[string]any)
	if !ok {
		t.Fatalf("the template declares no resource %q; it declares %v", symbol, mapKeys(resources))
	}
	return body
}

// armProvenanceOf reads the provenance block off one resource body and reports
// whether the resource carries one.
func armProvenanceOf(body map[string]any) (map[string]any, bool) {
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		return nil, false
	}
	block, ok := metadata[armProvenanceKey].(map[string]any)
	return block, ok
}

// armAdvisoryBySourceRef returns the advisories carrying the given source
// reference, in the order the build raised them.
func armAdvisoryBySourceRef(advisories []renderer.Diagnostic, sourceRef string) []renderer.Diagnostic {
	var out []renderer.Diagnostic
	for _, d := range advisories {
		if d.SourceRef == sourceRef {
			out = append(out, d)
		}
	}
	return out
}

// armCompareGolden asserts the emitted bytes equal the committed golden, and
// writes the golden instead when AGGREGA_UPDATE_GOLDENS=1. An empty golden is
// refused: a comparison against nothing certifies nothing.
func armCompareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if os.Getenv("AGGREGA_UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing the golden %s: %v", path, err)
		}
		t.Logf("AGGREGA_UPDATE_GOLDENS=1: wrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden %s (run with AGGREGA_UPDATE_GOLDENS=1 to create it): %v", path, err)
	}
	if len(want) == 0 {
		t.Fatalf("the golden %s is empty, so a comparison against it certifies nothing", path)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the emitted template differs from the golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
