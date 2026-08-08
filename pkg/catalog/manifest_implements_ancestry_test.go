// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ancestryComponent builds a catalog Component with an optional extends pointer.
func ancestryComponent(name, version, extends string) types.Component {
	return types.Component{
		Metadata: types.ComponentMetadata{Name: name, Version: version},
		Spec:     types.ComponentSpec{Extends: extends},
	}
}

// ancestryCatalog is fakeCatalog plus a populated Component index, keyed the way
// the loader keys it (identity), so resolveComponentKey's version-tolerant
// lookup resolves both bare and version-pinned references.
func ancestryCatalog(comps []types.Component, recipes ...types.Recipe) *Catalog {
	c := fakeCatalog(recipes...)
	for _, comp := range comps {
		c.Components[comp.Metadata.Name+":"+comp.Metadata.Version] = comp
	}
	return c
}

func ancestryManifest(mappings map[string]string, order ...string) *types.Manifest {
	tier := &types.MappingTier{}
	for _, k := range order {
		tier.Type.Set(k, mappings[k])
	}
	return &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: tier}},
	}
}

// TestVerifyManifestImplementsConsistency_SatisfiedThroughParent is the core of
// the reduced extends contract: a wrapper Component that extends a base is served
// by the BASE's Recipe, so a Manifest mapping the wrapper's own type onto that
// Recipe is consistent and no new Recipe has to be authored.
func TestVerifyManifestImplementsConsistency_SatisfiedThroughParent(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/compute/server", "1.0.0", ""),
			ancestryComponent("enterprise/labelled-server", "1.0.0", "core/compute/server"),
		},
		fakeRecipe("core/helm/server", "core/compute/server"),
	)
	m := ancestryManifest(map[string]string{
		"enterprise/labelled-server:1.0.0": "core/helm/server",
	}, "enterprise/labelled-server:1.0.0")

	if errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry); len(errs) != 0 {
		t.Errorf("a Recipe implementing the base must satisfy the extending child; got %v", errs)
	}
}

// TestVerifyManifestImplementsConsistency_SatisfiedThroughGrandparent pins that
// satisfaction reaches the whole chain, not one level: the mapped type's
// grandparent is what the Recipe implements.
func TestVerifyManifestImplementsConsistency_SatisfiedThroughGrandparent(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/base", "1.0.0", ""),
			ancestryComponent("core/mid", "1.0.0", "core/base"),
			ancestryComponent("enterprise/leaf", "1.0.0", "core/mid"),
		},
		fakeRecipe("core/helm/base", "core/base"),
	)
	m := ancestryManifest(map[string]string{
		"enterprise/leaf:1.0.0": "core/helm/base",
	}, "enterprise/leaf:1.0.0")

	if errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry); len(errs) != 0 {
		t.Errorf("satisfaction must reach a grandparent; got %v", errs)
	}
}

// TestVerifyManifestImplementsConsistency_UnrelatedStillHalts pins that the
// change ADDS a satisfaction path and removes none: a mapping onto a Recipe
// implementing a type nowhere in the mapped type's chain still fails with the
// same sentinel.
func TestVerifyManifestImplementsConsistency_UnrelatedStillHalts(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/compute/server", "1.0.0", ""),
			ancestryComponent("enterprise/labelled-server", "1.0.0", "core/compute/server"),
			ancestryComponent("core/datastore/mysql", "1.0.0", ""),
		},
		fakeRecipe("core/helm/mysql", "core/datastore/mysql"),
	)
	m := ancestryManifest(map[string]string{
		"enterprise/labelled-server:1.0.0": "core/helm/mysql",
	}, "enterprise/labelled-server:1.0.0")

	errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("an unrelated implements must still halt")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("expected ErrManifestImplementsMismatch; got %v", errs[0])
	}
}

