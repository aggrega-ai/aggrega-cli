// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLookupNameFromURI_PreservesPathSegments pins the URI-to-name
// helper that ClosureLookup's Tier-1 dispatch uses to find Component
// / Feature / Provider / Aggregate entries in the catalog. The
// helper strips only the trailing ":<version>" suffix; path segments
// must remain intact because catalog entries are indexed by the
// verbatim metadata.name string (which may itself contain "/"
// separators — e.g., "core/server", "hub.aggrega.ai/component/core/api-server").
//
// An earlier shape of this helper stripped to the last "/" segment,
// returning "server" for "core/server"; that lost load-bearing prefix
// content and prevented Tier-1 from finding entities whose
// metadata.name was a multi-segment URI.
func TestLookupNameFromURI_PreservesPathSegments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare-single-segment", "server", "server"},
		{"versioned-single-segment", "server:1.0.0", "server"},
		{"bare-two-segments", "core/server", "core/server"},
		{"versioned-two-segments", "core/server:1.0.0", "core/server"},
		{"versioned-registry-prefixed", "hub.aggrega.ai/component/core/api-server:1.0.0", "hub.aggrega.ai/component/core/api-server"},
		{"bare-registry-prefixed", "hub.aggrega.ai/component/core/api-server", "hub.aggrega.ai/component/core/api-server"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := lookupNameFromURI(tc.input)
			if got != tc.want {
				t.Errorf("lookupNameFromURI(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSchemaLibraryNameFromURI_TakesLastSegment pins the
// library-identifier extraction the SchemaLibrary / MappingLibrary
// Tier-1 dispatch uses. Library identifiers are PascalCase tokens at
// the URI's final segment (e.g., "Probes", "BaseMappings");
// canonical URIs may carry registry + namespace prefix segments
// which are stripped here to recover the catalog index key.
//
// The trailing ":<version>" suffix is stripped before the
// last-segment extraction.
func TestSchemaLibraryNameFromURI_TakesLastSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare-identifier", "Probes", "Probes"},
		{"versioned-bare", "Probes:1.0.0", "Probes"},
		{"namespaced", "core/Probes", "Probes"},
		{"versioned-namespaced", "core/Probes:1.0.0", "Probes"},
		{"versioned-registry-prefixed", "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0", "Probes"},
		{"versioned-mapping-library", "hub.aggrega.ai/mappinglibrary/core/BaseMappings:2.1.0", "BaseMappings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := schemaLibraryNameFromURI(tc.input)
			if got != tc.want {
				t.Errorf("schemaLibraryNameFromURI(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestTheReleasesOwnEntitiesAreRegisteredAndFoundByTheOneListOfThem registers a
// Release's Aggregate and Manifest, which share a name, through the registration
// every caller uses, then resolves the name. Every entity the one list names is
// registered under its own identity; a reference collected as each kind finds its
// own document; and a reference whose kind its caller does not know finds the
// Aggregate, the first the list names.
func TestTheReleasesOwnEntitiesAreRegisteredAndFoundByTheOneListOfThem(t *testing.T) {
	t.Parallel()
	const name = "imported/flow"
	aggregate := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: imported/flow\n")
	manifest := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: imported/flow\n")
	rel := &types.Release{}
	rel.Spec.Aggregate, rel.Spec.Manifest = name, name
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggregate, manifest)
	for _, key := range closure.ReleaseExternals(rel) {
		if _, registered := externals[key]; !registered {
			t.Errorf("%s is not registered", key.Quoted())
		}
	}
	if got, want := closure.ReleaseExternalKinds(), []string{closure.KindAggregate, closure.KindManifest}; len(got) != len(want) ||
		got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ReleaseExternalKinds() = %v; want %v", got, want)
	}

	cat, err := LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookupWithExternals(nil, t.TempDir(), "", externals)
	for _, tc := range []struct {
		hint string
		want []byte
	}{
		{closure.ReleaseAggregateKindHint, aggregate},
		{closure.KindManifest, manifest},
		{"", aggregate},
	} {
		res, err := lookup.ResolveCatalogEntity(t.Context(), name, tc.hint)
		if err != nil {
			t.Fatalf("hint %q: ResolveCatalogEntity: %v", tc.hint, err)
		}
		if want := oci.DocumentDigest(tc.want); res.NotFound || res.Digest != want {
			t.Errorf("hint %q: resolved NotFound=%v digest %s; want the digest %s of its own document",
				tc.hint, res.NotFound, res.Digest, want)
		}
	}
}

// TestARegistrySeedAnswersOnlyAReferenceOfItsOwnKind seeds the Release's own
// Aggregate as a registry resolution, keyed by its identity, and resolves its name
// under every hint the Release's own entities are collected with. The Aggregate
// reference and a reference whose kind its caller does not know take the seed; the
// Manifest of the same name never does, and misses on an empty catalog.
func TestARegistrySeedAnswersOnlyAReferenceOfItsOwnKind(t *testing.T) {
	t.Parallel()
	const name = "imported/flow"
	seed := closure.Resolution{ResolvedVersion: "1.2.0", Digest: "sha256:seeded", ResolutionPath: closure.ResolutionPathRegistry}
	cat, err := LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookupWithReuse(nil, t.TempDir(), "", nil,
		map[closure.EntityKey]closure.Resolution{{Kind: closure.KindAggregate, URI: name}: seed}, nil)
	for _, tc := range []struct {
		hint     string
		wantSeed bool
	}{
		{closure.ReleaseAggregateKindHint, true},
		{"", true},
		{closure.KindManifest, false},
	} {
		res, err := lookup.ResolveCatalogEntity(t.Context(), name, tc.hint)
		if err != nil {
			t.Fatalf("hint %q: ResolveCatalogEntity: %v", tc.hint, err)
		}
		if got := !res.NotFound && res.Digest == seed.Digest; got != tc.wantSeed {
			t.Errorf("hint %q: answered by the seed = %v (NotFound=%v digest %q); want %v",
				tc.hint, got, res.NotFound, res.Digest, tc.wantSeed)
		}
	}
}

// fetchBindingPuller records which registry fetch a Tier-3 pull made — the entity
// binding by kind, or the already-located reference — and serves neither, so the
// resolution misses once the fetch is recorded.
type fetchBindingPuller struct {
	bound     bool
	boundKind string
	located   bool
}

func (p *fetchBindingPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	p.located = true
	return nil, oci.ErrFetchNotFound()
}

func (p *fetchBindingPuller) PullEntity(_ context.Context, _, kind, _ string) (*oci.PullResult, string, error) {
	p.bound, p.boundKind = true, kind
	return nil, "", oci.ErrFetchNotFound()
}

// TestARegistryFetchBindsAReferenceByTheKindItsSlotIsKeptUnder drives the closure
// lookup over an empty catalog, so every reference reaches the registry tier, with
// a reference collected under every hint the walk records, an empty hint and an
// undeclared word. A reference collected as a Component, a Feature, a Recipe, a
// FeatureRecipe or a Plugin is fetched by the entity binding under the published
// kind its cache slot is kept under. An Aggregate — the Release's own or one an
// Aggregate includes — is fetched as the already-located reference it is, and so is
// a reference collected as no published kind: a Manifest, which no registry
// publishes, or one whose kind its caller did not know.
//
// The expected binding is spelled per hint rather than derived, so a fetch kind
// that stops agreeing with the slot kind for any hint fails here.
func TestARegistryFetchBindsAReferenceByTheKindItsSlotIsKeptUnder(t *testing.T) {
	t.Parallel()
	cat, err := LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	boundAs := map[string]string{
		closure.KindComponent:     "Component",
		closure.KindFeature:       "Feature",
		closure.KindRecipe:        "Recipe",
		closure.KindFeatureRecipe: "FeatureRecipe",
		closure.KindPlugin:        "Plugin",
	}
	hints := append(closure.DeclaredEntityKinds(), closure.ReleaseAggregateKindHint, "", "unknown")
	for _, hint := range hints {
		t.Run("hint "+hint, func(t *testing.T) {
			t.Parallel()
			p := &fetchBindingPuller{}
			lookup := cat.ClosureLookupWithReuse(p, t.TempDir(), "", nil, nil, nil)
			res, err := lookup.ResolveCatalogEntity(t.Context(), "hub.aggrega.ai/entity/core/probe:1.0.0", hint)
			if err != nil {
				t.Fatalf("ResolveCatalogEntity: %v", err)
			}
			if !res.NotFound {
				t.Fatalf("the reference resolved; the catalog is empty and the registry serves nothing")
			}
			if want, bound := boundAs[hint]; bound {
				if !p.bound || p.boundKind != want || p.located {
					t.Errorf("fetch bound=%v as %q, located=%v; want bound by the entity binding as %q only",
						p.bound, p.boundKind, p.located, want)
				}
				return
			}
			if p.bound || !p.located {
				t.Errorf("fetch bound=%v as %q, located=%v; want fetched as the already-located reference only",
					p.bound, p.boundKind, p.located)
			}
		})
	}
}
