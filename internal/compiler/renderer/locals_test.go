// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func omLocals(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func omLocalsIface(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestEvaluateRecipeLocals_Basic(t *testing.T) {
	t.Parallel()

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "db"},
		Spec: types.RecipeSpec{
			Locals: omLocals(
				"fqdn", `{{ .context.instance_id }}.svc`,
				"port", `{{ .values.port }}`,
				"tier", `{{ .infra.tier }}`,
			),
		},
	}
	values := omLocalsIface("port", float64(5432))
	infra := omLocalsIface("tier", "gold")

	got, err := EvaluateRecipeLocals(recipe, "db", values, infra)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := got.Get("fqdn"); v != "db.svc" {
		t.Errorf("fqdn: %q", v)
	}
	if v, _ := got.Get("port"); v != "5432" {
		t.Errorf("port: %q", v)
	}
	if v, _ := got.Get("tier"); v != "gold" {
		t.Errorf("tier: %q", v)
	}
}

// A local resolves BEFORE its node's provides — which is what lets a provides
// template read .locals — so the node's own provides are not a pillar a local can
// reach. The reference halts on the root the context does not define rather than
// rendering the value away to empty.
func TestEvaluateRecipeLocals_RejectsProvidesRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "reverse"},
		Spec: types.RecipeSpec{
			Locals: omLocals("fqdn", `{{ .provides.host }}.svc`),
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "db", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("expected a halt on the reverse reference, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".provides.host") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
	// A defensive wrapper must not launder the same reference into an empty value.
	recipe.Spec.Locals = omLocals("fqdn", `{{ default "x" .provides.host }}`)
	if _, err := EvaluateRecipeLocals(recipe, "db", types.OrderedMap[any]{}, types.OrderedMap[any]{}); err == nil {
		t.Error("a wrapped .provides reference must halt too")
	}
}

func TestEvaluateRecipeLocals_NilRecipe(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeLocals(nil, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty map, got %v", got.Keys())
	}
}

func TestEvaluateRecipeLocals_EmptyLocals(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "x"}}
	got, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty map, got %v", got.Keys())
	}
}

func TestEvaluateRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad"},
		Spec: types.RecipeSpec{
			Locals: omLocals("leak", `{{ .context.inject.env.DB_URL }}`),
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".context.inject.env.DB_URL") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
}

func TestEvaluateRecipeLocals_RejectsUsesRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad"},
		Spec: types.RecipeSpec{
			Locals: omLocals("leak", `{{ (index .context.uses "db").provides.host }}`),
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
}

func TestEvaluateRecipeLocals_Deterministic(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "d"},
		Spec: types.RecipeSpec{
			Locals: omLocals("z_key", "zed", "a_key", "alpha", "m_key", "mid"),
		},
	}
	var first types.OrderedMap[string]
	for i := 0; i < 5; i++ {
		got, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		for k, v := range first.All() {
			if gotV, _ := got.Get(k); gotV != v {
				t.Errorf("run %d key %q: got %q, want %q", i, k, gotV, v)
			}
		}
	}
}

