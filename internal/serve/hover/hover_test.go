// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// parseEntity parses src into an Entity for renderer tests. Inline
// fixtures (rather than testdata files) keep per-kind tests
// self-contained and avoid golden-file infrastructure.
func parseEntity(t *testing.T, src string) Entity {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return Entity{Doc: &doc, Content: []byte(src)}
}

// assertContains fails the test when out is missing the supplied
// substring. Used by each per-kind test to pin the load-bearing
// fragments of the rendered markdown without comparing the whole
// output verbatim.
func assertContains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("rendered hover missing %q\n---rendered---\n%s\n---", want, out)
	}
}

func assertNotContains(t *testing.T, out, unwanted string) {
	t.Helper()
	if strings.Contains(out, unwanted) {
		t.Errorf("rendered hover contains unwanted %q\n---rendered---\n%s\n---", unwanted, out)
	}
}

// --- Self-hover per kind ---

func TestHoverComponentRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\n  description: A web server.\nspec:\n  schema:\n    properties:\n      port:\n        type: integer\n"
	out := Component(parseEntity(t, src))
	assertContains(t, out, "**Component** `core/server` `v1.0.0`")
	assertContains(t, out, "A web server.")
	assertContains(t, out, "**Schema** (excerpt)")
	assertContains(t, out, "```yaml")
}

func TestHoverFeatureRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/autoscaler\n  version: 2.0.0\n  description: Autoscale based on CPU.\nspec:\n  schema:\n    properties:\n      minReplicas:\n        type: integer\n"
	out := Feature(parseEntity(t, src))
	assertContains(t, out, "**Feature** `feature/autoscaler` `v2.0.0`")
	assertContains(t, out, "Autoscale based on CPU.")
	assertContains(t, out, "**Schema** (excerpt)")
}

func TestHoverRecipeRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: helm-server\n  version: 1.0.0\nspec:\n  implements: core/server\n  capability: helm\n  pipeline:\n    - id: step1\n      plugin: p1\n      function: f1\n    - id: step2\n      plugin: p2\n      function: f2\n"
	out := Recipe(parseEntity(t, src))
	assertContains(t, out, "**Recipe** `helm-server` `v1.0.0`")
	assertContains(t, out, "**Implements**: `core/server`")
	assertContains(t, out, "**Capability**: `helm`")
	assertContains(t, out, "**Pipeline**: 2 steps")
}

func TestHoverFeatureRecipeRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: autoscaler-fr\n  version: 1.0.0\nspec:\n  implements: feature/autoscaler\n  capability: helm\n  pipeline: []\n"
	out := FeatureRecipe(parseEntity(t, src))
	assertContains(t, out, "**FeatureRecipe** `autoscaler-fr` `v1.0.0`")
	assertContains(t, out, "**Implements**: `feature/autoscaler`")
	assertContains(t, out, "**Capability**: `helm`")
}

func TestHoverSchemaLibraryRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: Common\n  version: 1.0.0\nspec:\n  schemas:\n    A: {}\n    B: {}\n  templates:\n    T1: {}\n"
	out := SchemaLibrary(parseEntity(t, src))
	assertContains(t, out, "**SchemaLibrary** `Common` `v1.0.0`")
	assertContains(t, out, "**Schemas**: 2 entries")
	assertContains(t, out, "**Templates**: 1 entries")
}

func TestHoverMappingLibraryRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: MappingLibrary\nmetadata:\n  name: helm-mappings\n  version: 1.0.0\nspec:\n  mappings:\n    EntryA: 'tpl1'\n    EntryB: 'tpl2'\n    EntryC: 'tpl3'\n"
	out := MappingLibrary(parseEntity(t, src))
	assertContains(t, out, "**MappingLibrary** `helm-mappings` `v1.0.0`")
	assertContains(t, out, "**Mappings**: 3 entries")
}

func TestHoverAggregateRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/api\n    db:\n      type: core/postgres\n"
	out := Aggregate(parseEntity(t, src))
	assertContains(t, out, "**Aggregate** `my-app` `v1.0.0`")
	assertContains(t, out, "**Components**: 2")
	assertContains(t, out, "`api` (`core/api`)")
	assertContains(t, out, "`db` (`core/postgres`)")
}

