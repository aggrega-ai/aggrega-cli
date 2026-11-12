// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAnnotationRegistry_KeysReturnsAuthorOrder pins the Keys accessor.
// Author order is the OrderedMap's key insertion order; the registry's
// Keys() forwards through.
func TestAnnotationRegistry_KeysReturnsAuthorOrder(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/first", types.AnnotationBinding{Feature: "core/first"})
	annotations.Set("feature.myco/second", types.AnnotationBinding{Feature: "core/second"})
	m.Spec.Mappings.Features = &types.MappingTier{Annotations: annotations}
	reg := BuildAnnotationRegistry(m)
	keys := reg.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys; got %d (%v)", len(keys), keys)
	}
	if keys[0] != "feature.myco/first" || keys[1] != "feature.myco/second" {
		t.Errorf("author order not preserved; got %v", keys)
	}
}

// TestAnnotationRegistry_KeysOnNilReturnsNil pins the nil-receiver guard.
// A nil registry returns nil rather than panic on the nil dereference;
// the contract supports defensive lookups without prior nil-check.
func TestAnnotationRegistry_KeysOnNilReturnsNil(t *testing.T) {
	t.Parallel()
	var reg *AnnotationRegistry
	if got := reg.Keys(); got != nil {
		t.Errorf("nil registry should return nil; got %v", got)
	}
	if got := reg.Len(); got != 0 {
		t.Errorf("nil registry Len should return 0; got %d", got)
	}
	if _, ok := reg.Lookup("anything"); ok {
		t.Errorf("nil registry Lookup should return ok=false")
	}
}

// TestAnnotationRegistry_LookupReturnsBindingOrFalse pins the two
// branches of Lookup: a present key returns the binding and ok=true;
// an absent key returns the zero AnnotationBinding and ok=false.
func TestAnnotationRegistry_LookupReturnsBindingOrFalse(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/db", types.AnnotationBinding{Feature: "core/db-pool"})
	m.Spec.Mappings.Features = &types.MappingTier{Annotations: annotations}
	reg := BuildAnnotationRegistry(m)
	if got, ok := reg.Lookup("feature.myco/db"); !ok || got.Feature != "core/db-pool" {
		t.Errorf("present key: got (%+v, %v); want (Feature: core/db-pool, true)", got, ok)
	}
	if got, ok := reg.Lookup("feature.myco/missing"); ok || got.Feature != "" {
		t.Errorf("absent key: got (%+v, %v); want (zero, false)", got, ok)
	}
}

// TestResolverError_Error_BothShapes pins the Error() format. With a
// Field, the rendered message includes "at <Field>"; without a Field,
// the message renders Entity + Message without the locator clause.
func TestResolverError_Error_BothShapes(t *testing.T) {
	t.Parallel()
	withField := &ResolverError{Entity: "Aggregate my-app", Field: ".spec.components.bad", Message: "invalid name"}
	if got := withField.Error(); !strings.Contains(got, "Aggregate my-app") || !strings.Contains(got, "at .spec.components.bad") || !strings.Contains(got, "invalid name") {
		t.Errorf("with-Field format missing component; got: %s", got)
	}
	noField := &ResolverError{Entity: "Aggregate my-app", Message: "general failure"}
	if got := noField.Error(); !strings.Contains(got, "Aggregate my-app") || !strings.Contains(got, "general failure") {
		t.Errorf("no-Field format missing component; got: %s", got)
	}
	if got := noField.Error(); strings.Contains(got, " at ") {
		t.Errorf("no-Field format should not include 'at' clause; got: %s", got)
	}
}

// TestPropertyOf_AbsentReturnsFalse pins the no-properties-map branch
// of the schema walker helper.
func TestPropertyOf_AbsentReturnsFalse(t *testing.T) {
	t.Parallel()
	node := map[string]any{"type": "string"}
	if _, ok := propertyOf(node, "anything"); ok {
		t.Errorf("absent properties: ok should be false")
	}
}

// TestPropertyOf_NonMapPropertiesReturnsFalse pins the type-assert-miss
// branch: a `properties` key whose value is not a map[string]any
// returns false rather than panic on the failed assertion.
func TestPropertyOf_NonMapPropertiesReturnsFalse(t *testing.T) {
	t.Parallel()
	node := map[string]any{"properties": []any{"not", "a", "map"}}
	if _, ok := propertyOf(node, "x"); ok {
		t.Errorf("non-map properties: ok should be false")
	}
}

// TestPropertyOf_PresentReturnsValue pins the happy-path branch: a
// well-formed `properties` map with the named entry returns the entry
// and ok=true.
func TestPropertyOf_PresentReturnsValue(t *testing.T) {
	t.Parallel()
	expected := map[string]any{"type": "integer"}
	node := map[string]any{"properties": map[string]any{"port": expected}}
	val, ok := propertyOf(node, "port")
	if !ok {
		t.Fatal("expected ok=true on present property")
	}
	if got, _ := val.(map[string]any); got["type"] != "integer" {
		t.Errorf("returned wrong property value: %v", val)
	}
}

