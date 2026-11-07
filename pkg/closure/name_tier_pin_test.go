// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The tests in this file hold that a realization a Manifest binds by instance name
// is pinned exactly as one bound by type: the one lock-time walk records it, every
// realization the build's lookup can pick is among what the walk recorded, and a
// lockfile without its row is refused the way a lockfile without a type-tier row
// is, naming the mapping entry — and so the instance name — that asked for it.
//
// With the walk reading the type tier alone, the name-tier rows below go absent:
// the first test misses their URIs, the second finds a realization the lookup
// returns that no entity covers, and the third finds no row to remove.

// nameTierManifest binds one component instance and one feature attachment by
// instance name, beside the type tier every other instance resolves through.
const nameTierManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        app/db:1.0.0: helm/db-bulk:1.0.0
      name:
        db-primary: terraform/db-managed:1.0.0
    features:
      type:
        sec/cred:1.0.0: helm/cred-existing:1.0.0
      name:
        password: helm/cred-external:1.0.0
`

// nameTierLookup resolves every reference the fixtures in this file name, each
// to a digest of its own, so a row recorded under the wrong reference is visible.
func nameTierLookup() *fakeLookup {
	ents := map[string]closure.Resolution{}
	for _, uri := range []string{
		"app/db:1.0.0", "helm/db-bulk:1.0.0", "terraform/db-managed:1.0.0",
		"sec/cred:1.0.0", "helm/cred-existing:1.0.0", "helm/cred-external:1.0.0",
	} {
		ents[uri] = resolutionOK("1.0.0", "sha256:"+uri, "tier1-local")
	}
	return &fakeLookup{entities: ents, plugins: map[string]closure.Resolution{}}
}

// closureOfManifest runs the lock-time walk over a Manifest alone.
func closureOfManifest(t *testing.T, src string) (closure.Closure, *types.Manifest) {
	t.Helper()
	man := loadManifest(t, src)
	c, err := closure.ComputeClosure(context.Background(), &types.Release{}, man,
		types.NormalizedAggregate{}, workspaceconfig.Default(), nameTierLookup())
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	return c, man
}

// TestTheWalkPinsWhatEitherMappingTierBinds holds the walk to every realization a
// mapping binds, in either tier: each is recorded with the kind its position gives
// it and under the mapping entry that binds it, an instance name is never recorded
// as an entity, and a Manifest with no name tier records exactly what it recorded
// before.
func TestTheWalkPinsWhatEitherMappingTierBinds(t *testing.T) {
	t.Parallel()

	type pin struct {
		kind           string
		referencedFrom []string
	}
	manifestOf := func(mappings string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: m\n  version: \"1.0.0\"\nspec:\n  mappings:\n" + mappings
	}
	cases := []struct {
		name     string
		manifest string
		want     map[string]pin
	}{
		{
			name: "a component realization bound by instance name",
			manifest: manifestOf(`    components:
      type:
        app/db:1.0.0: helm/db-bulk:1.0.0
      name:
        db-primary: terraform/db-managed:1.0.0
`),
			want: map[string]pin{
				"app/db:1.0.0":               {"component", []string{"manifest.spec.mappings.components.type.app/db:1.0.0"}},
				"helm/db-bulk:1.0.0":         {"recipe", []string{"manifest.spec.mappings.components.type.app/db:1.0.0"}},
				"terraform/db-managed:1.0.0": {"recipe", []string{"manifest.spec.mappings.components.name.db-primary"}},
			},
		},
		{
			name: "a feature realization bound by attachment name",
			manifest: manifestOf(`    features:
      type:
        sec/cred:1.0.0: helm/cred-existing:1.0.0
      name:
        password: helm/cred-external:1.0.0
`),
			want: map[string]pin{
				"sec/cred:1.0.0":           {"feature", []string{"manifest.spec.mappings.features.type.sec/cred:1.0.0"}},
				"helm/cred-existing:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.type.sec/cred:1.0.0"}},
				"helm/cred-external:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.name.password"}},
			},
		},
		{
			name: "a name tier with no type tier beside it",
			manifest: manifestOf(`    components:
      name:
        db-primary: terraform/db-managed:1.0.0
`),
			want: map[string]pin{
				"terraform/db-managed:1.0.0": {"recipe", []string{"manifest.spec.mappings.components.name.db-primary"}},
			},
		},
		{
			name: "one realization bound by both tiers is one row naming both entries",
			manifest: manifestOf(`    components:
      type:
        app/db:1.0.0: helm/db-bulk:1.0.0
      name:
        db-primary: helm/db-bulk:1.0.0
`),
			want: map[string]pin{
				"app/db:1.0.0": {"component", []string{"manifest.spec.mappings.components.type.app/db:1.0.0"}},
				"helm/db-bulk:1.0.0": {"recipe", []string{
					"manifest.spec.mappings.components.name.db-primary",
					"manifest.spec.mappings.components.type.app/db:1.0.0",
				}},
			},
		},
		{
			name: "the type tier alone records what it always recorded",
			manifest: manifestOf(`    components:
      type:
        app/db:1.0.0: helm/db-bulk:1.0.0
    features:
      type:
        sec/cred:1.0.0: helm/cred-existing:1.0.0
