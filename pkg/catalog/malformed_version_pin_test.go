// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The proofs in this file cover the catalog's own surfaces that turn a reference
// they could not resolve into a diagnostic: the whole-catalog extends pass, the
// mapping-consistency reading, and the instance-rooted recipe reading. Each is
// driven with a malformed pin, a well-formed pin no version satisfies, and a pin
// that resolves, so the malformed row is attributable to the pin's grammar rather
// than to the pin merely failing to resolve. Conditions are matched through their
// accessors, never their codes.

// TestResolveAllExtends_MalformedParentPin covers both places the whole-catalog
// extends pass reports an extends parent it could not resolve: the version select,
// where the parent's name is held and the pin selects nothing, and the chain walk,
// where the parent's name is held nowhere. A malformed pin is reported as the
// malformation at both; every other refusal keeps its own condition.
func TestResolveAllExtends_MalformedParentPin(t *testing.T) {
	t.Parallel()

	// wantText tells the two reporting sites apart: the whole-catalog pass reports a
	// held parent as "extends parent …" at `.extends`, and the chain walk reports an
	// absent one as "<child> extends …" at `.spec.extends`. Without it the chain walk
	// would answer for a whole-catalog pass that stopped reporting the malformation.
	for _, tc := range []struct {
		name     string
		extends  string
		want     error
		wantText string
	}{
		{"a held parent pinned with a caret whose range does not parse", "core/base:^garbage", types.ErrMalformedVersionPin(), `extends parent reference "core/base:^garbage"`},
		{"a held parent pinned with an exact pin that is not a semantic version", "core/base:1.x", types.ErrMalformedVersionPin(), `extends parent reference "core/base:1.x"`},
		{"a held parent pinned at a version the catalog does not hold", "core/base:9.9.9", ErrUnsatisfiableEntityVersion(), `extends parent "core/base:9.9.9"`},
		{"an absent parent pinned with a caret whose range does not parse", "core/absent:^garbage", types.ErrMalformedVersionPin(), `core/child extends reference "core/absent:^garbage"`},
		{"an absent parent pinned at a well-formed version", "core/absent:1.0.0", validator.ErrInheritanceMerge(), "core/child extends core/absent:1.0.0, which no catalog entity provides"},
		{"a held parent pinned at a version the catalog holds", "core/base:1.0.0", nil, ""},
	} {
		t.Run("component: "+tc.name, func(t *testing.T) {
			t.Parallel()
			c := ancestryCatalog([]types.Component{
				ancestryComponent("core/base", "1.0.0", ""),
				ancestryComponent("core/child", "1.0.0", tc.extends),
			})
			assertExtendsOutcome(t, c.ResolveAllExtends(), tc.want, tc.wantText)
		})
		t.Run("feature: "+tc.name, func(t *testing.T) {
			t.Parallel()
			c := &Catalog{Components: map[string]types.Component{}, Features: map[string]types.Feature{}}
			for _, f := range []struct{ name, extends string }{{"core/base", ""}, {"core/child", tc.extends}} {
				feat := types.Feature{}
				feat.APIVersion = "aggrega.ai/v1alpha1"
				feat.Kind = "Feature"
				feat.Metadata.Name = f.name
				feat.Metadata.Version = "1.0.0"
				feat.Spec.Extends = f.extends
				c.Features[f.name+":1.0.0"] = feat
			}
			assertExtendsOutcome(t, c.ResolveAllExtends(), tc.want, tc.wantText)
		})
	}
}

func assertExtendsOutcome(t *testing.T, errs []*validator.ValidationError, want error, wantText string) {
	t.Helper()
	if want == nil {
		if len(errs) != 0 {
			t.Fatalf("control: a parent the catalog holds must resolve; got %v", errs)
		}
		return
	}
	if len(errs) != 1 {
		t.Fatalf("want one extends failure, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], want) {
		t.Errorf("failure = %v; want it to match %v", errs[0], want)
	}
	malformed := errors.Is(errs[0], types.ErrMalformedVersionPin())
	if malformed != errors.Is(want, types.ErrMalformedVersionPin()) {
		t.Errorf("failure = %v; malformed-pin condition present = %v, want %v", errs[0], malformed, !malformed)
	}
	if malformed && !strings.Contains(errs[0].Error(), types.VersionPinForms) {
		t.Errorf("a malformed-pin failure must carry the three forms; got %q", errs[0].Error())
	}
	if !strings.Contains(errs[0].Error(), wantText) {
		t.Errorf("failure %q does not carry %q, which names the site that reported it", errs[0].Error(), wantText)
	}
}

// TestVerifyManifestImplementsConsistency_MalformedPin covers the reading that
// judges a type-axis mapping against its recipe's spec.implements. An entry whose
// key or implements carries a malformed pin could never have matched, so it is
// reported as that malformation at the Manifest; a well-formed pair that does not
// match keeps the mismatch condition; a matching pair reports nothing.
func TestVerifyManifestImplementsConsistency_MalformedPin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		key        string
		implements string
		want       error
		wantText   string
	}{
		{"a mapping key pinned with a caret whose range does not parse", "core/datastore/postgres:^garbage", "core/datastore/postgres:1.0.0", types.ErrMalformedVersionPin(), "in the mapping key"},
		{"a mapping key pinned with an exact pin that is not a semantic version", "core/datastore/postgres:1.x", "core/datastore/postgres:1.0.0", types.ErrMalformedVersionPin(), "in the mapping key"},
		{"a recipe implements pinned with a caret whose range does not parse", "core/datastore/postgres:1.0.0", "core/datastore/postgres:^garbage", types.ErrMalformedVersionPin(), "in the recipe's spec.implements"},
		{"a well-formed pair that does not match", "core/datastore/postgres:9.9.9", "core/datastore/postgres:1.0.0", ErrManifestImplementsMismatch(), ""},
		{"a matching pair", "core/datastore/postgres:1.0.0", "core/datastore/postgres:^1.0.0", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := fakeCatalog(fakeRecipe("core/helm/postgres", tc.implements))
			tier := &types.MappingTier{}
			tier.Type.Set(tc.key, "core/helm/postgres")
			m := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: tier}}}

			errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry)
			if tc.want == nil {
				if len(errs) != 0 {
					t.Fatalf("control: a consistent mapping must report nothing; got %v", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("want one finding, got %d: %v", len(errs), errs)
			}
			if !errors.Is(errs[0], tc.want) {
				t.Errorf("finding = %v; want it to match %v", errs[0], tc.want)
			}
			if tc.wantText == "" {
				if errors.Is(errs[0], types.ErrMalformedVersionPin()) {
					t.Errorf("a well-formed pair was reported as a malformed pin: %v", errs[0])
				}
				return
			}
			if errors.Is(errs[0], ErrManifestImplementsMismatch()) {
				t.Errorf("a malformed pin must not also report as a mismatch: %v", errs[0])
			}
			for _, want := range []string{tc.wantText, `manifest mapping "` + tc.key + `" -> "core/helm/postgres"`, types.VersionPinForms} {
				if !strings.Contains(errs[0].Error(), want) {
					t.Errorf("finding %q does not carry %q", errs[0].Error(), want)
				}
			}
		})
	}
}