// TestSchemaPropertyType_EveryBranch pins every documented return shape
// of the schema-property-type walker:
//   - nil schema → skippable
//   - pillar absent → skippable
//   - pillar present but not a map → skippable
//   - empty path → skippable
//   - escape hatch mid-walk → skippable
//   - terminal segment found → (type, true, false)
//   - terminal segment absent → (zero, false, false)
//   - intermediate segment not a map → skippable
func TestSchemaSubSchemaAtPath_EveryBranch(t *testing.T) {
	t.Parallel()
	// (a) Nil schema — skippable.
	if _, found, skippable := schemaSubSchemaAtPath(nil, "infra", "port"); found || !skippable {
		t.Errorf("nil schema: got (found=%v, skippable=%v); want (false, true)", found, skippable)
	}
	// (b) Pillar absent — skippable.
	schemaNoPillar := map[string]any{"properties": map[string]any{}}
	if _, found, skippable := schemaSubSchemaAtPath(schemaNoPillar, "infra", "port"); found || !skippable {
		t.Errorf("pillar absent: got (found=%v, skippable=%v); want (false, true)", found, skippable)
	}
	// (c) Empty path — skippable.
	schemaWithInfra := map[string]any{
		"properties": map[string]any{
			"infra": map[string]any{
				"properties": map[string]any{
					"port": map[string]any{"type": "integer"},
				},
			},
		},
	}
	if _, found, skippable := schemaSubSchemaAtPath(schemaWithInfra, "infra", ""); found || !skippable {
		t.Errorf("empty path: got (found=%v, skippable=%v); want (false, true)", found, skippable)
	}
	// (d) Terminal segment found — returns the sub-schema node (true, false).
	sub, found, skippable := schemaSubSchemaAtPath(schemaWithInfra, "infra", "port")
	if !found || skippable {
		t.Errorf("terminal found: got (found=%v, skippable=%v); want (true, false)", found, skippable)
	}
	if typ, _ := sub["type"].(string); typ != "integer" {
		t.Errorf("terminal sub-schema: got type=%q; want integer", typ)
	}
	// (e) Terminal segment absent — (nil, false, false).
	if _, found, skippable := schemaSubSchemaAtPath(schemaWithInfra, "infra", "missing"); found || skippable {
		t.Errorf("terminal absent: got (found=%v, skippable=%v); want (false, false)", found, skippable)
	}
	// (f) Escape hatch mid-walk — skippable.
	schemaWithEscape := map[string]any{
		"properties": map[string]any{
			"infra": map[string]any{
				"$ref": "#/defs/X",
			},
		},
	}
	if _, found, skippable := schemaSubSchemaAtPath(schemaWithEscape, "infra", "port"); found || !skippable {
		t.Errorf("escape hatch: got (found=%v, skippable=%v); want (false, true)", found, skippable)
	}
}

func TestResolveSubDirectory_PrefersComponentLevel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		boundary  string
		component string
		want      string
	}{
		{"./dist/web", "", "./dist/web"},
		{"./dist/web", "./custom", "./custom"},
		{"", "./component-only", "./component-only"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := resolveSubDirectory(c.boundary, c.component); got != c.want {
			t.Errorf("resolveSubDirectory(%q, %q) = %q; want %q", c.boundary, c.component, got, c.want)
		}
	}
}

// TestDeepMerge_AllBranches pins each documented branch of the
// recursive parameter-merge:
//   - both nil → empty map
//   - base nil → copy of child
//   - child nil → copy of base
//   - new key in child → added
//   - map-vs-map in both → recursive merge
//   - scalar-vs-scalar or mismatched shapes → child replaces base
func TestDeepMerge_AllBranches(t *testing.T) {
	t.Parallel()
	if got := deepMerge(nil, nil); len(got) != 0 {
		t.Errorf("both nil: expected empty map; got %v", got)
	}
	got := deepMerge(nil, map[string]any{"x": 1})
	if got["x"] != 1 {
		t.Errorf("base nil: expected x=1; got %v", got)
	}
	got = deepMerge(map[string]any{"x": 1}, nil)
	if got["x"] != 1 {
		t.Errorf("child nil: expected x=1; got %v", got)
	}
	got = deepMerge(
		map[string]any{"a": 1, "shared": map[string]any{"k1": "v1"}},
		map[string]any{"b": 2, "shared": map[string]any{"k2": "v2"}, "a": 99},
	)
	if got["a"] != 99 || got["b"] != 2 {
		t.Errorf("child replaces / new key: got %v", got)
	}
	sharedMap, ok := got["shared"].(map[string]any)
	if !ok || sharedMap["k1"] != "v1" || sharedMap["k2"] != "v2" {
		t.Errorf("recursive merge: expected both k1 and k2; got %v", got["shared"])
	}
}