`),
			want: map[string]pin{
				"app/db:1.0.0":             {"component", []string{"manifest.spec.mappings.components.type.app/db:1.0.0"}},
				"helm/db-bulk:1.0.0":       {"recipe", []string{"manifest.spec.mappings.components.type.app/db:1.0.0"}},
				"sec/cred:1.0.0":           {"feature", []string{"manifest.spec.mappings.features.type.sec/cred:1.0.0"}},
				"helm/cred-existing:1.0.0": {"feature_recipe", []string{"manifest.spec.mappings.features.type.sec/cred:1.0.0"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := closureOfManifest(t, tc.manifest)
			got := map[string]pin{}
			for _, e := range c.Entities {
				got[e.URI] = pin{kind: e.KindHint, referencedFrom: e.ReferencedFrom}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the walk recorded\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestEveryRealizationTheLookupPicksIsPinned holds the lookup a build and a pull's
// source descent read against the walk the lockfile is written from: for an
// instance the name tier overrides, for its siblings, and for a lookup with no
// instance in scope, the realization the lookup returns is one the walk recorded.
// Each row also names the realization it expects, so a row that stopped reaching
// the name tier cannot pass by resolving through the type tier.
func TestEveryRealizationTheLookupPicksIsPinned(t *testing.T) {
	t.Parallel()

	c, man := closureOfManifest(t, nameTierManifest)
	pinned := map[string]bool{}
	for _, e := range c.Entities {
		pinned[e.URI] = true
	}

	cases := []struct {
		name     string
		lookup   func(*types.Manifest, string, string) (string, bool, error)
		instance string
		typ      string
		want     string
	}{
		{"a component the name tier overrides", catalog.ComponentRecipeURI, "db-primary", "app/db:1.0.0", "terraform/db-managed:1.0.0"},
		{"a sibling component of the same type", catalog.ComponentRecipeURI, "db-replica", "app/db:1.0.0", "helm/db-bulk:1.0.0"},
		{"a component lookup with no instance in scope", catalog.ComponentRecipeURI, "", "app/db:1.0.0", "helm/db-bulk:1.0.0"},
		{"a feature attachment the name tier overrides", catalog.FeatureRecipeURI, "password", "sec/cred:1.0.0", "helm/cred-external:1.0.0"},
		{"a sibling attachment of the same feature type", catalog.FeatureRecipeURI, "app_password", "sec/cred:1.0.0", "helm/cred-existing:1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uri, ok, err := tc.lookup(man, tc.instance, tc.typ)
			if err != nil || !ok {
				t.Fatalf("lookup(%q, %q) = (%q, %v, %v); want a mapped realization", tc.instance, tc.typ, uri, ok, err)
			}
			if uri != tc.want {
				t.Fatalf("lookup(%q, %q) = %q; want %q", tc.instance, tc.typ, uri, tc.want)
			}
			if !pinned[uri] {
				t.Errorf("the lookup picks %q for %q, and the walk the lockfile is written from records no row for it; pinned: %v",
					uri, tc.instance, slices.Sorted(maps.Keys(pinned)))
			}
		})
	}
}

// TestALockfileWithoutANameTierPinIsRefusedAsDrift holds the build's verify to a
// name-tier realization exactly as to a type-tier one. A lockfile written from the
// walk is accepted; with one realization's row removed it is refused under the
// lockfile-drift condition, the entry naming the realization and the mapping entry
// that binds it — for a name-tier binding, the instance name. The type-tier row is
// the control: the same condition, worded the same way.
func TestALockfileWithoutANameTierPinIsRefusedAsDrift(t *testing.T) {
	t.Parallel()

	c, _ := closureOfManifest(t, nameTierManifest)
	full := lockfileOfEntities(c)
	if err := lockfile.Verify(full, c); err != nil {
		t.Fatalf("positive control: the lockfile written from the walk must verify; got %v", err)
	}

	cases := []struct {
		name string
		drop string
		kind string
		site string
	}{
		{"a component realization bound by instance name", "terraform/db-managed:1.0.0", closure.KindRecipe, "manifest.spec.mappings.components.name.db-primary"},
		{"a feature realization bound by attachment name", "helm/cred-external:1.0.0", closure.KindFeatureRecipe, "manifest.spec.mappings.features.name.password"},
		{"a realization bound by type (control)", "helm/db-bulk:1.0.0", closure.KindRecipe, "manifest.spec.mappings.components.type.app/db:1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := lockfileOfEntities(c)
			kept := lf.Entities[:0]
			for _, e := range lf.Entities {
				if e.URI != tc.drop {
					kept = append(kept, e)
				}
			}
			if len(kept) == len(lf.Entities) {
				t.Fatalf("precondition: the lockfile written from the walk carries no row for %q", tc.drop)
			}
			lf.Entities = kept

			err := lockfile.Verify(lf, c)
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("a lockfile without the row for %q must be refused as drift; got %v", tc.drop, err)
			}
			want := `entity "` + tc.drop + `" (` + tc.kind + `) not in lockfile (referenced from ` + tc.site + `)`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not carry %q:\n%v", want, err)
			}
		})
	}
}

// lockfileOfEntities copies a closure's entity rows into a lockfile field for
// field, as a pull does before it writes one.
func lockfileOfEntities(c closure.Closure) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{Version: 1, Release: "release.yaml", GeneratedAt: walkOrderGeneratedAt}
	for _, e := range c.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{
			Kind:            e.Kind(),
			URI:             e.URI,
			ResolvedVersion: e.ResolvedVersion,
			Digest:          e.Digest,
			ManifestDigest:  e.ManifestDigest,
			ReferencedFrom:  append([]string(nil), e.ReferencedFrom...),
		})
	}
	return lf
}
