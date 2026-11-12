// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// entityBody returns a minimal schema-valid document of the named kind, used only
// to give the catalog walk something with a readable header.
func entityBody(kind entitykind.Kind, name, body string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: " + string(kind) +
		"\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n" + body
}

// TestCatalogWalkAdmitsEveryConventionsLintedKind drives the real catalog walk —
// not the collection downstream of it — and holds that every kind the vocabulary
// marks as conventions-linted actually reaches the rules.
//
// The walk is the gate: a file whose kind the walk declines never enters the
// name index, so it reaches no convention rule and nothing reports that it was
// skipped. A convention violation in such a file simply ships. Asserting against
// the declared kind set rather than a hand-written list is what keeps that from
// recurring, because a new kind joins this assertion by being declared rather
// than by being remembered here.
func TestCatalogWalkAdmitsEveryConventionsLintedKind(t *testing.T) {
	ws := t.TempDir()

	// Body fragments per kind, so each document is a plausible entity rather than
	// a bare header. The walk only peeks at the header, but a realistic body keeps
	// the fixture usable if the walk ever parses further.
	bodies := map[entitykind.Kind]string{
		entitykind.Component:       "spec:\n  type: service\n",
		entitykind.Feature:         "spec: {}\n",
		entitykind.Recipe:          "spec:\n  capability: helm\n",
		entitykind.FeatureRecipe:   "spec:\n  capability: helm\n",
		entitykind.SchemaLibrary:   "spec:\n  schemas:\n    Storage:\n      size: { type: string }\n",
		entitykind.MappingLibrary:  "spec:\n  mappings:\n    Simple: |\n      key: value\n",
		entitykind.WorkloadLibrary: "spec:\n  workloads:\n    Deploy: |\n      kind: Deployment\n",
		entitykind.ArtifactLibrary: "spec:\n  artifacts:\n    Bundle:\n      note.txt: |\n        hello\n",
		entitykind.Plugin:          "spec:\n  pluginType: linter\n",
	}

	linted := entitykind.ConventionsLinted()
	want := map[entitykind.Kind]string{}
	for _, k := range linted {
		body, ok := bodies[k]
		if !ok {
			t.Fatalf("no fixture body for conventions-linted kind %q: the fixture set has fallen behind the declared vocabulary, so this test would silently stop covering that kind", k)
		}
		p := filepath.Join(ws, entitykind.Segment(k)+".yaml")
		mustWriteFile(t, p, entityBody(k, "Probe"+string(k), body))
		want[k] = p
	}

	files, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{ws}, nil, "")
	if err != nil {
		t.Fatalf("walkCatalogEntityFiles: %v", err)
	}
	admitted := map[string]bool{}
	for _, f := range files {
		admitted[f.Kind] = true
	}

	// Non-vacuity: the fixture must actually reach the walk at all. If not even a
	// Component came back, the assertion below would be reporting a broken fixture
	// rather than a vocabulary gap.
	if !admitted[string(entitykind.Component)] {
		t.Fatalf("the fixture did not reach the walk at all (admitted %v); the assertion below would be vacuous", admitted)
	}

	for k := range want {
		if !admitted[string(k)] {
			t.Errorf("the catalog walk declined kind %s, so a locally-authored %s never reaches the conventions rules: a convention violation in one is linted by nobody and nothing reports the gap", k, k)
		}
	}
}

// TestConventionsKindGateReadsTheDeclaredVocabulary holds the walk's kind gate to
// the declared vocabulary in both directions: it admits exactly the
// conventions-linted kinds, and declines every other kind the vocabulary knows.
//
// Both directions matter. Missing a kind closes the rules over content that
// should be examined; admitting a Release or Manifest runs catalog-publishing
// rules over a document that is not published, reporting findings an author
// cannot act on.
func TestConventionsKindGateReadsTheDeclaredVocabulary(t *testing.T) {
	for _, e := range entitykind.Entries() {
		got := isLintableCatalogKind(string(e.Kind))
		if got != e.ConventionsLinted {
			t.Errorf("kind %s: the walk's gate says linted=%v but the declared vocabulary says %v", e.Kind, got, e.ConventionsLinted)
		}
	}
	if isLintableCatalogKind("NotAKind") {
		t.Error("the walk's gate admitted a token that is not an entity kind")
	}
}
