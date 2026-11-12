// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestPullDetailRendersTierKindRef checks the --detail line shape, the tier
// vocabulary, deterministic sorting, and that the Release's own Aggregate, whose
// hint is its own word, prints as an Aggregate.
func TestPullDetailRendersTierKindRef(t *testing.T) {
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{ // the Release's own Aggregate
			URI:            "hub.aggrega.ai/aggregate/example/app:1.0.0",
			ResolutionPath: "external",
			ReferencedFrom: []string{"release.spec.aggregate"},
			KindHint:       closure.ReleaseAggregateKindHint,
		},
		{ // recipe via the Manifest components mapping value
			URI:            "adminer-helm:1.0.0",
			ResolutionPath: "tier-3-pull",
			ReferencedFrom: []string{"manifest.spec.mappings.components.type.app/adminer"},
			KindHint:       closure.KindRecipe,
		},
	}}

	var buf bytes.Buffer
	printPullDetail(&buf, cl)
	out := buf.String()

	if !strings.Contains(out, "resolved 2 entities (this pull):") {
		t.Errorf("missing header:\n%s", out)
	}
	// The Release's own Aggregate must render Kind=Aggregate.
	if !strings.Contains(out, "external") || !strings.Contains(out, "Aggregate") ||
		!strings.Contains(out, "← release.spec.aggregate") {
		t.Errorf("top-level aggregate line wrong:\n%s", out)
	}
	if !strings.Contains(out, "fetched-fresh") || !strings.Contains(out, "Recipe") {
		t.Errorf("recipe line wrong:\n%s", out)
	}
	// Deterministic: sorted by URI, so the aggregate URI sorts before adminer? No —
	// "adminer-helm" < "hub.aggrega.ai/..." lexically, so adminer comes first.
	if idxAdminer, idxAgg := strings.Index(out, "adminer-helm"), strings.Index(out, "aggregate/example/app"); idxAdminer > idxAgg {
		t.Errorf("rows not sorted by URI:\n%s", out)
	}
	// Idempotent render (no map-order nondeterminism).
	var buf2 bytes.Buffer
	printPullDetail(&buf2, cl)
	if buf.String() != buf2.String() {
		t.Errorf("non-deterministic --detail output")
	}
}

// TestPullDetailRendersLibraryAxis confirms a pulled SchemaLibrary / MappingLibrary
// is surfaced as `<tier>  <Kind>  <uri>` with NO referenced-from column (a
// library's only reference is its consumer chain — inspect territory), while an
// entity row in the same render still carries its `← <referenced_from>`.
func TestPullDetailRendersLibraryAxis(t *testing.T) {
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{{
			URI:            "hub.aggrega.ai/component/core/api:1.0.0",
			ResolutionPath: "tier-1-catalog",
			ReferencedFrom: []string{"aggregate.spec.components.api.type"},
		}},
		SchemaLibraries: []closure.ResolvedLibrary{{
			URI:            "PortSpec",
			ResolutionPath: "tier-3-pull",
			Consumers:      []closure.LibraryConsumer{{ConsumerURI: "hub.aggrega.ai/component/core/api:1.0.0"}},
		}},
		MappingLibraries: []closure.ResolvedLibrary{{
			URI:            "EnvMap",
			ResolutionPath: "tier-2-cache",
		}},
	}

	var buf bytes.Buffer
	printPullDetail(&buf, cl)
	out := buf.String()

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case strings.Contains(line, "SchemaLibrary"):
			if !strings.Contains(line, "fetched-fresh") || !strings.Contains(line, "PortSpec") {
				t.Errorf("schema-library row wrong: %q", line)
			}
			if strings.Contains(line, "←") {
				t.Errorf("library row must not carry a referenced-from column: %q", line)
			}
		case strings.Contains(line, "MappingLibrary"):
			if !strings.Contains(line, "cache-reuse") || strings.Contains(line, "←") {
				t.Errorf("mapping-library row wrong: %q", line)
			}
		case strings.Contains(line, "core/api"):
			// the entity row still carries its reference site
			if !strings.Contains(line, "← aggregate.spec.components.api.type") {
				t.Errorf("entity row lost its referenced-from: %q", line)
			}
		}
	}
	// No consumer chain / consumer URI leaked into the output (stays out of inspect territory).
	if strings.Contains(out, "consumer") || strings.Contains(out, "Consumer") {
		t.Errorf("--detail leaked consumer-chain detail:\n%s", out)
	}
}

