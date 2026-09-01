// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// The compose provenance promise is that every service in an assembled
// docker-compose.yaml carries the same build provenance, whichever input kind
// produced it. A compose Recipe declaring BOTH `source:` and `artifacts:` puts
// two of those kinds side by side in ONE document: the source path contributes
// `db`, the artifact path contributes `db-metrics`, and the boundary merge folds
// them into the same file. That pairing is what makes the proof below a parity
// assertion rather than a presence check — the two services are compared against
// each other in the output they actually share, so neither can pass by carrying
// a set the other does not.
//
// The fixture also declares a second artifact contributing only a top-level
// `volumes:` key. Compose carries provenance as service labels, so that fragment
// has no site for it; the build emits it unchanged and names it in an advisory.
// The pair covers both outcomes of the placer in one build.
//
// The Recipes below carry their own identities and their own Manifest rather than
// reusing the artifact-less compose-sourced pair beside them. Two reasons, and the
// first is a hard constraint: catalog identities are unique per directory, so a
// second Recipe answering to a name already taken refuses the whole catalog for
// every consumer of this shared fixture, not only for this test. The second is
// that binding through a Manifest naming these identities is what makes the
// resolution observable — the assertion on recipe-uri below reads it back out of
// the emitted document.
const (
	composeArtifactVariantRelease = "testdata/fullstack-app/releases/compose-sourced-with-artifacts.yaml"
	composeArtifactVariantRecipes = "testdata/fullstack-app/catalog/recipes/compose-sourced-with-artifacts"
	composeArtifactAssembledFile  = "dist/app/compose/docker-compose.yaml"

	// The identity the db node's Recipe resolves to, asserted whole rather than by
	// prefix; see the assertion site for why a prefix would not discriminate.
	composeArtifactVariantRecipeURI = "compose-db-sourced-with-artifacts:1.0.0"
)

// composeServiceLabels decodes an assembled compose document into per-service
// label maps. It reads through a decode rather than matching text, so the
// assertions below are about the emitted document and not about its formatting.
func composeServiceLabels(t *testing.T, body []byte) map[string]map[string]string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode assembled compose document: %v\n%s", err, body)
	}
	out := make(map[string]map[string]string, len(doc.Services))
	for name, svc := range doc.Services {
		out[name] = svc.Labels
	}
	return out
}

// TestComposeArtifactService_CarriesTheSameProvenanceAsTheSourcedServiceBesideIt
// is the parity proof, asserted key by key in the emitted bytes. A fixture
// checking only that `labels:` exists on the artifact-supplied service would
// pass on a partial splice, so the comparison is a set DIFFERENCE against the
// source-supplied service in the same document, naming whatever is missing or
// extra.
//
// The values are compared too, not only the keys: both services come from the
// same node, so a value that differs would mean the artifact path stamped a
// different node's identity — which no key-set comparison can see.
func TestComposeArtifactService_CarriesTheSameProvenanceAsTheSourcedServiceBesideIt(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  composeArtifactVariantRelease,
		CatalogPaths: []string{"testdata/fullstack-app/catalog/base", composeArtifactVariantRecipes},
	})

	body, ok := result.Artifacts[composeArtifactAssembledFile]
	if !ok {
		var got []string
		for path := range result.Artifacts {
			got = append(got, path)
		}
		sort.Strings(got)
		t.Fatalf("expected an assembled compose document at %q; emitted %v", composeArtifactAssembledFile, got)
	}

	labelsByService := composeServiceLabels(t, body)
	sourced, ok := labelsByService["db"]
	if !ok {
		t.Fatalf("the source path must contribute the db service; document:\n%s", body)
	}
	fromArtifact, ok := labelsByService["db-metrics"]
	if !ok {
		t.Fatalf("the artifact path must contribute the db-metrics service; document:\n%s", body)
	}

	// The sourced service is the reference rather than a hand-listed key set: the
	// two are produced by one node through one fold, so a key added to the shared
	// provenance builders has to appear on both or the comparison fails. A
	// hand-listed expectation would instead go quietly stale.
	if len(sourced) == 0 {
		t.Fatal("the source-supplied service carries no labels; the reference side of the parity comparison is empty")
	}

	var missing, extra []string
	for k := range sourced {
		if _, present := fromArtifact[k]; !present {
			missing = append(missing, k)
		}
	}
	for k := range fromArtifact {
		if _, present := sourced[k]; !present {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("the artifact-supplied service is missing provenance keys the source-supplied service carries: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("the artifact-supplied service carries keys the source-supplied service does not: %v", extra)
	}
	for k, want := range sourced {
		if got := fromArtifact[k]; got != want {
			t.Errorf("provenance key %q: artifact-supplied service has %q, source-supplied service has %q", k, got, want)
		}
	}

	// One value spelled out, so the test states which identity is expected rather
	// than only that the two agree: both services are emitted by the db node
	// through the db Recipe, and an artifact stamped from some other node's
	// context would satisfy a pure agreement check between two wrong values.
	if fromArtifact["aggrega.ai/component-id"] != "db" {
		t.Errorf("the artifact-supplied service must carry the emitting node's identity; component-id = %q", fromArtifact["aggrega.ai/component-id"])
	}
	// Pinned to the whole identity rather than to a prefix, and that is the point:
	// the artifact-less Recipe beside this one carries a name this Recipe's name
	// merely EXTENDS, so a prefix check is satisfied by either. Only the exact
	// identity establishes that the artifacts-declaring Recipe is the one that
	// rendered — which is the premise every assertion above rests on.
	if got := fromArtifact["aggrega.ai/recipe-uri"]; got != composeArtifactVariantRecipeURI {
		t.Errorf("the artifact-supplied service must carry the producing Recipe's identity; recipe-uri = %q, want %q", got, composeArtifactVariantRecipeURI)
	}

	// The unstampable half of the same build: the volumes-only fragment reaches
	// the assembled document, so the skip emitted it rather than dropping or
	// rewriting it. Its advisory is asserted where the placer produces it; what
	// is observable HERE is that the pass-through happened.
	if !strings.Contains(string(body), "db-archive:") {
		t.Errorf("the service-less artifact fragment must still reach the assembled document; document:\n%s", body)
	}
}
