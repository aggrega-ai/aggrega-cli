// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that a Release boundary's declared
// deployment wave orders the services of the compose file it renders into, and
// that a build declaring no wave renders the same bytes it rendered before the
// ordering existed.

package compiler_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const composeSyncWavePath = "dist/cluster/compose/docker-compose.yaml"

// composeSyncWaveBuild compiles one of the compose-sync-wave fixture's Releases
// and returns the single assembled compose document the three boundaries collapse
// into.
func composeSyncWaveBuild(t *testing.T, releasePath string) string {
	t.Helper()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  releasePath,
		CatalogPaths: []string{"testdata/compose-sync-wave/catalog/base"},
	})
	body, ok := result.Artifacts[composeSyncWavePath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected the merged compose file at %q; got %v", composeSyncWavePath, paths)
	}
	return string(body)
}

// composeDependsOn decodes the assembled document and returns each service's
// depends_on entries as service name → condition, with an empty condition for an
// entry emitted in the short list form.
//
// It decodes rather than searching the text because a substring search cannot say
// which service an occurrence belongs to, and the whole claim here is about which
// service waits on which.
func composeDependsOn(t *testing.T, body string) map[string]map[string]string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			DependsOn yaml.Node `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("assembled document does not parse as YAML: %v\n--- merged ---\n%s", err, body)
	}
	out := map[string]map[string]string{}
	for name, svc := range doc.Services {
		entries := map[string]string{}
		switch svc.DependsOn.Kind {
		case 0:
			// No depends_on on this service.
		case yaml.SequenceNode:
			var names []string
			if err := svc.DependsOn.Decode(&names); err != nil {
				t.Fatalf("service %q short-form depends_on does not decode: %v", name, err)
			}
			for _, n := range names {
				entries[n] = ""
			}
		case yaml.MappingNode:
			var long map[string]struct {
				Condition string `yaml:"condition"`
			}
			if err := svc.DependsOn.Decode(&long); err != nil {
				t.Fatalf("service %q long-form depends_on does not decode: %v", name, err)
			}
			for n, b := range long {
				entries[n] = b.Condition
			}
		default:
			t.Fatalf("service %q depends_on is neither a list nor a mapping", name)
		}
		out[name] = entries
	}
	return out
}

// TestComposeSyncWave_DeclaredWaveOrdersServicesInTheRenderedFile compiles a
// Release whose three boundaries share one directory: — so they collapse into a
// single docker-compose.yaml — at waves -1, 0 and 1, and asserts the emitted
// depends_on expresses that ordering with the condition each target can satisfy.
//
// store declares a healthcheck and migrator does not, so:
//
//   - store (wave 0) waits on migrator (wave -1) gated on migrator's START,
//     because a service with no healthcheck can never report healthy and a
//     health-gated wait on it would never be satisfied;
//   - frontend (wave 1) waits on store (wave 0) gated on store's HEALTH;
//   - frontend does not name migrator, because the ordering fans out to the
//     immediately-preceding populated wave only and compose chains the rest;
//   - migrator, in the lowest wave, waits on nothing.
//
// frontend also `uses` store, so that pair is named by both an edge and the wave.
// It must emit as one entry carrying the wave's condition rather than twice.
func TestComposeSyncWave_DeclaredWaveOrdersServicesInTheRenderedFile(t *testing.T) {
	t.Parallel()
	merged := composeSyncWaveBuild(t, "testdata/compose-sync-wave/release.yaml")
	deps := composeDependsOn(t, merged)

	for _, svc := range []string{"migrator", "store", "frontend"} {
		if _, ok := deps[svc]; !ok {
			t.Fatalf("service %q missing from the merged compose file\n--- merged ---\n%s", svc, merged)
		}
	}

	if got, want := deps["store"], map[string]string{"migrator": "service_started"}; !equalStringMap(got, want) {
		t.Errorf("store's depends_on = %v, want %v\n--- merged ---\n%s", got, want, merged)
	}
	if got, want := deps["frontend"], map[string]string{"store": "service_healthy"}; !equalStringMap(got, want) {
		t.Errorf("frontend's depends_on = %v, want %v\n--- merged ---\n%s", got, want, merged)
	}
	if len(deps["migrator"]) != 0 {
		t.Errorf("the lowest wave must wait on nothing; migrator's depends_on = %v", deps["migrator"])
	}

	// The wave is still recorded on each service as a label. The ordering is
	// added beside that record, not in place of it: the label is what a reader
	// inspecting the emitted file consults to see which wave a service came from,
	// which the depends_on entries do not state.
	for _, want := range []string{`aggrega.ai/sync-wave: "1"`, `aggrega.ai/sync-wave: "-1"`} {
		if !strings.Contains(merged, want) {
			t.Errorf("the wave provenance label was dropped: missing %s\n--- merged ---\n%s", want, merged)
		}
	}
}

// TestComposeSyncWave_NoDeclaredWaveIsUnchanged compiles the same three
// boundaries with no wave declared anywhere and asserts the assembled file
// carries no wave-derived ordering at all — the shape every compose corpus that
// declares no wave has.
//
// 🔴 The difference between the two builds is asserted ON THE ORDERING, not on the
// documents merely differing. The two documents also differ in the provenance
// label the wave is recorded under, so "the outputs are not equal" would be
// satisfied by the label alone and would pass however inert the ordering was.
// Bracketing it — the wave-declaring build HAS a health-gated entry and the
// wave-free build has NO gated entry at all — is what makes the comparison
// load-bearing.
func TestComposeSyncWave_NoDeclaredWaveIsUnchanged(t *testing.T) {
	t.Parallel()
	waved := composeSyncWaveBuild(t, "testdata/compose-sync-wave/release.yaml")
	plain := composeSyncWaveBuild(t, "testdata/compose-sync-wave/release-no-wave.yaml")

	if !strings.Contains(waved, "condition: service_healthy") {
		t.Fatalf("the wave-declaring build carries no gated dependency, so the comparison below proves nothing\n--- merged ---\n%s", waved)
	}

	// With no wave declared, nothing is gated: the only dependency left is the
	// edge-derived one, which emits in the short list form.
	if strings.Contains(plain, "condition:") {
		t.Errorf("a build declaring no wave emitted a gated depends_on\n--- merged ---\n%s", plain)
	}
	deps := composeDependsOn(t, plain)
	if got, want := deps["frontend"], map[string]string{"store": ""}; !equalStringMap(got, want) {
		t.Errorf("frontend's depends_on = %v, want the ungated edge-derived entry %v\n--- merged ---\n%s", got, want, plain)
	}
	for _, svc := range []string{"migrator", "store"} {
		if len(deps[svc]) != 0 {
			t.Errorf("service %q gained a dependency with no wave declared: %v", svc, deps[svc])
		}
	}
}

// equalStringMap reports whether two string maps carry the same keys and values.
func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
