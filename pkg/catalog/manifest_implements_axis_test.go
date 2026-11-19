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

// TestVerifyManifestImplementsConsistency_JudgesTheTypeAxisOnly pins the bound
// that no other test reaches: the reading is over the type axis, and an entry on
// the name axis is passed over however badly its value would fare under the same
// comparison.
//
// The bound is real rather than incidental. A name-axis entry keys an instance
// NAME, and a recipe's implements declares a TYPE, so a comparison there would
// refuse every instance whose name is not spelled like a type — which is every one
// of them. Widening onto that axis would start halting builds the product accepts
// today.
//
// Both halves are asserted, and the positive control is what makes the negative
// mean anything: the SAME key against the SAME recipe is reported when it is
// written on the type axis, so the silence on the name axis is the axis filter and
// not a fixture that offends nothing.
func TestVerifyManifestImplementsConsistency_JudgesTheTypeAxisOnly(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(fakeRecipe("core/helm/wrong-recipe", "core/datastore/mysql"))

	onName := &types.MappingTier{}
	onName.Name.Set("core/datastore/postgres", "core/helm/wrong-recipe")
	nameManifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: onName}},
	}
	if errs := c.VerifyManifestImplementsConsistency(nameManifest, "manifest.yaml", c.ComponentAncestry); len(errs) != 0 {
		t.Errorf("a name-axis entry is not judged by this reading; got %d finding(s): %v", len(errs), errs)
	}

	onType := &types.MappingTier{}
	onType.Type.Set("core/datastore/postgres", "core/helm/wrong-recipe")
	typeManifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: onType}},
	}
	errs := c.VerifyManifestImplementsConsistency(typeManifest, "manifest.yaml", c.ComponentAncestry)
	if len(errs) != 1 {
		t.Fatalf("the control must offend: the same key and recipe on the type axis; got %d finding(s): %v",
			len(errs), errs)
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("the control's finding must carry the mapping-mismatch condition; got %v", errs[0])
	}
}

// TestVerifyManifestImplementsConsistency_FeatureArmNamesNoExtendsAlternative
// pins the wording the published reference reproduces for the feature arm: it
// names the feature mapping, the featurerecipe's declaration, and the expected
// key — and offers no "or a type it extends" alternative.
//
// The absence is the assertion worth having. A Feature's extends composes its
// pillars rather than inheriting them, so a FeatureRecipe written against a base
// is not known to serve an extending Feature; offering the base as a remedy would
// send the author to an edit that does not resolve the fault.
func TestVerifyManifestImplementsConsistency_FeatureArmNamesNoExtendsAlternative(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Recipes:        map[string]types.Recipe{},
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	fr := types.FeatureRecipe{}
	fr.Metadata.Name = "myco/fr-mismatch"
	fr.Spec.Implements = "feature/different"
	c.FeatureRecipes[recipeURIKey(fr.Metadata.Name, fr.Metadata.Version)] = fr

	tier := &types.MappingTier{}
	tier.Type.Set("feature/mismatch", "myco/fr-mismatch")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: tier}},
	}

	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml", c.ComponentAncestry)
	if len(errs) != 1 {
		t.Fatalf("a feature mapping onto a featurerecipe implementing another type must be reported; got %d: %v",
			len(errs), errs)
	}
	got := errs[0].Error()
	for _, want := range []string{
		`Manifest feature mapping "feature/mismatch" -> "myco/fr-mismatch"`,
		`featurerecipe spec.implements is "feature/different"`,
		`expected "feature/mismatch"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the feature arm's message must contain %q; got %q", want, got)
		}
	}
	if strings.Contains(got, "extends") {
		t.Errorf("the feature arm names no extends alternative; got %q", got)
	}
}

// TestVerifyManifestImplementsConsistency_ChainFaultsPrecedeMismatches pins the
// order a Manifest offending in both ways reports in: every entry whose extends
// chain could not be walked comes first, then every mismatch, each group in the
// order the Manifest declares its entries.
//
// The order is a decision rather than an accident, so it is pinned rather than
// left to whatever the implementation happens to do. A chain that did not walk is
// a defect of the catalog's own Component entries, resolved where the walking
// happened; a mismatch is a defect of the mapping the author wrote. The
// resolution reports what it kept, once, and the reading reports what it judged —
// so the two arrive as two groups rather than interleaved by entry.
//
// The fixture authors the mismatch FIRST, so a reading that simply followed
// declaration order would fail this.
func TestVerifyManifestImplementsConsistency_ChainFaultsPrecedeMismatches(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/cyc-a", "1.0.0", "core/cyc-b"),
			ancestryComponent("core/cyc-b", "1.0.0", "core/cyc-a"),
		},
		fakeRecipe("core/helm/unrelated", "core/datastore/mysql"),
	)
	m := ancestryManifest(map[string]string{
		"core/plain-mismatch:1.0.0": "core/helm/unrelated",
		"core/cyc-a:1.0.0":          "core/helm/unrelated",
	}, "core/plain-mismatch:1.0.0", "core/cyc-a:1.0.0")

	errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry)
	if len(errs) != 2 {
		t.Fatalf("both entries offend, each in its own way; got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], validator.ErrCyclicExtension()) {
		t.Errorf("the chain fault is reported first; got %v", errs[0])
	}
	if errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("a chain fault must not be reported as a mapping mismatch: %v", errs[0])
	}
	if !errors.Is(errs[1], ErrManifestImplementsMismatch()) {
		t.Errorf("the mapping mismatch is reported after it; got %v", errs[1])
	}
	if !strings.Contains(errs[0].Error(), `Manifest mapping "core/cyc-a:1.0.0" -> "core/helm/unrelated"`) {
		t.Errorf("the chain fault names the mapping entry that asked for the chain; got %q", errs[0].Error())
	}
}