// Locals evaluation is invariant to the KEY ORDER of the values and infra
// substrate it is handed, and that invariance is what allows one evaluation to
// serve every consumer: the render path hands over an author-ordered pillar read
// straight from the IR, while the pre-render assertion path historically rebuilt
// the same data alphabetically after a deep template-flattening pass. Both shapes
// have to produce identical locals, or a single evaluation would silently change
// what one of the two consumers reads.
//
// The invariance holds because the evaluator converts every ordered pillar to a
// plain map at the template boundary and Go templates range plain maps in sorted
// key order — so it is a property of the boundary, not a coincidence of the data.
// This pins it at the level an author can observe: a range over .values, a nested
// object, and an object inside a list.
func TestEvaluateRecipeLocals_SubstrateKeyOrderInvariant(t *testing.T) {
	t.Parallel()

	nested := types.OrderedMap[any]{}
	nested.Set("zone", "eu-west-1")
	nested.Set("az", "b")

	authorOrder := types.OrderedMap[any]{}
	authorOrder.Set("zeta", "z-val")
	authorOrder.Set("alpha", float64(1))
	authorOrder.Set("placement", &nested)
	authorOrder.Set("list", []any{&nested, "tail"})

	infra := types.OrderedMap[any]{}
	infra.Set("tier", "gold")
	infra.Set("account", "acct-1")

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "substrate"},
		Spec: types.RecipeSpec{
			Locals: omLocals(
				"ranged", `{{ range $k, $v := .values }}{{ $k }};{{ end }}`,
				"nested", `{{ .values.placement.zone }}-{{ .values.placement.az }}`,
				"in_list", `{{ (index .values.list 0).zone }}`,
				"infra_ranged", `{{ range $k, $v := .infra }}{{ $k }}={{ $v }};{{ end }}`,
			),
		},
	}

	fromAuthorOrder, err := EvaluateRecipeLocals(recipe, "n", authorOrder, infra)
	if err != nil {
		t.Fatalf("author-order substrate: %v", err)
	}

	// The alphabetical, deep-flattened rebuild of the same data — the shape the
	// pre-render assertion path produced.
	rebuilt := types.OrderedMap[any]{}
	flat := authorOrder.TemplateAlphabeticalMapDeep()
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rebuilt.Set(k, flat[k])
	}
	rebuiltInfra := types.OrderedMap[any]{}
	flatInfra := infra.TemplateAlphabeticalMapDeep()
	infraKeys := make([]string, 0, len(flatInfra))
	for k := range flatInfra {
		infraKeys = append(infraKeys, k)
	}
	sort.Strings(infraKeys)
	for _, k := range infraKeys {
		rebuiltInfra.Set(k, flatInfra[k])
	}

	fromRebuilt, err := EvaluateRecipeLocals(recipe, "n", rebuilt, rebuiltInfra)
	if err != nil {
		t.Fatalf("alphabetical substrate: %v", err)
	}

	if strings.Join(fromAuthorOrder.Keys(), ",") != strings.Join(fromRebuilt.Keys(), ",") {
		t.Fatalf("key sets differ: %v vs %v", fromAuthorOrder.Keys(), fromRebuilt.Keys())
	}
	for k, want := range fromAuthorOrder.All() {
		got, _ := fromRebuilt.Get(k)
		if got != want {
			t.Errorf("locals[%q] differs by substrate shape: author-order %q, alphabetical %q", k, want, got)
		}
		t.Logf("locals[%q] = %q (identical on both substrates)", k, want)
	}
	// A range that returned one key would make the comparison vacuous.
	if ranged, _ := fromAuthorOrder.Get("ranged"); strings.Count(ranged, ";") < 4 {
		t.Errorf("the range probe must observe every values key; got %q", ranged)
	}
}

func TestMergeFeatureLocals_FRWinsOnCollision(t *testing.T) {
	t.Parallel()

	base := omLocals("host", "db-base.svc", "port", "5432")
	frOwn := omLocals("host", "db-fr.svc", "tls", "enabled")

	merged := mergeFeatureLocals(base, frOwn)

	// FR wins on collision
	if v, _ := merged.Get("host"); v != "db-fr.svc" {
		t.Errorf("host: got %q, want %q (FR should win on collision)", v, "db-fr.svc")
	}
	if v, _ := merged.Get("port"); v != "5432" {
		t.Errorf("port: %q", v)
	}
	if v, _ := merged.Get("tls"); v != "enabled" {
		t.Errorf("tls: %q", v)
	}
	// Base is untouched
	if v, _ := base.Get("host"); v != "db-base.svc" {
		t.Error("base map was mutated")
	}
}

func TestMergeFeatureLocals_NilBase(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(types.OrderedMap[string]{}, omLocals("k", "v"))
	if v, _ := merged.Get("k"); v != "v" || merged.Len() != 1 {
		t.Errorf("empty base: %v", merged.Keys())
	}
}

func TestMergeFeatureLocals_NilFROwn(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(omLocals("k", "v"), types.OrderedMap[string]{})
	if v, _ := merged.Get("k"); v != "v" || merged.Len() != 1 {
		t.Errorf("empty FR: %v", merged.Keys())
	}
}
