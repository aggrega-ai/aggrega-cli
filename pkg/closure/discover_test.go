// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

func resolvedLib(version, digest string) closure.LibraryResolution {
	return closure.LibraryResolution{Resolution: closure.Resolution{
		ResolvedVersion: version,
		Digest:          digest,
		ResolutionPath:  "tier-1-catalog",
	}}
}

// TestDiscoverSchemaLibrariesTransitive asserts the discovery fixpoint: a
// consumer $ref:s LibA, LibA's own entry body $ref:s LibB, so both are discovered
// and resolved — discovery descends a resolved library's body, not just the
// consumer schemas.
func TestDiscoverSchemaLibrariesTransitive(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		schemaLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumer := mustParse(t, `$ref: "LibA#/schemas/Foo"`)
	libABody := mustParse(t, `$ref: "LibB#/schemas/Bar"`)
	body := func(lib closure.ResolvedLibrary) ([]*yaml.Node, bool, error) {
		if lib.URI == "LibA" {
			return []*yaml.Node{libABody}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverSchemaLibraries(lookup,
		[]closure.SchemaConsumer{{URI: "consumer", Schema: consumer}}, body); err != nil {
		t.Fatalf("DiscoverSchemaLibraries: %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.SchemaLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected transitive discovery of LibA and LibB; got %+v", c.SchemaLibraries)
	}
}

// TestDiscoverSchemaLibrariesCycleTerminates asserts the visited-set terminates a
// library↔library reference cycle at discovery: LibA's body $ref:s LibB and
// LibB's body $ref:s LibA. Discovery descends each library once (keyed on
// canonical identity) and returns without error or unbounded recursion — the
// cycle itself is the $ref expander's concern (ErrCircularRef), not discovery's.
func TestDiscoverSchemaLibrariesCycleTerminates(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		schemaLibs: map[string]closure.LibraryResolution{
			"LibA": resolvedLib("1.0.0", "sha256:aaa"),
			"LibB": resolvedLib("1.0.0", "sha256:bbb"),
		},
	}
	consumer := mustParse(t, `$ref: "LibA#/schemas/Foo"`)
	libA := mustParse(t, `$ref: "LibB#/schemas/Bar"`)
	libB := mustParse(t, `$ref: "LibA#/schemas/Foo"`)
	body := func(lib closure.ResolvedLibrary) ([]*yaml.Node, bool, error) {
		switch lib.URI {
		case "LibA":
			return []*yaml.Node{libA}, true, nil
		case "LibB":
			return []*yaml.Node{libB}, true, nil
		}
		return nil, true, nil
	}

	var c closure.Closure
	if err := c.DiscoverSchemaLibraries(lookup,
		[]closure.SchemaConsumer{{URI: "consumer", Schema: consumer}}, body); err != nil {
		t.Fatalf("cycle should terminate cleanly at discovery; got %v", err)
	}

	got := map[string]bool{}
	for _, lib := range c.SchemaLibraries {
		got[lib.URI] = true
	}
	if !got["LibA"] || !got["LibB"] {
		t.Errorf("expected both cycle members discovered; got %+v", c.SchemaLibraries)
	}
}