// TestSortResolverErrors_StableOrderByEntityThenField pins the
// deterministic-output discipline: errors sort by Entity first, then
// by Field. Same Entity + Field collisions stay in insertion order
// (sort.Slice is not stable but the test inputs avoid same-key
// collisions).
func TestSortResolverErrors_StableOrderByEntityThenField(t *testing.T) {
	t.Parallel()
	errs := []*ResolverError{
		{Entity: "Release", Field: ".spec.boundaries.b"},
		{Entity: "Aggregate", Field: ".spec.components.api"},
		{Entity: "Release", Field: ".spec.boundaries.a"},
		{Entity: "Aggregate", Field: ".spec.components.db"},
	}
	sortResolverErrors(errs)
	wantOrder := []string{
		"Aggregate.spec.components.api",
		"Aggregate.spec.components.db",
		"Release.spec.boundaries.a",
		"Release.spec.boundaries.b",
	}
	for i, w := range wantOrder {
		if errs[i].Entity+errs[i].Field != w {
			t.Errorf("sorted position %d: got %q+%q; want %q", i, errs[i].Entity, errs[i].Field, w)
		}
	}
}

// TestLintAnnotationBindingMaps_NilAnnotationsTierSkipped pins the
// nil-annotations branch of the reading's presentation. A Features tier that
// declares no `annotations` map (nil pointer) presents no binding key, so the
// rule is asked about nothing and no warning is added.
func TestLintAnnotationBindingMaps_NilAnnotationsTierSkipped(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Features = &types.MappingTier{} // Annotations nil
	if warnings := lintBindingMaps(m); warnings != nil {
		t.Errorf("nil annotations tier: expected no warnings; got %v", warnings)
	}
	// A nil Features tier is skipped on the same terms.
	m.Spec.Mappings.Features = nil
	if warnings := lintBindingMaps(m); warnings != nil {
		t.Errorf("nil features tier: expected no warnings; got %v", warnings)
	}
}

// TestValidateAnnotationBindingTargets_EveryEarlyReturn pins the
// three early-return guards. Each guard returns nil so the orchestrator
// can call the validator without prior nil-check.
func TestValidateAnnotationBindingTargets_EveryEarlyReturn(t *testing.T) {
	t.Parallel()
	// (a) Nil manifest.
	if err := validateBindingTargets(nil, CatalogLookup{}); err != nil {
		t.Errorf("nil manifest: expected nil; got %v", err)
	}
	// (b) Manifest with no Features tier.
	m := &types.Manifest{}
	if err := validateBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("no Features tier: expected nil; got %v", err)
	}
	// (c) Features tier with nil Annotations.
	m.Spec.Mappings.Features = &types.MappingTier{}
	if err := validateBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("nil Annotations: expected nil; got %v", err)
	}
	// (d) Annotations present but nil catalog Feature lookup.
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/x", types.AnnotationBinding{Feature: "core/x"})
	m.Spec.Mappings.Features.Annotations = annotations
	if err := validateBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("nil catalog Feature: expected nil; got %v", err)
	}
}

