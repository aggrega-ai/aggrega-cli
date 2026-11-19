// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// mapping-consistency reading, and the instance-rooted feature recipe reading.
// Each is driven with a malformed pin, a well-formed pin no version satisfies, and
// a pin that resolves, so the malformed row is attributable to the pin's grammar
// rather than to the pin merely failing to resolve. Conditions are matched through
// their accessors, never their codes.

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
				feat.APIVersion = "aggrega.ai/v1beta1"
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
			for _, want := range []string{tc.wantText, `Manifest mapping "` + tc.key + `" -> "core/helm/postgres"`, types.VersionPinForms} {
				if !strings.Contains(errs[0].Error(), want) {
					t.Errorf("finding %q does not carry %q", errs[0].Error(), want)
				}
			}
		})
	}
}

// TestVerifyManifestImplementsConsistency_FindingsFollowEntryOrder covers a
// Manifest carrying both refusals the mapping-consistency reading reports: a
// well-formed entry that does not match its recipe, and an entry whose key carries
// a malformed pin. The two are reported by two rules, and the findings still
// arrive in the order the Manifest declares its entries, whichever of the two is
// written first — the order an author reads the mapping table in. Each entry
// carries one fault, and a table of the same two types mapped consistently is the
// control.
func TestVerifyManifestImplementsConsistency_FindingsFollowEntryOrder(t *testing.T) {
	t.Parallel()

	c := fakeCatalog(
		fakeRecipe("core/helm/postgres", "core/datastore/postgres:1.0.0"),
		fakeRecipe("core/helm/mysql", "core/datastore/mysql:1.0.0"),
	)
	type entry struct{ key, reference string }
	mismatch := entry{"core/datastore/postgres:9.9.9", "core/helm/postgres"}
	malformed := entry{"core/datastore/mysql:^garbage", "core/helm/mysql"}

	for _, tc := range []struct {
		name    string
		entries []entry
		want    []error
	}{
		{"the mismatch declared first", []entry{mismatch, malformed},
			[]error{ErrManifestImplementsMismatch(), types.ErrMalformedVersionPin()}},
		{"the malformed pin declared first", []entry{malformed, mismatch},
			[]error{types.ErrMalformedVersionPin(), ErrManifestImplementsMismatch()}},
		{"both types mapped consistently", []entry{
			{"core/datastore/postgres:1.0.0", "core/helm/postgres"},
			{"core/datastore/mysql:1.0.0", "core/helm/mysql"},
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tier := &types.MappingTier{}
			for _, e := range tc.entries {
				tier.Type.Set(e.key, e.reference)
			}
			m := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: tier}}}

			errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry)
			if len(errs) != len(tc.want) {
				t.Fatalf("want %d finding(s), got %d: %v", len(tc.want), len(errs), errs)
			}
			for i, want := range tc.want {
				other := types.ErrMalformedVersionPin()
				if want == other {
					other = ErrManifestImplementsMismatch()
				}
				if !errors.Is(errs[i], want) || errors.Is(errs[i], other) {
					t.Errorf("finding %d = %v; want it to match %v and not %v", i, errs[i], want, other)
				}
				entryText := `Manifest mapping "` + tc.entries[i].key + `" -> "` + tc.entries[i].reference + `"`
				if !strings.Contains(errs[i].Error(), entryText) {
					t.Errorf("finding %d = %q; want it to name the entry declared at that position, %s",
						i, errs[i].Error(), entryText)
				}
			}
		})
	}
}

// TestVerifyRecipeResolution_MalformedPin covers the instance-rooted reading of a
// Manifest mapping value, through the real catalog loader, on both the feature and
// the component axis. On the feature axis a value whose pin is malformed is
// reported as the malformation at the feature instance whose resolution missed. On
// the component axis it is reported by nothing here: the rule over the flattened
// composition reports it before this reading runs, so a component row finding
// anything would be the same fault reported twice. A well-formed pin no version
// satisfies keeps the feature arm's no-recipe-resolved condition, and on the
// component arm is left to the rule over the composition for the same reason.
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

	if err := c.VerifyRecipeResolution(manifestFor("probe/present:1.0.0", "probe/cred:1.0.0"), graph); err != nil {
		t.Fatalf("control: pins the catalog satisfies must resolve cleanly; got %v", err)
	}

	// One malformed pin per row, on one axis, with the other axis's pin satisfied,
	// so a row finding nothing is about the axis it names and nothing else.
	for _, tc := range []struct {
		name        string
		compURI     string
		featURI     string
		wantFeature bool
	}{
		{"a feature pin whose caret range does not parse", "probe/present:1.0.0", "probe/cred:^garbage", true},
		{"a feature pin that is not a semantic version", "probe/present:1.0.0", "probe/cred:1.x", true},
		{"a component pin whose caret range does not parse", "probe/present:^garbage", "probe/cred:1.0.0", false},
		{"a component pin that is not a semantic version", "probe/present:1.x", "probe/cred:1.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := c.ResolutionFindings(manifestFor(tc.compURI, tc.featURI), graph)
			if !tc.wantFeature {
				if len(findings) != 0 {
					t.Fatalf("a malformed component pin must be reported by nothing here, "+
						"since the rule over the composition already reports it; got %d: %v", len(findings), findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("want the feature arm's finding alone, got %d: %v", len(findings), findings)
			}
			f := findings[0]
			if !errors.Is(f, types.ErrMalformedVersionPin()) {
				t.Errorf("finding = %v; want ErrMalformedVersionPin", f)
			}
			if errors.Is(f, ErrNoRecipeResolved()) {
				t.Errorf("finding = %v; a malformed pin must not report as no-recipe-resolved", f)
			}
			for _, want := range []string{`feature "db-cred"`, `reference "` + tc.featURI + `"`, types.VersionPinForms} {
				if !strings.Contains(f.Error(), want) {
					t.Errorf("finding = %q; want it to carry %q", f.Error(), want)
				}
			}
		})
	}

	t.Run("well-formed pins no version satisfies", func(t *testing.T) {
		t.Parallel()
		findings := c.ResolutionFindings(manifestFor("probe/present:9.9.9", "probe/cred:9.9.9"), graph)
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