func TestHoverManifestRendersSelfMetadataName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n        core/server: recipes/helm-server\n"
	out := Manifest(parseEntity(t, src))
	assertContains(t, out, "**Manifest** `prod` `v1.0.0`")
	assertContains(t, out, "**Flat mappings**: 1 entries")
	assertContains(t, out, "`core/server` -> `recipes/helm-server`")
}

// --- Cross-reference / instance / edge cases at renderer level ---

func TestHoverSchemaExcerptTruncatesToTenLines(t *testing.T) {
	t.Parallel()
	// Build a schema with > 10 lines after yaml.Marshal.
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: big/thing\n  version: 1.0.0\nspec:\n  schema:\n    properties:\n      a: {type: string}\n      b: {type: string}\n      c: {type: string}\n      d: {type: string}\n      e: {type: string}\n      f: {type: string}\n      g: {type: string}\n      h: {type: string}\n      i: {type: string}\n      j: {type: string}\n      k: {type: string}\n      l: {type: string}\n"
	out := Component(parseEntity(t, src))
	assertContains(t, out, "# ...")
}

func TestHoverAggregateTruncatesComponentListToThree(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: big-app\n  version: 1.0.0\nspec:\n  components:\n    a: {type: core/a}\n    b: {type: core/b}\n    c: {type: core/c}\n    d: {type: core/d}\n    e: {type: core/e}\n"
	out := Aggregate(parseEntity(t, src))
	assertContains(t, out, "**Components**: 5")
	assertContains(t, out, "`a` (`core/a`)")
	assertContains(t, out, "`b` (`core/b`)")
	assertContains(t, out, "`c` (`core/c`)")
	assertContains(t, out, "...and 2 more")
	assertNotContains(t, out, "`d` (`core/d`)")
	assertNotContains(t, out, "`e` (`core/e`)")
}

func TestHoverManifestTruncatesMappingListToThree(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: big-prod\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n        core/a: recipes/a\n        core/b: recipes/b\n        core/c: recipes/c\n        core/d: recipes/d\n        core/e: recipes/e\n"
	out := Manifest(parseEntity(t, src))
	assertContains(t, out, "**Flat mappings**: 5 entries")
	assertContains(t, out, "`core/a` -> `recipes/a`")
	assertContains(t, out, "`core/b` -> `recipes/b`")
	assertContains(t, out, "`core/c` -> `recipes/c`")
	assertContains(t, out, "...and 2 more")
	assertNotContains(t, out, "`core/d`")
}

func TestHoverComponentInstanceRendersWithType(t *testing.T) {
	t.Parallel()
	// Parse an Aggregate, walk to the spec.components.api node, render the instance.
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    payment-svc:\n      type: core/server\n      description: The payment service.\n      values:\n        port: 8080\n      uses:\n        backend:\n          primary:\n            target: db\n      features:\n        scaling:\n          type: feature/autoscaler\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	// Walk to spec.components.payment-svc value node.
	components := mappingNodeAtPath(&doc, "spec", "components")
	if components == nil {
		t.Fatal("fixture precondition: spec.components missing")
	}
	var instance *yaml.Node
	for i := 0; i+1 < len(components.Content); i += 2 {
		if components.Content[i].Value == "payment-svc" {
			instance = components.Content[i+1]
		}
	}
	if instance == nil {
		t.Fatal("fixture precondition: payment-svc instance missing")
	}
	out := RenderComponentInstance(ComponentInstance{Key: "payment-svc", Instance: instance})
	assertContains(t, out, "**Component instance** `payment-svc`")
	assertContains(t, out, "Type: `core/server`")
	assertContains(t, out, "The payment service.")
	assertContains(t, out, "**Values**: 1 entries")
	assertContains(t, out, "**Uses**: 1 sockets")
	assertContains(t, out, "**Features**: 1 attached")
}

func TestHoverExtendsRendersParentLine(t *testing.T) {
	t.Parallel()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/derived\n  version: 1.0.0\nspec:\n  extends: core/base\n"
	out := Component(parseEntity(t, src))
	assertContains(t, out, "**Extends**: `core/base`")
}