// TestValidateAnnotationBindingTargets_CatalogMissErrors pins the
// catalog-miss branch: a binding pointing at a Feature URI the
// catalog does not carry raises the binding-target condition and names
// the binding key and the unknown Feature.
func TestValidateAnnotationBindingTargets_CatalogMissErrors(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/db", types.AnnotationBinding{Feature: "core/unknown"})
	m.Spec.Mappings.Features = &types.MappingTier{Annotations: annotations}
	cat := CatalogLookup{
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
	err := validateBindingTargets(m, cat)
	if err == nil {
		t.Fatal("expected a halt for the missing Feature; got nil")
	}
	if !stderrors.Is(err, ErrAnnotationBindingTargetUnresolved()) {
		t.Errorf("halt should carry the binding-target condition; got: %v", err)
	}
	if !strings.Contains(err.Error(), "core/unknown") {
		t.Errorf("error should name the missing Feature; got: %s", err.Error())
	}
}

// TestCheckArrayConstraints_EveryBranch pins each documented return
// shape:
//   - Items unset → no errors (any element accepted)
//   - non-array value → no errors (per-type validator dispatch
//     already filtered)
//   - well-formed items + valid elements → no errors
//   - well-formed items + invalid element → error per failing element
func TestCheckArrayConstraints_EveryBranch(t *testing.T) {
	t.Parallel()
	// (a) Items unset.
	if errs := checkArrayConstraints("p", types.ParameterContract{}, []any{1, 2}); len(errs) != 0 {
		t.Errorf("items unset: expected no errors; got %v", errs)
	}
	// (b) Non-array value.
	pcWithItems := types.ParameterContract{
		Items: map[string]any{"type": "string"},
	}
	if errs := checkArrayConstraints("p", pcWithItems, "not-an-array"); len(errs) != 0 {
		t.Errorf("non-array value: expected no errors; got %v", errs)
	}
	// (c) Valid elements pass.
	if errs := checkArrayConstraints("p", pcWithItems, []any{"a", "b"}); len(errs) != 0 {
		t.Errorf("valid elements: expected no errors; got %v", errs)
	}
	// (d) Invalid element — integer-typed items contract, string supplied.
	intItems := types.ParameterContract{
		Items: map[string]any{"type": "integer"},
	}
	errs := checkArrayConstraints("p", intItems, []any{1, "not-int", 3})
	if len(errs) == 0 {
		t.Errorf("invalid element: expected at least one error; got none")
	}
}

// TestAnnotationSubkeyConformance_EveryBranch pins the conformance rule's
// documented dispatch, on the Release side so the infra pillar is the one
// under test:
//   - declared type mismatches the value the author wrote → a halt naming both
//   - declared type matches → no finding
//   - a subkey the binding routes and the Feature declares no property for → a
//     halt naming the subkey
//
// The nil-contract branch is pinned separately, over a build resolving against
// no catalog, where it is the whole of what the rule can see.
func TestAnnotationSubkeyConformance_EveryBranch(t *testing.T) {
	t.Parallel()
	manifest := loadManifest(t, `
apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/sizing:
          feature: core/sizing:1.0.0
          maps:
            infra:
              - port
              - undeclared
`)
	catalog := mockCatalogLookup(map[string]types.Feature{
		"core/sizing:1.0.0": featureWithSchema(t, "core/sizing", `
infra:
  port: { type: integer }
`),
	})

	// Integer expected; a string supplied.
	err := authoredAnnotationRefusal(t, manifest, nil,
		releaseAnnotating(t, `feature.myco/sizing:`+"\n"+`  port: "not-an-integer"`+"\n"), catalog)
	if err == nil || !stderrors.Is(err, ErrAnnotationSubkeyNotConforming()) {
		t.Errorf("type mismatch: expected the subkey-conformance condition; got %v", err)
	}
	// Integer expected; an integer supplied.
	if err := authoredAnnotationRefusal(t, manifest, nil,
		releaseAnnotating(t, "feature.myco/sizing:\n  port: 8080\n"), catalog); err != nil {
		t.Errorf("matching type: expected no finding; got %v", err)
	}
	// A subkey the binding routes and the Feature's contract declares nothing for.
	err = authoredAnnotationRefusal(t, manifest, nil,
		releaseAnnotating(t, "feature.myco/sizing:\n  undeclared: anything\n"), catalog)
	if err == nil || !strings.Contains(err.Error(), "declares no property for") {
		t.Errorf("subkey not declared: expected a halt naming the subkey; got %v", err)
	}
}

// TestExpandBindingStructured_ReleaseSideInfraPathFires pins the
// happy-path Release-side branch: a structured annotation whose
// subkey routes to the infra pillar via Maps.Lookup fires its
// contribution into the BoundaryFeature.Infra block.
func TestExpandBindingStructured_ReleaseSideInfraPathFires(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("pool_size", 20)
	binding := types.AnnotationBinding{
		Feature: "feature.myco/db-pool",
		Maps: types.AnnotationBindingMaps{
			Infra: []string{"pool_size"},
		},
	}
	out, errs := expandBindingStructured(
		types.BoundaryFeature{Type: binding.Feature},
		"feature.myco/db-pool",
		value,
		binding,
		sideRelease,
		nil, // no feature lookup; cache also nil
		nil,
		"release.yaml:boundaries.prod.annotations.feature.myco/db-pool",
	)
	if len(errs) != 0 {
		t.Fatalf("expected no errors; got %v", errs)
	}
	v, ok := out.Infra.Get("pool_size")
	if !ok || v != 20 {
		t.Errorf("expected pool_size=20 in Infra; got (%v, %v)", v, ok)
	}
}

// TestExpandBindingStructured_MultipleSubkeysCollectErrors pins the
// collect-and-emit-all discipline over the faults expansion still reports: two
// subkeys the binding's own map names neither of, reported together rather than
// one per build.
//
// Only that class remains here. The faults over what the routed value IS — the
// pillar its authoring side may use, and the contract the target Feature
// declares — are asked over the authored entry before expansion runs, so
// reporting them here as well would make one rule report itself twice for one
// input.
func TestExpandBindingStructured_MultipleSubkeysCollectErrors(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("unknown_subkey", "x")
	value.Structured.Set("also_unknown", "y")
	binding := types.AnnotationBinding{
		Feature: "feature.myco/db-pool",
		Maps: types.AnnotationBindingMaps{
			Values: []string{"db_name"},
		},
	}
	_, errs := expandBindingStructured(
		types.BoundaryFeature{Type: binding.Feature},
		"feature.myco/db-pool",
		value,
		binding,
		sideRelease,
		nil, nil,
		"release.yaml:boundaries.prod.annotations.feature.myco/db-pool",
	)
	if len(errs) != 2 {
		t.Errorf("expected 2 errors (one per failed subkey); got %d: %v", len(errs), errs)
	}
}

// TestExpandBindingStructured_UnknownSubkeyAccumulatesError pins the
// subkey-not-in-binding-map branch. A structured annotation whose
// subkey doesn't appear in `binding.Maps.Infra` or `binding.Maps.Values`
// accumulates an error citing the unknown subkey + the declared paths.
func TestExpandBindingStructured_UnknownSubkeyAccumulatesError(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("unknown_subkey", "some-value")
	binding := types.AnnotationBinding{
		Feature: "feature.myco/x",
		Maps: types.AnnotationBindingMaps{
			Infra:  []string{"declared_infra"},
			Values: []string{"declared_values"},
		},
	}
	_, errs := expandBindingStructured(
		types.BoundaryFeature{Type: binding.Feature},
		"feature.myco/x",
		value,
		binding,
		sideRelease,
		nil, // no Feature lookup needed; subkey will fail before type validation
		nil, // no cache needed for this path
		"release.yaml:boundaries.prod.annotations.feature.myco/x",
	)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for unknown subkey; got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "unknown_subkey") {
		t.Errorf("error should name the unknown subkey; got: %s", errs[0].Error())
	}
}

