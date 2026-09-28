// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules about whether a component instance
// resolves to a Recipe. They compile the fixture workspace for real, varying only
// the Manifest's mapping table, so what halts is a property of the compiled
// documents rather than of an assertion.
//
// The fixture is what makes the coverage question answerable at all. Its
// Aggregate declares two components of its own AND imports a module that
// declares a third, so the composition holds three nodes of two provenances: a
// component the author wrote in the document under compilation, and a component
// that exists only once the module's own document has been read. A rule asked
// before the composition is flattened is never invoked for the second, and
// reports exactly what a rule that ran and approved reports — so a proof that
// exercises only the first would pass against the placement this rule exists to
// avoid. Each class therefore gets its own halt below, named.

// The three component types the fixture's composition carries, and the node each
// is carried by. The module-contributed node is the one a rule asked earlier
// cannot see.
const (
	fixturePlainNodeType        = "core/compute/api-server"
	fixturePlainNodeID          = "backend"
	fixtureSecondPlainNodeType  = "core/compute/web-ui"
	fixtureModuleNodeType       = "core/datastore/postgres"
	fixtureModuleNodeID         = "data.main-db"
	fixtureAPIServerRecipe      = "helm-api-server-synthesized-with-artifacts:1.0.0"
	fixtureWebUIRecipe          = "helm-web-ui-synthesized-with-artifacts:1.0.0"
	fixturePostgresRecipe       = "helm-postgres-synthesized-with-artifacts:1.0.0"
	fixtureFeatureRecipeMapping = "    features:\n      type:\n        core/k8s-dns-resolver: k8s-dns-patcher:1.0.0\n"
)

