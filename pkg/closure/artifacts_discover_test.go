// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestDiscoverArtifactLibrariesTransitive asserts the discovery fixpoint on the
// artifacts axis: a recipe artifacts entry `$import:`s LibA, a file body inside
// LibA's bundle `$import:`s LibB, so both are discovered and resolved — discovery
// descends a resolved library's bundle file bodies, not just the consumer
// artifacts entries.
func TestDiscoverArtifactLibrariesTransitive(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		artifactsLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumers := []closure.ArtifactsConsumer{
		{URI: "consumer", Template: "$import: LibA#/artifacts/Bundle"},
	}
	body := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
		if lib.URI == "LibA" {
			return []string{"$import: LibB#/artifacts/Other"}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverArtifactLibraries(context.Background(), lookup, consumers, body); err != nil {
		t.Fatalf("DiscoverArtifactLibraries: %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.ArtifactLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected transitive discovery of LibA and LibB; got %+v", c.ArtifactLibraries)
	}
}

// TestDiscoverArtifactLibrariesCycleTerminates asserts the visited-set terminates
// a library↔library reference cycle at discovery on the artifacts axis: a file in
// LibA's bundle `$import:`s LibB and a file in LibB's bundle `$import:`s LibA.
// Discovery descends each library once and returns without error.
func TestDiscoverArtifactLibrariesCycleTerminates(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		artifactsLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumers := []closure.ArtifactsConsumer{
		{URI: "consumer", Template: "$import: LibA#/artifacts/Bundle"},
	}
	body := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
		switch lib.URI {
		case "LibA":
			return []string{"$import: LibB#/artifacts/Other"}, true, nil
		case "LibB":
			return []string{"$import: LibA#/artifacts/Bundle"}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverArtifactLibraries(context.Background(), lookup, consumers, body); err != nil {
		t.Fatalf("cycle should terminate cleanly at discovery; got %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.ArtifactLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected both cycle members discovered; got %+v", c.ArtifactLibraries)
	}
}

// TestDiscoverArtifactLibrariesIdentityGate asserts the walker records only
// host-less identity `#/artifacts/` references: a local-file import and a
// host-bearing `oci://…` form are not cross-library edges and yield an empty
// ArtifactLibrary closure.
func TestDiscoverArtifactLibrariesIdentityGate(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		artifactsLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
		},
	}
	noBody := func(closure.ResolvedLibrary) ([]string, bool, error) { return nil, false, nil }

	cases := map[string]string{
		"local-file":   "$import: ./artifacts/crd.yaml",
		"host-bearing": "$import: oci://hub.aggrega.ai/LibA#/artifacts/Bundle",
		"inline":       "apiVersion: v1\nkind: ConfigMap\n",
	}
	for name, tmpl := range cases {
		tmpl := tmpl
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var c closure.Closure
			if err := c.DiscoverArtifactLibraries(context.Background(), lookup,
				[]closure.ArtifactsConsumer{{URI: "consumer", Template: tmpl}}, noBody); err != nil {
				t.Fatalf("DiscoverArtifactLibraries: %v", err)
			}
			if len(c.ArtifactLibraries) != 0 {
				t.Errorf("expected no artifact-library edge for %q; got %+v", tmpl, c.ArtifactLibraries)
			}
		})
	}
}