// TestExpandBindingStructured_ValuesPillarOnReleaseSideContributesNothing pins
// what expansion does with a subkey routed to the other side's pillar: it fires
// no contribution and reports nothing.
//
// Reporting is not expansion's to do here. Which side may route through which
// pillar is a declared rule asked over the authored entry before expansion runs,
// so a document that crossed the partition has already halted — and a second
// report at this site would make one rule report itself twice for one input. The
// gate that remains is the routing itself, and this pins that it still routes
// nothing.
func TestExpandBindingStructured_ValuesPillarOnReleaseSideContributesNothing(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("db_name", "prod-db")
	binding := types.AnnotationBinding{
		Feature: "feature.myco/db-pool",
		Maps: types.AnnotationBindingMaps{
			Values: []string{"db_name"},
		},
	}
	out, errs := expandBindingStructured(
		types.BoundaryFeature{Type: binding.Feature},
		"feature.myco/db-pool",
		value,
		binding,
		sideRelease,
		nil,
		nil,
		"release.yaml:boundaries.prod.annotations.feature.myco/db-pool",
	)
	if len(errs) != 0 {
		t.Fatalf("expected no expansion-time error; got %d: %v", len(errs), errs)
	}
	if out.Infra.Len() != 0 {
		t.Errorf("a values-routed subkey must contribute nothing to the infra pillar; got %d entr(ies)",
			out.Infra.Len())
	}
}

// TestAggregateAnnotationSourcePath_NoFileIsTheKeyPathAlone pins what the
// formatter produces when the run names no file for the Aggregate: the key path
// the author wrote, with no file name put in front of it. A name the run was not
// given would send the reader to a file that may not be the one holding the
// annotation.
func TestAggregateAnnotationSourcePath_NoFileIsTheKeyPathAlone(t *testing.T) {
	t.Parallel()
	got := aggregateAnnotationSourcePath("", "subsystems.data", "", "feature.myco/db-pool", false)
	want := "subsystems.data.annotations.feature.myco/db-pool"
	if got != want {
		t.Errorf("no file: got %q; want %q", got, want)
	}
}

// TestAggregateAnnotationSourcePath_NamedPrefix pins the happy-path
// formatting: a non-empty file path is preserved as the prefix.
func TestAggregateAnnotationSourcePath_NamedPrefix(t *testing.T) {
	t.Parallel()
	got := aggregateAnnotationSourcePath("/abs/path/aggregate.yaml", "subsystems.default", "api", "feature.myco/rewrite", true)
	want := "/abs/path/aggregate.yaml:components.api.annotations.feature.myco/rewrite"
	if got != want {
		t.Errorf("named prefix: got %q; want %q", got, want)
	}
}