// TestVerifyRecipeResolution_MalformedPin covers the instance-rooted reading of a
// Manifest mapping value, through the real catalog loader, on both attachment
// axes. A value whose pin is malformed is reported as the malformation at the
// instance whose resolution missed; a well-formed pin no version satisfies keeps
// the feature arm's no-recipe-resolved condition, and on the component arm is left
// to the rule over the composition, which reports it before this reading runs.
func TestVerifyRecipeResolution_MalformedPin(t *testing.T) {
	t.Parallel()
	const compType = "app/db"
	const featType = "sec/db-cred"
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "recipe/present.yaml", recipeYAML("present", compType))
	writeCatalogEntity(t, dir, "feature-recipe/cred.yaml", featureRecipeYAML("cred", featType))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("db", types.IRNode{Component: compType})
	feats := types.OrderedMap[types.IRFeatureEntry]{}
	feats.Set("db-cred", types.IRFeatureEntry{Type: featType})
	graph.Spec.FeatureValuesMapping.Set("db", feats)

	manifestFor := func(compURI, featURI string) *types.Manifest {
		comp := &types.MappingTier{}
		comp.Type.Set(compType, compURI)
		feat := &types.MappingTier{}
		feat.Type.Set(featType, featURI)
		return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
			Components: comp,
			Features:   feat,
		}}}
	}

	if err := c.VerifyRecipeResolution(manifestFor("eval/present:1.0.0", "eval/cred:1.0.0"), graph); err != nil {
		t.Fatalf("control: pins the catalog satisfies must resolve cleanly; got %v", err)
	}

	for _, tc := range []struct {
		name     string
		compURI  string
		featURI  string
		wantInst []string
	}{
		{"caret pins whose range does not parse", "eval/present:^garbage", "eval/cred:^garbage", []string{`component "db"`, `feature "db-cred"`}},
		{"exact pins that are not semantic versions", "eval/present:1.x", "eval/cred:1.x", []string{`component "db"`, `feature "db-cred"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := c.ResolutionFindings(manifestFor(tc.compURI, tc.featURI), graph)
			if len(findings) != 2 {
				t.Fatalf("want one finding per axis, got %d: %v", len(findings), findings)
			}
			for i, f := range findings {
				if !errors.Is(f, types.ErrMalformedVersionPin()) {
					t.Errorf("finding %d = %v; want ErrMalformedVersionPin", i, f)
				}
				if errors.Is(f, ErrNoRecipeResolved()) {
					t.Errorf("finding %d = %v; a malformed pin must not report as no-recipe-resolved", i, f)
				}
				if !strings.Contains(f.Error(), tc.wantInst[i]) {
					t.Errorf("finding %d = %q; want it rooted at %s", i, f.Error(), tc.wantInst[i])
				}
			}
		})
	}

	t.Run("well-formed pins no version satisfies", func(t *testing.T) {
		t.Parallel()
		findings := c.ResolutionFindings(manifestFor("eval/present:9.9.9", "eval/cred:9.9.9"), graph)
		if len(findings) != 1 {
			t.Fatalf("want the feature arm's finding alone, got %d: %v", len(findings), findings)
		}
		if !errors.Is(findings[0], ErrNoRecipeResolved()) {
			t.Errorf("finding = %v; want ErrNoRecipeResolved", findings[0])
		}
		if errors.Is(findings[0], types.ErrMalformedVersionPin()) {
			t.Errorf("a well-formed pin was reported as malformed: %v", findings[0])
		}
	})
}