// TestVerifyManifestImplementsConsistency_AncestryHonoursCaret pins that
// ancestry does not smuggle the version dimension past the matcher: each base is
// offered in its own version-pinned form, so a Recipe pinned to a range the base
// falls outside of does NOT satisfy the child.
func TestVerifyManifestImplementsConsistency_AncestryHonoursCaret(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/api", "1.0.0", ""),
			ancestryComponent("enterprise/api-wrapper", "1.0.0", "core/api"),
		},
		fakeRecipe("core/helm/api-v2", "core/api:^2.0.0"),
		fakeRecipe("core/helm/api-v1", "core/api:^1.0.0"),
	)

	outOfRange := ancestryManifest(map[string]string{
		"enterprise/api-wrapper:1.0.0": "core/helm/api-v2",
	}, "enterprise/api-wrapper:1.0.0")
	errs := c.VerifyManifestImplementsConsistency(outOfRange, "manifest.yaml", c.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("a base outside the recipe's caret range must not satisfy the child")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("expected ErrManifestImplementsMismatch; got %v", errs[0])
	}

	inRange := ancestryManifest(map[string]string{
		"enterprise/api-wrapper:1.0.0": "core/helm/api-v1",
	}, "enterprise/api-wrapper:1.0.0")
	if errs := c.VerifyManifestImplementsConsistency(inRange, "manifest.yaml", c.ComponentAncestry); len(errs) != 0 {
		t.Errorf("a base inside the recipe's caret range must satisfy the child; got %v", errs)
	}
}

// TestVerifyManifestImplementsConsistency_CyclicChainReportsChainFault pins that
// a circular extends chain reached through this gate surfaces as the
// chain-structural fault rather than as a mapping mismatch — the mapping may be
// perfectly well authored, and reporting it as the problem sends the author to
// the wrong file. It must also not hang.
func TestVerifyManifestImplementsConsistency_CyclicChainReportsChainFault(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/cyc-a", "1.0.0", "core/cyc-b"),
			ancestryComponent("core/cyc-b", "1.0.0", "core/cyc-a"),
		},
		fakeRecipe("core/helm/unrelated", "core/datastore/mysql"),
	)
	m := ancestryManifest(map[string]string{
		"core/cyc-a:1.0.0": "core/helm/unrelated",
	}, "core/cyc-a:1.0.0")

	errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", c.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("a cyclic chain must be reported")
	}
	if !errors.Is(errs[0], validator.ErrCyclicExtension()) {
		t.Errorf("expected the chain-structural sentinel ErrCyclicExtension; got %v", errs[0])
	}
	if errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("a chain fault must not be reported as a mapping mismatch: %v", errs[0])
	}
}

// TestVerifyManifestImplementsConsistency_NilResolverResolvesNoAncestry pins the
// nil contract explicitly, so the silent shape is a written expectation rather
// than an accident: a caller that passes no resolver gets the direct comparison
// only.
func TestVerifyManifestImplementsConsistency_NilResolverResolvesNoAncestry(t *testing.T) {
	t.Parallel()
	c := ancestryCatalog(
		[]types.Component{
			ancestryComponent("core/compute/server", "1.0.0", ""),
			ancestryComponent("enterprise/labelled-server", "1.0.0", "core/compute/server"),
		},
		fakeRecipe("core/helm/server", "core/compute/server"),
	)
	m := ancestryManifest(map[string]string{
		"enterprise/labelled-server:1.0.0": "core/helm/server",
	}, "enterprise/labelled-server:1.0.0")

	errs := c.VerifyManifestImplementsConsistency(m, "manifest.yaml", nil)
	if len(errs) == 0 {
		t.Fatal("a nil resolver must resolve no ancestry, so this mapping fails")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("expected ErrManifestImplementsMismatch; got %v", errs[0])
	}
}

const ancestryBaseCompYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/ancestry/base
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      replicas: { type: integer, default: 1 }
`

const ancestryMidCompYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/ancestry/mid
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/ancestry/base
  schema: inherited
`

const ancestryLeafCompYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: enterprise/ancestry/leaf
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
    compliance.acme.io/tier: gold
spec:
  extends: core/ancestry/mid
  schema: inherited
`

const ancestryBaseRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ancestry-base
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/ancestry/base"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: demo:1
`

const ancestryOtherRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ancestry-other
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/ancestry/unrelated"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: demo:1
`

const ancestryCycARecipeless = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/ancestry/cyc-a
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/ancestry/cyc-b
  schema: inherited
`

const ancestryCycBRecipeless = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/ancestry/cyc-b
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: core/ancestry/cyc-a
  schema: inherited