// TestAggregateAnnotationSourcePath_SynthesizedScopeIsElided pins the
// Aggregate-side formatter against its Release-side sibling's rule: a component
// in the synthesized default subsystem is named at the top-level shape the
// author wrote, keyed on the provenance flag, while a subsystem the author
// declared — even one named like the synthesized scope — keeps its own path.
func TestAggregateAnnotationSourcePath_SynthesizedScopeIsElided(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		scope       string
		component   string
		synthesized bool
		want        string
	}{
		{"top-level component", "subsystems.default", "api", true,
			"aggregate.yaml:components.api.annotations.feature.myco/x"},
		{"declared subsystem named default", "subsystems.default", "api", false,
			"aggregate.yaml:subsystems.default.components.api.annotations.feature.myco/x"},
		{"nested declared subsystem", "subsystems.a.subsystems.b", "api", false,
			"aggregate.yaml:subsystems.a.subsystems.b.components.api.annotations.feature.myco/x"},
		{"declared subsystem scope", "subsystems.a", "", false,
			"aggregate.yaml:subsystems.a.annotations.feature.myco/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := aggregateAnnotationSourcePath("aggregate.yaml", tc.scope, tc.component, "feature.myco/x", tc.synthesized)
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

// TestAnnotationSourcePath_ComponentVsBoundaryShape pins the
// boundary-vs-component branch of the Release-side annotation source
// formatter: empty componentName produces a boundary-scoped path;
// non-empty componentName produces a component-scoped path.
func TestAnnotationSourcePath_ComponentVsBoundaryShape(t *testing.T) {
	t.Parallel()
	if got := annotationSourcePath("release.yaml", "prod", "", "feature.myco/x", false); got != "release.yaml:boundaries.prod.annotations.feature.myco/x" {
		t.Errorf("boundary-scoped: got %q", got)
	}
	if got := annotationSourcePath("release.yaml", "prod", "api", "feature.myco/x", false); got != "release.yaml:boundaries.prod.components.api.annotations.feature.myco/x" {
		t.Errorf("component-scoped: got %q", got)
	}
	// No file named for the Release: the key path alone, no file name invented.
	if got := annotationSourcePath("", "prod", "api", "feature.myco/x", false); got != "boundaries.prod.components.api.annotations.feature.myco/x" {
		t.Errorf("no file: got %q", got)
	}
	// A file the run names is the prefix, whatever it is called.
	if got := annotationSourcePath("releases/prod.yaml", "prod", "api", "feature.myco/x", false); got != "releases/prod.yaml:boundaries.prod.components.api.annotations.feature.myco/x" {
		t.Errorf("named file: got %q", got)
	}
	// Nested boundary uses slash-to-dot translation.
	if got := annotationSourcePath("release.yaml", "outer/inner", "", "feature.myco/x", false); got != "release.yaml:boundaries.outer.boundaries.inner.annotations.feature.myco/x" {
		t.Errorf("nested boundary: got %q", got)
	}
	// A synthesized-default boundary attributes annotations to the root shape
	// the author typed — the generated `default` scope never appears. (R3 has no
	// shipped consumer yet; this proves the formatter directly.)
	if got := annotationSourcePath("release.yaml", "default", "api", "feature.myco/x", true); got != "release.yaml:components.api.annotations.feature.myco/x" {
		t.Errorf("synthesized component-scoped: got %q", got)
	}
	if got := annotationSourcePath("release.yaml", "default", "", "feature.myco/x", true); got != "release.yaml:annotations.feature.myco/x" {
		t.Errorf("synthesized boundary-scoped: got %q", got)
	}
}

// TestSchemaHasPropertyAtPath_EveryBranch pins every documented return
// shape of the path-existence walker (a sibling of
// schemaPropertyType that returns (ok, skippable) without the type
// value):
//   - nil schema → skippable
//   - pillar absent → skippable
//   - pillar present but not a map → skippable
//   - empty path → (true, false)
//   - terminal segment found → (true, false)
//   - terminal segment absent → (false, false)
//   - escape hatch mid-walk → skippable
//   - intermediate segment not a map → skippable
func TestSchemaHasPropertyAtPath_EveryBranch(t *testing.T) {
	t.Parallel()
	// (a) Nil schema.
	if ok, skippable := schemaHasPropertyAtPath(nil, "infra", "port"); ok || !skippable {
		t.Errorf("nil schema: got (ok=%v, skippable=%v); want (false, true)", ok, skippable)
	}
	// (b) Pillar absent.
	schemaNoPillar := map[string]any{"properties": map[string]any{}}
	if ok, skippable := schemaHasPropertyAtPath(schemaNoPillar, "infra", "port"); ok || !skippable {
		t.Errorf("pillar absent: got (ok=%v, skippable=%v); want (false, true)", ok, skippable)
	}
	// (c) Well-formed schema.
	schemaWithInfra := map[string]any{
		"properties": map[string]any{
			"infra": map[string]any{
				"properties": map[string]any{
					"port":   map[string]any{"type": "integer"},
					"nested": map[string]any{"properties": map[string]any{"leaf": map[string]any{"type": "string"}}},
				},
			},
		},
	}
	if ok, skippable := schemaHasPropertyAtPath(schemaWithInfra, "infra", ""); !ok || skippable {
		t.Errorf("empty path: got (ok=%v, skippable=%v); want (true, false)", ok, skippable)
	}
	if ok, skippable := schemaHasPropertyAtPath(schemaWithInfra, "infra", "port"); !ok || skippable {
		t.Errorf("terminal found: got (ok=%v, skippable=%v); want (true, false)", ok, skippable)
	}
	if ok, skippable := schemaHasPropertyAtPath(schemaWithInfra, "infra", "missing"); ok || skippable {
		t.Errorf("terminal absent: got (ok=%v, skippable=%v); want (false, false)", ok, skippable)
	}
	if ok, skippable := schemaHasPropertyAtPath(schemaWithInfra, "infra", "nested.leaf"); !ok || skippable {
		t.Errorf("nested path: got (ok=%v, skippable=%v); want (true, false)", ok, skippable)
	}
	// (d) Escape hatch mid-walk.
	schemaWithEscape := map[string]any{
		"properties": map[string]any{
			"infra": map[string]any{"$ref": "#/defs/X"},
		},
	}
	if ok, skippable := schemaHasPropertyAtPath(schemaWithEscape, "infra", "port"); ok || !skippable {
		t.Errorf("escape hatch: got (ok=%v, skippable=%v); want (false, true)", ok, skippable)
	}
}

// TestEvalValidatePredicate_TrueFalseAndErrors pins the predicate-
// evaluator dispatch:
//   - template renders "true" → (true, nil)
//   - template renders "false" → (false, nil)
//   - template parse error → (false, error)
//   - template execution error → (false, error)
//   - template renders something else → (false, error)
func TestEvalValidatePredicate_TrueFalseAndErrors(t *testing.T) {
	t.Parallel()
	if got, err := evalValidatePredicate(`{{ eq .value "ok" }}`, "ok"); err != nil || !got {
		t.Errorf("eq match: got (%v, %v); want (true, nil)", got, err)
	}
	if got, err := evalValidatePredicate(`{{ eq .value "ok" }}`, "nope"); err != nil || got {
		t.Errorf("eq miss: got (%v, %v); want (false, nil)", got, err)
	}
	if _, err := evalValidatePredicate(`{{ malformed`, "x"); err == nil {
		t.Errorf("parse error: expected non-nil error")
	}
	if _, err := evalValidatePredicate(`{{ .value.missing }}`, "x"); err == nil {
		t.Errorf("execute error: expected non-nil error")
	}
	if _, err := evalValidatePredicate(`{{ .value }}`, "neither-true-nor-false"); err == nil {
		t.Errorf("non-truthy render: expected non-nil error")
	}
}

// TestTypeMatches_DiscriminatesEveryDeclaredType pins the type-coercion
// matrix per the documented contract. Each declared type accepts the
// documented Go-runtime types and rejects the rest.
func TestTypeMatches_DiscriminatesEveryDeclaredType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		declared string
		value    any
		want     bool
	}{
		{"string", "x", true},
		{"string", 42, false},
		{"string", true, false},
		{"boolean", true, true},
		{"boolean", "true", false},
		{"integer", 42, true},
		{"integer", int8(1), true},
		{"integer", int16(1), true},
		{"integer", int32(1), true},
		{"integer", int64(1), true},
		{"integer", uint(1), true},
		{"integer", uint8(1), true},
		{"integer", uint16(1), true},
		{"integer", uint32(1), true},
		{"integer", uint64(1), true},
		{"integer", 3.14, false},
		{"integer", "1", false},
		{"number", 42, true},
		{"number", 3.14, true},
		{"number", float32(2.71), true},
		{"number", "1", false},
		{"number", true, false},
		{"array", []any{1, 2, 3}, true},
		{"array", "[1,2]", false},
		{"object", map[string]any{"x": 1}, true},
		{"object", []any{}, false},
		{"unknown_type", "anything", true}, // unknown declared types accept any value
	}
	for _, c := range cases {
		if got := typeMatches(c.declared, c.value); got != c.want {
			t.Errorf("typeMatches(%q, %T) = %v; want %v", c.declared, c.value, got, c.want)
		}
	}
}