// TestADetailRowNamesTheKindTheWalkCollectedTheEntityAs pins the kind each entity
// row prints to the kind the walk recorded for it, named as an author writes it —
// whatever site it was recorded at. A Component a Manifest's components mapping
// names as a key is a Component, though its site is the mapping's; a Plugin is a
// Plugin; and only an entity recorded with no kind prints "Entity". The expected
// words are spelled per row rather than derived, so a row naming a kind another
// way fails here.
func TestADetailRowNamesTheKindTheWalkCollectedTheEntityAs(t *testing.T) {
	for _, tc := range []struct {
		hint, site, want string
	}{
		{closure.KindComponent, "manifest.spec.mappings.components.type.core/cache", "Component"},
		{closure.KindRecipe, "manifest.spec.mappings.components.type.core/cache", "Recipe"},
		{closure.KindFeature, "manifest.spec.mappings.features.type.core/ingress", "Feature"},
		{closure.KindFeatureRecipe, "manifest.spec.mappings.features.type.core/ingress", "FeatureRecipe"},
		{closure.KindPlugin, ".aggrega-config.yaml.plugins.governance", "Plugin"},
		{closure.KindComponent, "aggregate.spec.components.api.type", "Component"},
		{closure.KindFeature, "aggregate.spec.components.api.features.cache.type", "Feature"},
		{closure.KindAggregate, "aggregate.spec.aggregates.sub0.type", "Aggregate"},
		{closure.ReleaseAggregateKindHint, "release.spec.aggregate", "Aggregate"},
		{closure.KindManifest, "release.spec.manifest", "Manifest"},
		{"", "aggregate.spec.components.api.type", "Entity"},
	} {
		e := closure.ResolvedRef{URI: "core/cache:1.0.0", KindHint: tc.hint, ReferencedFrom: []string{tc.site}}
		if got := entityKindForDetail(e); got != tc.want {
			t.Errorf("an entity collected under hint %q at %s prints kind %q; want %q", tc.hint, tc.site, got, tc.want)
		}
	}
}

// TestAComponentAndARecipeOfOneNameAtOneMappingSiteAreTwoRowsInAFixedOrder renders
// a Component and the Recipe of the same reference that a Manifest's components
// mapping binds to it — one reference, one reference site, two kinds — and asserts
// both rows print, each under its own kind, in the same order whichever the
// closure lists first.
func TestAComponentAndARecipeOfOneNameAtOneMappingSiteAreTwoRowsInAFixedOrder(t *testing.T) {
	const site = "manifest.spec.mappings.components.type.core/cache"
	component := closure.ResolvedRef{URI: "core/cache", KindHint: closure.KindComponent, ReferencedFrom: []string{site}}
	recipe := closure.ResolvedRef{URI: "core/cache", KindHint: closure.KindRecipe, ReferencedFrom: []string{site}}
	var first, second bytes.Buffer
	printPullDetail(&first, closure.Closure{Entities: []closure.ResolvedRef{component, recipe}})
	printPullDetail(&second, closure.Closure{Entities: []closure.ResolvedRef{recipe, component}})
	if first.String() != second.String() {
		t.Errorf("the two rows print in the order the closure lists them:\n%s\n---\n%s", first.String(), second.String())
	}
	out := first.String()
	component1, recipe1 := strings.Index(out, "Component"), strings.Index(out, "Recipe")
	if component1 < 0 || recipe1 < 0 {
		t.Fatalf("want one Component row and one Recipe row:\n%s", out)
	}
}