`

// TestLazyLookup_AssertManifestImplementsConsistency_Ancestry is the build path's
// own proof. The build gates through a lazily-enumerated view whose recipe shell
// carries NO Component index, so ancestry there can only come from the view's
// entity cascade. This test therefore fails — silently accepting nothing —
// if the view ever hands the gate the shell's own resolver instead of its own.
//
// It covers all four outcomes on the one path: satisfaction through a
// grandparent, an unrelated implements still halting, a cyclic chain reported as
// the chain fault rather than a mismatch or a hang, and the recipe shell's own
// resolver being demonstrably blind (the trap this wiring exists to avoid).
func TestLazyLookup_AssertManifestImplementsConsistency_Ancestry(t *testing.T) {
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "base.yaml", ancestryBaseCompYAML)
	writeCatalogFile(t, cd, "mid.yaml", ancestryMidCompYAML)
	writeCatalogFile(t, cd, "leaf.yaml", ancestryLeafCompYAML)
	writeCatalogFile(t, cd, "cyc-a.yaml", ancestryCycARecipeless)
	writeCatalogFile(t, cd, "cyc-b.yaml", ancestryCycBRecipeless)
	writeCatalogFile(t, cd, "recipe-base.yaml", ancestryBaseRecipeYAML)
	writeCatalogFile(t, cd, "recipe-other.yaml", ancestryOtherRecipeYAML)

	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)

	// Calibration. A mapping onto a recipe the gate cannot resolve is SKIPPED, not
	// judged, so a passing consistency result over an unresolvable recipe proves
	// nothing. Establish that both recipes resolve before reading any verdict.
	for _, uri := range []string{"compose/ancestry-base:1.0.0", "compose/ancestry-other:1.0.0"} {
		if _, ok := lazy.recipeCatalog().RecipeByURI(uri); !ok {
			t.Fatalf("calibration: recipe %q must resolve, or every verdict below is vacuous", uri)
		}
	}

	satisfied := ancestryManifest(map[string]string{
		"enterprise/ancestry/leaf:1.0.0": "compose/ancestry-base:1.0.0",
	}, "enterprise/ancestry/leaf:1.0.0")
	if err := lazy.AssertManifestImplementsConsistency(satisfied, "manifest.yaml"); err != nil {
		t.Errorf("the build path must satisfy a two-level chain through the base's Recipe: %v", err)
	}

	unrelated := ancestryManifest(map[string]string{
		"enterprise/ancestry/leaf:1.0.0": "compose/ancestry-other:1.0.0",
	}, "enterprise/ancestry/leaf:1.0.0")
	err := lazy.AssertManifestImplementsConsistency(unrelated, "manifest.yaml")
	if err == nil {
		t.Fatal("the build path must still halt on an unrelated implements")
	}
	if !errors.Is(err, ErrManifestImplementsMismatch()) {
		t.Errorf("expected ErrManifestImplementsMismatch; got %v", err)
	}

	cyclic := ancestryManifest(map[string]string{
		"core/ancestry/cyc-a:1.0.0": "compose/ancestry-other:1.0.0",
	}, "core/ancestry/cyc-a:1.0.0")
	cerr := lazy.AssertManifestImplementsConsistency(cyclic, "manifest.yaml")
	if cerr == nil {
		t.Fatal("a cyclic chain must be reported on the build path")
	}
	if !errors.Is(cerr, validator.ErrCyclicExtension()) {
		t.Errorf("expected ErrCyclicExtension on the build path; got %v", cerr)
	}

	// The trap, pinned: the recipe shell the gate runs against holds no Component
	// index, so ITS resolver answers nothing. This is why the view supplies its
	// own; a future edit that swaps them reds this assertion instead of quietly
	// refusing every ancestry-dependent mapping on the build path alone.
	shell := lazy.recipeCatalog()
	if len(shell.Components) != 0 {
		t.Fatalf("the recipe shell is expected to hold no Component index; it holds %d", len(shell.Components))
	}
	if bases, aerr := shell.ComponentAncestry("enterprise/ancestry/leaf:1.0.0"); aerr != nil || len(bases) != 0 {
		t.Errorf("the recipe shell's own resolver must be blind (bases=%v err=%v)", bases, aerr)
	}
	if bases, aerr := lazy.componentAncestry("enterprise/ancestry/leaf:1.0.0"); aerr != nil || len(bases) != 2 {
		t.Errorf("the view's own resolver must see the whole chain (bases=%v err=%v)", bases, aerr)
	}
}