// TestToFloat_AcceptsEveryNumericRuntimeType pins the numeric-coercion
// table. Every Go integer + float type must coerce to a float64; non-
// numeric types return (0, false) so callers can distinguish a real
// zero from a coercion failure.
func TestToFloat_AcceptsEveryNumericRuntimeType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value any
		want  float64
		ok    bool
	}{
		{int(42), 42, true},
		{int8(42), 42, true},
		{int16(42), 42, true},
		{int32(42), 42, true},
		{int64(42), 42, true},
		{uint(42), 42, true},
		{uint8(42), 42, true},
		{uint16(42), 42, true},
		{uint32(42), 42, true},
		{uint64(42), 42, true},
		{float32(3.5), 3.5, true},
		{float64(3.5), 3.5, true},
		{"42", 0, false},
		{true, 0, false},
		{nil, 0, false},
		{[]any{}, 0, false},
	}
	for _, c := range cases {
		got, ok := toFloat(c.value)
		if ok != c.ok {
			t.Errorf("toFloat(%T %v): ok = %v; want %v", c.value, c.value, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("toFloat(%T %v) = %v; want %v", c.value, c.value, got, c.want)
		}
	}
}

// TestHasEscapeHatch_DetectsEveryEscapeClause pins the resolver-side
// escape-hatch detector used during the per-subkey schema walker.
// Mirrors validator/hasOverlapEscapeHatch but at the annotation-
// binding-target validation surface.
func TestHasEscapeHatch_DetectsEveryEscapeClause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		node map[string]any
		want bool
	}{
		{"ref", map[string]any{"$ref": "#/defs/X"}, true},
		{"patternProperties", map[string]any{"patternProperties": map[string]any{}}, true},
		{"oneOf", map[string]any{"oneOf": []any{}}, true},
		{"anyOf", map[string]any{"anyOf": []any{}}, true},
		{"allOf", map[string]any{"allOf": []any{}}, true},
		{"additionalProperties_true", map[string]any{"additionalProperties": true}, true},
		{"additionalProperties_false", map[string]any{"additionalProperties": false}, false},
		{"additionalProperties_schema_body", map[string]any{"additionalProperties": map[string]any{"type": "string"}}, true},
		{"plain", map[string]any{"type": "string"}, false},
		{"propertiesOnly", map[string]any{"properties": map[string]any{}}, false},
	}
	for _, c := range cases {
		if got := hasEscapeHatch(c.node); got != c.want {
			t.Errorf("hasEscapeHatch(%s) = %v; want %v", c.name, got, c.want)
		}
	}
}