// manifestMapping composes a Manifest whose components tier carries exactly the
// entries supplied, with the features tier the fixture needs left intact.
//
// The entries are supplied rather than patched out of the committed Manifest,
// because every proof here turns on WHICH entries are present and a patch would
// leave that to be read out of a diff.
func manifestMapping(entries ...string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: prod-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
`)
	for _, entry := range entries {
		b.WriteString("        " + entry + "\n")
	}
	b.WriteString(fixtureFeatureRecipeMapping)
	return b.String()
}

// mapEvery is the mapping table under which every node of the fixture resolves.
// Each proof below removes or doubles exactly one of its entries, so the halt it
// asserts is caused by that edit and by nothing else.
func mapEvery() []string {
	return []string{
		fixturePlainNodeType + ": " + fixtureAPIServerRecipe,
		fixtureSecondPlainNodeType + ": " + fixtureWebUIRecipe,
		fixtureModuleNodeType + ": " + fixturePostgresRecipe,
	}
}

// without returns the mapping table with the entry for one component type
// dropped.
func without(componentType string) []string {
	var out []string
	for _, entry := range mapEvery() {
		if strings.HasPrefix(entry, componentType+":") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// recipeResolutionInputs loads the fixture workspace with the supplied Aggregate
// and Manifest text and hands back everything a pipeline entry takes.
//
// It exists for the reason its siblings in this package do: the halt is the thing
// under proof, so a helper contracting on "a compilation that reached its
// outputs" is exactly what a proof here must not use.
func recipeResolutionInputs(t *testing.T, aggregate, manifest string) (Inputs, *catalog.Catalog) {
	t.Helper()
	rel, err := resolver.LoadRelease(invocationReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	man, err := resolver.LoadManifestFromBytes([]byte(manifest), invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", invocationCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	return Inputs{
		Release:           rel,
		Manifest:          man,
		AggregateRawBytes: []byte(aggregate),
		AggregatePath:     invocationAggregatePath,
		ReleasePath:       invocationReleasePath,
		WorkspaceConfig:   defaultWorkspaceConfigForTest(),
		PluginPins:        pluginpath.NoPins(),
		CompilerVersion:   invocationVersion,
	}, cat
}

// compileWithManifest drives the fixture through the entry a surface that EMITS
// takes, and hands the caller the error rather than failing on it.
//
// Both rules under proof refuse a run that emits and are carried by a run that
// only describes, so which entry a proof drives is half of what it asserts. This
// one declares that its run emits, the way every artifact-producing surface does,
// and then stops short of the render: what the gate does with the findings is
// settled by that declaration rather than by whether the render machinery
// afterwards runs, and rendering here would load guests to prove nothing this
// file is about. The emitting run in full — a build writing real files — is
// driven through the binary itself.
func compileWithManifest(t *testing.T, aggregate, manifest string) (*Result, error) {
	t.Helper()
	inputs, cat := recipeResolutionInputs(t, aggregate, manifest)
	outcome, err := RunPipeline(
		context.Background(),
		inputs,
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(inputs.Manifest),
		cat.SubAggregateLoader(),
		Options{
			SkipRender:               true,
			RecipeResolutionVerifier: cat.VerifyRecipeResolution,
		},
	)
	return outcome.Completed(), err
}

// describeWithManifest drives the same fixture through the entry a surface that
// only DESCRIBES the composition takes, and hands back what that entry reported.
//
// It is the other half of the proof below. A rule at this severity is not one
// that refuses less loudly — it is one whose consequence depends on what the run
// is for — so a file driving only the refusing side would pass against a rule
// that refused everywhere, which is the outcome this severity exists to avoid.
func describeWithManifest(t *testing.T, aggregate, manifest string) (*Tier4Result, error) {
	t.Helper()
	inputs, cat := recipeResolutionInputs(t, aggregate, manifest)
	return RunTier4Pipeline(
		context.Background(),
		inputs,
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(inputs.Manifest).Recipe,
		cat.SubAggregateLoader(),
	)
}

// fixtureAggregate is the committed Aggregate document, read rather than
// restated so a proof cannot drift from the composition the rest of this package
// compiles.
func fixtureAggregate(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	return string(data)
}

// violationsCarrying returns the findings of one condition off a halt, and fails
// when the halt carries no structured findings at all.
func violationsCarrying(t *testing.T, err error, condition error) []*resolver.ResolverError {
	t.Helper()
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T: %v", err, err)
	}
	var out []*resolver.ResolverError
	for _, violation := range multi.Violations {
		if errors.Is(violation.Err, condition) {
			out = append(out, violation)
		}
	}
	return out
}

// TestUnmappedComponentHaltsForEachNodeClassTheCompositionCarries is the coverage
// proof, and the coverage is the point rather than the halt.
//
// One subtest per class of node the composition carries, each removing the
// mapping for that class's own component type and leaving every other entry in
// place. The module-contributed case is the one that cannot be proved anywhere
// earlier: the component it names exists only once the module's own document has
// been read, so a rule asked over the Aggregate's own instance list is never
// invoked for it and reports success while never looking.
func TestUnmappedComponentHaltsForEachNodeClassTheCompositionCarries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		componentType string
		wantNode      string
		wantLocator   string
	}{
		{
			name:          "a component the Aggregate itself declares",
			componentType: fixturePlainNodeType,
			wantNode:      fixturePlainNodeID,
			wantLocator:   ".spec.components.backend",
		},
		{
			name:          "a component a sub-aggregate contributes",
			componentType: fixtureModuleNodeType,
			wantNode:      fixtureModuleNodeID,
			wantLocator:   ".spec.components.main-db",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := compileWithManifest(t,
				fixtureAggregate(t), manifestMapping(without(tc.componentType)...))
			if err == nil {
				t.Fatalf("a composition holding an unmapped %s compiled without halting", tc.componentType)
			}
			if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
				t.Fatalf("the halt does not carry the no-recipe-resolved condition: %v", err)
			}

			reported := violationsCarrying(t, err, catalog.ErrNoRecipeResolved())
			if len(reported) != 1 {
				t.Fatalf("findings carrying this condition = %d, want 1: %+v", len(reported), reported)
			}
			if short, ok := cerrors.ShortCodeOf(reported[0].Err); !ok || short != "E0099" {
				t.Errorf("short code = %q (present %t), want %q", short, ok, "E0099")
			}
			if reported[0].Field != tc.wantLocator {
				t.Errorf("locator = %q, want %q", reported[0].Field, tc.wantLocator)
			}

			rendered := reported[0].Error()
			for _, want := range []string{
				// The identity the composition gave the node, which for a
				// module-contributed component carries the module's prefix and is
				// the whole evidence that such a node was reached at all.
				`"` + tc.wantNode + `"`,
				`of type "` + tc.componentType + `"`,
				"recipes implementing it",
				"pin one of them in the Manifest",
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("the rendered finding does not carry %q: %q", want, rendered)
				}
			}
			t.Logf("rendered finding: %s", rendered)

			// The remedy is an entry under the Manifest's own mapping table, so the
			// category has to send the reader to the operator's document.
			if got := reported[0].ToCompilerError().Category(); got != cerrors.OpsError {
				t.Errorf("category = %v, want %v", got, cerrors.OpsError)
			}
		})
	}
}

// TestEveryNodeMappedCompiles is the control for the proof above. Without it that
// proof would pass on a rule that raised for every composition, including the one
// the committed mapping table resolves completely.
func TestEveryNodeMappedCompiles(t *testing.T) {
	t.Parallel()

	out, err := compileWithManifest(t, fixtureAggregate(t), manifestMapping(mapEvery()...))
	if err != nil {
		t.Fatalf("a composition whose every node is mapped halted: %v", err)
	}
	if out == nil {
		t.Fatal("the run reported no failure and produced no result")
	}
}

// TestAnUnmappedComponentIsCarriedByARunThatOnlyDescribes is the proof of the
// severity itself, and it is what the proof above cannot be.
//
// The same fixture, carrying the same fault, through the entry that produces a
// picture of the composition and emits nothing. It must NOT refuse — a reader
// looking at an incomplete mapping is exactly the reader who needs the picture —
// and it must report the finding all the same, positioned on the instance it is
// about. Asserting only the first half would pass against a rule nobody reports,
// and asserting only the second would pass against a rule that refuses here too.
func TestAnUnmappedComponentIsCarriedByARunThatOnlyDescribes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		componentType string
		wantNode      string
	}{
		{
			name:          "a component the Aggregate itself declares",
			componentType: fixturePlainNodeType,
			wantNode:      fixturePlainNodeID,
		},
		{
			name:          "a component a sub-aggregate contributes",
			componentType: fixtureModuleNodeType,
			wantNode:      fixtureModuleNodeID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			described, err := describeWithManifest(t,
				fixtureAggregate(t), manifestMapping(without(tc.componentType)...))
			if err != nil {
				t.Fatalf("a run that only describes the composition refused it: %v", err)
			}
			if described == nil || described.IRGraph == nil {
				t.Fatal("the run returned no graph, which is the whole thing this posture exists to return")
			}

			var reported []*resolver.ResolverError
			for _, diagnostic := range described.ResolveDiagnostics {
				if errors.Is(diagnostic.Err, catalog.ErrNoRecipeResolved()) {
					reported = append(reported, diagnostic)
				}
			}
			if len(reported) != 1 {
				t.Fatalf("diagnostics carrying this condition = %d, want 1: %+v", len(reported), reported)
			}
			if !strings.Contains(reported[0].Error(), `"`+tc.wantNode+`"`) {
				t.Errorf("the reported diagnostic does not name the instance %q: %q", tc.wantNode, reported[0].Error())
			}
			// The same category the refusing side carries. A finding that changed
			// domain with the posture would send two readers of one fault to two
			// different files.
			if got := reported[0].ToCompilerError().Category(); got != cerrors.OpsError {
				t.Errorf("category = %v, want %v", got, cerrors.OpsError)
			}
		})
	}

	t.Run("a composition whose every node is mapped is described with no such finding", func(t *testing.T) {
		t.Parallel()

		described, err := describeWithManifest(t, fixtureAggregate(t), manifestMapping(mapEvery()...))
		if err != nil {
			t.Fatalf("a composition whose every node is mapped was refused: %v", err)
		}
		for _, diagnostic := range described.ResolveDiagnostics {
			if errors.Is(diagnostic.Err, catalog.ErrNoRecipeResolved()) {
				t.Errorf("a fully mapped composition reported %v", diagnostic)
			}
		}
	})
}

// TestMappedToAnAbsentRecipeHaltsNamingTheMappingValue covers the second arm of
// the same condition: the mapping reached the node and named something the
// catalog does not hold. It is a different remedy from the arm above — correct
// the value, rather than write an entry — so the diagnostic has to name the value
// the author wrote.
func TestMappedToAnAbsentRecipeHaltsNamingTheMappingValue(t *testing.T) {
	t.Parallel()

	const absent = "helm-api-server-absent-from-catalog:1.0.0"
	entries := append(without(fixturePlainNodeType), fixturePlainNodeType+": "+absent)

	_, err := compileWithManifest(t, fixtureAggregate(t), manifestMapping(entries...))
	if err == nil {
		t.Fatal("a mapping naming a recipe the catalog does not hold compiled without halting")
	}
	if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
		t.Fatalf("the halt does not carry the no-recipe-resolved condition: %v", err)
	}
	reported := violationsCarrying(t, err, catalog.ErrNoRecipeResolved())
	if len(reported) != 1 {
		t.Fatalf("findings carrying this condition = %d, want 1: %+v", len(reported), reported)
	}
	rendered := reported[0].Error()
	for _, want := range []string{
		`"` + fixturePlainNodeID + `"`,
		`mapped to recipe "` + absent + `"`,
		"or publish the recipe it names",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered finding does not carry %q: %q", want, rendered)
		}
	}
	// The pin-candidate arm's remedy must not appear here: a mapped node has had
	// its choice made, and offering alternatives would name a decision the author
	// already took.
	if strings.Contains(rendered, "recipes implementing it") {
		t.Errorf("a mapped-but-absent finding must offer no pin candidates: %q", rendered)
	}
}

// TestMappedToAMalformedPinHaltsAsTheMalformation covers the one mapping value the
// rule above passes over: a value whose version pin is none of the forms a pin
// takes. The pin names no version, so "not in the catalog" would send the author
// looking for a recipe when the fault is the pin's text; the rule passes the node
// over and the instance reading reports the malformation instead.
//
// A well-formed pin no catalog version satisfies is the row beside it, and it keeps
// the condition the arm above reports — which is what makes the malformed row a
// proof about the pin's grammar rather than about the pin being unsatisfiable.
func TestMappedToAMalformedPinHaltsAsTheMalformation(t *testing.T) {
	t.Parallel()

	const recipeBase = "helm-api-server-synthesized-with-artifacts"
	for _, tc := range []struct {
		name          string
		value         string
		wantMalformed bool
	}{
		{"a caret pin whose range does not parse", recipeBase + ":^garbage", true},
		{"an exact pin that is not a semantic version", recipeBase + ":1.x", true},
		{"a well-formed pin no version satisfies", recipeBase + ":9.9.9", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries := append(without(fixturePlainNodeType), fixturePlainNodeType+": "+tc.value)
			_, err := compileWithManifest(t, fixtureAggregate(t), manifestMapping(entries...))
			if err == nil {
				t.Fatalf("a mapping to %q compiled without halting", tc.value)
			}
			if !tc.wantMalformed {
				if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
					t.Fatalf("a well-formed unsatisfiable pin must keep the no-recipe-resolved condition: %v", err)
				}
				if errors.Is(err, types.ErrMalformedVersionPin()) {
					t.Fatalf("a well-formed pin was reported as malformed: %v", err)
				}
				return
			}
			if !errors.Is(err, types.ErrMalformedVersionPin()) {
				t.Fatalf("the halt does not carry the malformed-version-pin condition: %v", err)
			}
			if errors.Is(err, catalog.ErrNoRecipeResolved()) {
				t.Errorf("a malformed pin must not also report as no-recipe-resolved: %v", err)
			}
			// The halt's own frame names the stage and nothing else; the finding
			// that names the reference, the instance and the three forms is the
			// cause beneath it. So the text is read with every cause, and each
			// string below can come only from that finding, never from the frame.
			rendered := cerrors.CauseChain(err)
			for _, want := range []string{
				`reference "` + tc.value + `"`,
				`"` + fixturePlainNodeID + `"`,
				types.VersionPinForms,
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("the halt, with its causes, does not carry %q: %q", want, rendered)
				}
			}
		})
	}
}

// aggregateWithPinnedBackendType is the committed Aggregate with the one
// component type this file doubles a mapping key for written at an exact version.
//
// It differs from the committed document in that one type reference and in
// nothing else, which is what makes the pair of proofs below a test of the
// INSTANCE's pinning rather than of the mapping table.
const aggregateWithPinnedBackendType = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: fullstack-app
  version: "1.0.0"
spec:
  aggregates:
    data:
      type: db-module
  components:
    backend:
      type: core/compute/api-server:1.0.0
      uses:
        databases:
          - data.main-db
    frontend:
      type: core/compute/web-ui
      uses:
        apis:
          my-api:
            target: backend
            inject:
              env:
                - in: API_HOST
                  from: host
                - in: API_PORT
                  from: service_port
`

// TestTwoVersionDistinctKeysRefuseAnUnpinnedInstanceAndResolveAPinnedOne pins
// both directions of the rule about an ambiguous mapping, which is the specific
// way it can be got wrong.
//
// A mapping table holding one component type at two distinct versions is LEGAL
// AUTHORING, and a rule that refused the table itself would reject documents that
// build today. What the lookup cannot do is choose between those keys for an
// instance whose authored type pins no version — an exact key match wins before
// any base match is considered, so the same table resolves a pinned instance and
// refuses an unpinned one. Both halves are asserted together because either alone
// would pass while the discrimination was broken.
func TestTwoVersionDistinctKeysRefuseAnUnpinnedInstanceAndResolveAPinnedOne(t *testing.T) {
	t.Parallel()

	doubled := append(without(fixturePlainNodeType),
		fixturePlainNodeType+":1.0.0: "+fixtureAPIServerRecipe,
		fixturePlainNodeType+":2.0.0: "+fixtureAPIServerRecipe,
	)

	t.Run("an instance whose authored type pins no version is refused", func(t *testing.T) {
		t.Parallel()

		_, err := compileWithManifest(t, fixtureAggregate(t), manifestMapping(doubled...))
		if err == nil {
			t.Fatal("an unpinned instance matching two version-distinct keys compiled without halting")
		}
		if !errors.Is(err, catalog.ErrAmbiguousTypeMapping()) {
			t.Fatalf("the halt does not carry the ambiguous-type-mapping condition: %v", err)
		}
		reported := violationsCarrying(t, err, catalog.ErrAmbiguousTypeMapping())
		if len(reported) != 1 {
			t.Fatalf("findings carrying this condition = %d, want 1: %+v", len(reported), reported)
		}
		if short, ok := cerrors.ShortCodeOf(reported[0].Err); !ok || short != "E0093" {
			t.Errorf("short code = %q (present %t), want %q", short, ok, "E0093")
		}
		rendered := reported[0].Error()
		for _, want := range []string{
			`"` + fixturePlainNodeType + `"`,
			`declared by component "` + fixturePlainNodeID + `"`,
			fixturePlainNodeType + ":1.0.0",
			fixturePlainNodeType + ":2.0.0",
			"pin the authored type's version",
		} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the rendered finding does not carry %q: %q", want, rendered)
			}
		}
		// The refusal is not an absent mapping, and reporting it as one would tell
		// the author to write an entry the table already carries twice.
		if errors.Is(err, catalog.ErrNoRecipeResolved()) {
			t.Errorf("a refused lookup must not also report as no-recipe-resolved: %v", err)
		}
	})

	t.Run("an instance whose authored type pins a version resolves", func(t *testing.T) {
		t.Parallel()

		out, err := compileWithManifest(t, aggregateWithPinnedBackendType, manifestMapping(doubled...))
		if err != nil {
			t.Fatalf("an exactly-pinned instance must resolve against the key it names; got %v", err)
		}
		if out == nil {
			t.Fatal("the run reported no failure and produced no result")
		}
	})
}
