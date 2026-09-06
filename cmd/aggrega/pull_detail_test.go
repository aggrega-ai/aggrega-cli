// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestPullDetailRendersTierKindRef checks the --detail line shape, the tier
// vocabulary, deterministic sorting, and — the load-bearing case — that a
// KindHint-empty entity (the top-level aggregate) still prints the correct Kind
// via the reference-site map.
func TestPullDetailRendersTierKindRef(t *testing.T) {
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{ // top-level aggregate: KindHint empty, kind derived from the site
			URI:            "hub.aggrega.ai/aggregate/example/app:1.0.0",
			ResolutionPath: "external",
			ReferencedFrom: []string{"release.spec.aggregate"},
		},
		{ // recipe via the Manifest components mapping value
			URI:            "adminer-helm:1.0.0",
			ResolutionPath: "tier-3-pull",
			ReferencedFrom: []string{"manifest.spec.mappings.components.type.app/adminer"},
		},
	}}

	var buf bytes.Buffer
	printPullDetail(&buf, cl)
	out := buf.String()

	if !strings.Contains(out, "resolved 2 entities (this pull):") {
		t.Errorf("missing header:\n%s", out)
	}
	// The KindHint-empty top-level aggregate must still render Kind=Aggregate.
	if !strings.Contains(out, "external") || !strings.Contains(out, "Aggregate") ||
		!strings.Contains(out, "← release.spec.aggregate") {
		t.Errorf("top-level aggregate line wrong (KindHint-empty derivation failed):\n%s", out)
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

// TestKindFromReferenceSite pins the reference-site → kind table, including the
// nuance that distinguishes a Manifest mapping value (Recipe/FeatureRecipe)
// from an instance attachment (Component/Feature), and an attachment nested
// under a component (Feature, not the enclosing Component). A credential
// attachment reaches the table through the same `features` collection, so it
// resolves to Feature — there is no second collection for it to arrive under.
func TestKindFromReferenceSite(t *testing.T) {
	cases := map[string]string{
		"release.spec.aggregate":                                               "Aggregate",
		"release.spec.manifest":                                                "Manifest",
		"manifest.spec.extends":                                                "Manifest",
		"manifest.spec.mappings.components.type.app/adminer":                   "Recipe",
		"manifest.spec.mappings.features.type.core/ingress":                    "FeatureRecipe",
		"aggregate.spec.components.api.type":                                   "Component",
		"aggregate.spec.aggregates.sub0.type":                                  "Aggregate",
		"aggregate.spec.components.api.features.cache.type":                    "Feature",
		"aggregate.spec.components.api.features.db.type":                       "Feature",
		"release.spec.boundaries.primary.components.api.features.ingress.type": "Feature",
		"something.unmapped":                                                   "",
	}
	for ref, want := range cases {
		if got := kindFromReferenceSite(ref); got != want {
			t.Errorf("kindFromReferenceSite(%q) = %q; want %q", ref, got, want)
		}
	}
}

// TestEntityKindForDetailPrefersHint confirms KindHint wins when present, and
// the reference-site map fills in the KindHint-empty kinds.
func TestEntityKindForDetailPrefersHint(t *testing.T) {
	if got := entityKindForDetail("aggregate", []string{"x"}); got != "Aggregate" {
		t.Errorf("KindHint aggregate => %q", got)
	}
	if got := entityKindForDetail("", []string{"aggregate.spec.components.api.type"}); got != "Component" {
		t.Errorf("empty hint, component site => %q", got)
	}
	if got := entityKindForDetail("", []string{"unmapped.site"}); got != "Entity" {
		t.Errorf("unmapped fallback => %q", got)
	}
}