// TestVersionedName_VersionedAndUnversionedShapes pins the cache-key
// derivation: a Feature with Metadata.Version produces a "name:version"
// key; a Feature without Version produces the bare name.
func TestVersionedName_VersionedAndUnversionedShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		feature *types.Feature
		want    string
	}{
		{&types.Feature{Metadata: types.FeatureMetadata{Name: "core/db", Version: "1.0.0"}}, "core/db:1.0.0"},
		{&types.Feature{Metadata: types.FeatureMetadata{Name: "core/db"}}, "core/db"},
		{&types.Feature{Metadata: types.FeatureMetadata{Name: "core/db", Version: ""}}, "core/db"},
	}
	for _, c := range cases {
		if got := versionedName(c.feature); got != c.want {
			t.Errorf("versionedName(%+v) = %q; want %q", c.feature.Metadata, got, c.want)
		}
	}
}

// TestSplitAggregatePath_EveryShape pins the canonical-path tokeniser.
// Top-level component path returns 2 segments; subsystem-nested paths
// return 4+ segments; empty path returns nil.
func TestSplitAggregatePath_EveryShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want []string
	}{
		{"", nil},
		{"components.api", []string{"components", "api"}},
		{"subsystems.data.components.api", []string{"subsystems", "data", "components", "api"}},
		{"subsystems.web.subsystems.frontend.components.ui", []string{"subsystems", "web", "subsystems", "frontend", "components", "ui"}},
	}
	for _, c := range cases {
		got := splitAggregatePath(c.path)
		if len(got) != len(c.want) {
			t.Errorf("splitAggregatePath(%q) length = %d; want %d (got %v)", c.path, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitAggregatePath(%q)[%d] = %q; want %q", c.path, i, got[i], c.want[i])
			}
		}
	}
}

// TestSplitBoundaryPath_SlashSeparatedTokens pins the boundary-path
// tokeniser used by the Release walker. Boundary paths use `/` as the
// separator (distinct from Aggregate paths' `.`); empty path returns
// nil; trailing slash produces an empty trailing segment.
func TestSplitBoundaryPath_SlashSeparatedTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want []string
	}{
		{"", nil},
		{"outer", []string{"outer"}},
		{"outer/inner", []string{"outer", "inner"}},
		{"a/b/c/d", []string{"a", "b", "c", "d"}},
		{"outer/", []string{"outer", ""}},
		{"/leading", []string{"", "leading"}},
	}
	for _, c := range cases {
		got := splitBoundaryPath(c.path)
		if len(got) != len(c.want) {
			t.Errorf("splitBoundaryPath(%q) length = %d; want %d (got %v)", c.path, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitBoundaryPath(%q)[%d] = %q; want %q", c.path, i, got[i], c.want[i])
			}
		}
	}
}

// FuzzSplitBoundaryPath exercises the boundary-path tokeniser with
// arbitrary input. The fuzz target seeds from the table-driven cases
// and asserts the round-trip invariant: joining the tokens with `/`
// reproduces the original input. Catches separator-handling regressions
// the table-driven enumeration would miss (unicode runes that contain
// `/` byte sequences, empty segments at every position, etc.).
func FuzzSplitBoundaryPath(f *testing.F) {
	f.Add("")
	f.Add("outer")
	f.Add("outer/inner")
	f.Add("a/b/c/d")
	f.Add("outer/")
	f.Add("/leading")
	f.Add("//double//")
	f.Fuzz(func(t *testing.T, path string) {
		tokens := splitBoundaryPath(path)
		// Empty path → nil tokens contract; assert directly.
		if path == "" {
			if tokens != nil {
				t.Errorf("empty path should return nil; got %v", tokens)
			}
			return
		}
		// Reconstruct: joining the tokens with `/` reproduces input.
		joined := ""
		for i, tok := range tokens {
			if i > 0 {
				joined += "/"
			}
			joined += tok
		}
		if joined != path {
			t.Errorf("round-trip failed: split(%q) = %v; rejoined = %q", path, tokens, joined)
		}
	})
}

// FuzzSplitAggregatePath exercises the canonical-aggregate-path
// tokeniser with arbitrary input. Round-trip invariant: tokens joined
// by `.` reproduce the original input. Catches edge cases in the
// segment-splitting around empty segments and unicode boundaries.
func FuzzSplitAggregatePath(f *testing.F) {
	f.Add("")
	f.Add("components.api")
	f.Add("subsystems.data.components.api")
	f.Add(".leading-dot")
	f.Add("trailing-dot.")
	f.Add("..double..dots")
	f.Fuzz(func(t *testing.T, path string) {
		tokens := splitAggregatePath(path)
		if path == "" {
			if tokens != nil {
				t.Errorf("empty path should return nil; got %v", tokens)
			}
			return
		}
		joined := ""
		for i, tok := range tokens {
			if i > 0 {
				joined += "."
			}
			joined += tok
		}
		if joined != path {
			t.Errorf("round-trip failed: split(%q) = %v; rejoined = %q", path, tokens, joined)
		}
	})
}
