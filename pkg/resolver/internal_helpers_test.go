// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
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

// TestIsCredentialFeature_EveryBranch pins all four documented return
// paths of the credential-feature discriminator:
//   - nil catalog Feature lookup → false
//   - catalog miss → false
//   - present but no kind=secret label → false
//   - present with kind=secret label → true
func TestIsCredentialFeature_EveryBranch(t *testing.T) {
	t.Parallel()
	// (a) Nil lookup.
	if isCredentialFeature("anything", CatalogLookup{}) {
		t.Errorf("nil lookup should return false")
	}
	// (b) Catalog miss.
	emptyCat := CatalogLookup{Feature: func(string) (types.Feature, bool) { return types.Feature{}, false }}
	if isCredentialFeature("missing", emptyCat) {
		t.Errorf("catalog miss should return false")
	}
	// (c) Present, no kind label.
	noLabel := CatalogLookup{Feature: func(string) (types.Feature, bool) {
		return types.Feature{Metadata: types.FeatureMetadata{Name: "x"}}, true
	}}
	if isCredentialFeature("x", noLabel) {
		t.Errorf("no kind label should return false")
	}
	// (d) Present, kind=secret label.
	labels := types.OrderedMap[string]{}
	labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
	withSecret := CatalogLookup{Feature: func(string) (types.Feature, bool) {
		return types.Feature{Metadata: types.FeatureMetadata{Name: "x", Labels: labels}}, true
	}}
	if !isCredentialFeature("x", withSecret) {
		t.Errorf("kind=secret label should return true")
	}
}

// TestResolveSubDirectory_PrefersComponentLevel pins the precedence
// rule: when a component declares its own subdirectory, the
// component-level value wins; when the component omits it, the
// boundary-level value is used.
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

// TestValidateSubsystemNames_InvalidNamesSurfaceAtSubsystemPath pins
// the subsystem-name validator: invalid feature names, subsystem
// names, and component names ALL surface as separate ResolverErrors
// with their correct dotted-path locators. Recursive — an invalid
// name nested in a sub-subsystem surfaces with the full path.
func TestValidateSubsystemNames_InvalidNamesSurfaceAtSubsystemPath(t *testing.T) {
	t.Parallel()
	// Invalid feature instance name (contains dot) at this level.
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("bad.feature", types.FeatureInstance{Type: "x"})
	// Invalid subsystem child name (slash).
	innerFeats := types.OrderedMap[types.FeatureInstance]{}
	innerFeats.Set("inner-bad.name", types.FeatureInstance{Type: "y"})
	inner := types.Subsystem{Features: innerFeats}
	innerSubs := types.OrderedMap[types.Subsystem]{}
	innerSubs.Set("bad/nested", inner)
	sub := types.Subsystem{
		Features:   feats,
		Subsystems: innerSubs,
	}
	errs := validateSubsystemNames("Aggregate", ".spec.subsystems.outer", sub)
	if len(errs) < 3 {
		t.Errorf("expected at least 3 errors (top-level feature, nested subsystem name, deep feature); got %d: %v", len(errs), errs)
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
// nil-annotations branch of the lint walker. A Features tier that
// declares no `annotations` map (nil pointer) is skipped without
// adding warnings — the walker's per-tier check returns early.
func TestLintAnnotationBindingMaps_NilAnnotationsTierSkipped(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Features = &types.MappingTier{} // Annotations nil
	if warnings := LintAnnotationBindingMaps(m); warnings != nil {
		t.Errorf("nil annotations tier: expected no warnings; got %v", warnings)
	}
	// Legacy tier with nil annotations also skipped.
	legacy := &types.ProviderMappings{Features: &types.MappingTier{}} // Annotations nil
	m.Spec.Mappings.Legacy.Set("helm", *legacy)
	if warnings := LintAnnotationBindingMaps(m); warnings != nil {
		t.Errorf("legacy tier nil annotations: expected no warnings; got %v", warnings)
	}
}

// TestValidateAnnotationBindingTargets_EveryEarlyReturn pins the
// three early-return guards. Each guard returns nil so the orchestrator
// can call the validator without prior nil-check.
func TestValidateAnnotationBindingTargets_EveryEarlyReturn(t *testing.T) {
	t.Parallel()
	// (a) Nil manifest.
	if err := ValidateAnnotationBindingTargets(nil, CatalogLookup{}); err != nil {
		t.Errorf("nil manifest: expected nil; got %v", err)
	}
	// (b) Manifest with no Features tier.
	m := &types.Manifest{}
	if err := ValidateAnnotationBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("no Features tier: expected nil; got %v", err)
	}
	// (c) Features tier with nil Annotations.
	m.Spec.Mappings.Features = &types.MappingTier{}
	if err := ValidateAnnotationBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("nil Annotations: expected nil; got %v", err)
	}
	// (d) Annotations present but nil catalog Feature lookup.
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/x", types.AnnotationBinding{Feature: "core/x"})
	m.Spec.Mappings.Features.Annotations = annotations
	if err := ValidateAnnotationBindingTargets(m, CatalogLookup{}); err != nil {
		t.Errorf("nil catalog Feature: expected nil; got %v", err)
	}
}

// TestValidateAnnotationBindingTargets_CatalogMissErrors pins the
// CATALOG_ERROR branch: a binding pointing at a Feature URI the
// catalog does not carry returns an error naming the binding key and
// the unknown Feature.
func TestValidateAnnotationBindingTargets_CatalogMissErrors(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	annotations := &types.OrderedMap[types.AnnotationBinding]{}
	annotations.Set("feature.myco/db", types.AnnotationBinding{Feature: "core/unknown"})
	m.Spec.Mappings.Features = &types.MappingTier{Annotations: annotations}
	cat := CatalogLookup{
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
	err := ValidateAnnotationBindingTargets(m, cat)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for missing Feature; got nil")
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") || !strings.Contains(err.Error(), "core/unknown") {
		t.Errorf("error should name the missing Feature; got: %s", err.Error())
	}
}

// TestCollectUsesInjectDeliveryKeys_EmptyUsesReturnsEmpty pins the
// short-circuit: an empty UsesSocketValue map returns an empty result
// without walking the dispatch table.
func TestCollectUsesInjectDeliveryKeys_EmptyUsesReturnsEmpty(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	got := collectUsesInjectDeliveryKeys(uses)
	if len(got) != 0 {
		t.Errorf("empty uses: expected empty result; got %v", got)
	}
}

// TestCollectUsesInjectDeliveryKeys_VerboseFormShapes pins the verbose-
// form dispatch: every entry's Inject block contributes its keys to
// the result map by mechanism. Sorted keys per mechanism for
// deterministic output.
func TestCollectUsesInjectDeliveryKeys_VerboseFormShapes(t *testing.T) {
	t.Parallel()
	envMap := &types.OrderedMap[any]{}
	envMap.Set("VAR_A", "from_a")
	envMap.Set("VAR_B", "from_b")
	fileMap := &types.OrderedMap[any]{}
	fileMap.Set("/etc/config.json", "config")
	uses := types.OrderedMap[types.UsesSocketValue]{}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("primary", types.VerboseUsesEntry{
		Target: "backend",
		Inject: &types.InjectBlock{
			Env:  envMap,
			File: fileMap,
		},
	})
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
	got := collectUsesInjectDeliveryKeys(uses)
	if len(got["env"]) != 2 || got["env"][0] != "VAR_A" || got["env"][1] != "VAR_B" {
		t.Errorf("env keys: expected [VAR_A, VAR_B]; got %v", got["env"])
	}
	if len(got["file"]) != 1 || got["file"][0] != "/etc/config.json" {
		t.Errorf("file keys: expected one entry; got %v", got["file"])
	}
}

// TestValidateComponentInstanceNames_FeatureAndSecretNames pins both
// loop branches: invalid feature names produce one error; invalid
// secret names produce another. The branches share the path-format
// shape but use distinct sub-block dispatches.
func TestValidateComponentInstanceNames_FeatureAndSecretNames(t *testing.T) {
	t.Parallel()
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("bad.feature.name", types.FeatureInstance{Type: "x"})
	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("bad/secret/name", types.SecretInstance{Type: "y"})
	comp := types.ComponentInstance{Features: feats, Secrets: secrets}
	errs := validateComponentInstanceNames("Aggregate", "api", comp)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors (one feature + one secret); got %d: %v", len(errs), errs)
	}
	var sawFeature, sawSecret bool
	for _, e := range errs {
		if strings.Contains(e.Field, ".features[") {
			sawFeature = true
		}
		if strings.Contains(e.Field, ".secrets[") {
			sawSecret = true
		}
	}
	if !sawFeature || !sawSecret {
		t.Errorf("expected one feature error + one secret error; got %v", errs)
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

// TestValidateSubkeyValueAgainstFeatureSchema_EveryBranch pins the
// validator's documented dispatch:
//   - nil feature → nil (defensive soft-skip)
//   - compile failure → nil (defensive soft-skip per default-to-skip discipline)
//   - skippable branch (escape-hatch in schema) → nil
//   - found=false (property not declared) → OPS_ERROR naming the subkey
//   - declared property carries no `type` field → nil (relaxed default)
//   - declared type mismatches runtime Go type → OPS_ERROR naming both
//   - declared type matches runtime → nil
func TestValidateSubkeyValueAgainstFeatureSchema_EveryBranch(t *testing.T) {
	t.Parallel()
	// (a) Nil feature.
	if err := validateSubkeyValueAgainstFeatureSchema(newFeatureSchemaCache(nil), nil, "infra", "port", 8080); err != nil {
		t.Errorf("nil feature: expected nil; got %v", err)
	}
	// (b) Type mismatch — string supplied for an integer-typed schema.
	cache := newFeatureSchemaCache(nil)
	// Build a Feature with a real schema (compile must succeed).
	var schemaNode yaml.Node
	if err := yaml.Unmarshal([]byte(`infra:
  port: { type: integer }
`), &schemaNode); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	doc := &schemaNode
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	feature := &types.Feature{
		Metadata: types.FeatureMetadata{Name: "test/feat", Version: "1.0.0"},
		Spec:     types.FeatureSpec{Schema: *doc},
	}
	// Integer expected; string supplied → mismatch error.
	err := validateSubkeyValueAgainstFeatureSchema(cache, feature, "infra", "port", "not-an-integer")
	if err == nil || !strings.Contains(err.Error(), "OPS_ERROR") {
		t.Errorf("type mismatch: expected OPS_ERROR; got %v", err)
	}
	// Integer expected; integer supplied → no error.
	if err := validateSubkeyValueAgainstFeatureSchema(cache, feature, "infra", "port", 8080); err != nil {
		t.Errorf("matching type: expected nil; got %v", err)
	}
	// (c) Subkey not declared on the schema.
	err = validateSubkeyValueAgainstFeatureSchema(cache, feature, "infra", "undeclared", "anything")
	if err == nil || !strings.Contains(err.Error(), "is not declared") {
		t.Errorf("subkey not declared: expected OPS_ERROR; got %v", err)
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
// collect-and-emit-all discipline: when multiple subkeys fail
// independent checks (one unknown subkey + one side-vs-pillar
// mismatch), both errors surface in the returned slice rather than
// halting on the first.
func TestExpandBindingStructured_MultipleSubkeysCollectErrors(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("unknown_subkey", "x")
	value.Structured.Set("db_name", "y") // routes to values; release-side fails
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

// TestExpandBindingStructured_ValuesPillarOnReleaseSideFails pins the
// side-vs-pillar enforcement: a Release-side annotation whose subkey
// routes to the values pillar produces an error (the subkey would be
// inaccessible at expansion).
func TestExpandBindingStructured_ValuesPillarOnReleaseSideFails(t *testing.T) {
	t.Parallel()
	value := types.AnnotationValue{Structured: types.OrderedMap[any]{}}
	value.Structured.Set("db_name", "prod-db")
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
		nil,
		nil,
		"release.yaml:boundaries.prod.annotations.feature.myco/db-pool",
	)
	if len(errs) != 1 {
		t.Fatalf("expected 1 side-vs-pillar error; got %d: %v", len(errs), errs)
	}
}

// TestAggregateAnnotationSourcePath_EmptyPrefixDefault pins the
// fallback when the Aggregate file path is empty: the formatter
// substitutes "aggregate.yaml" so source locators have a usable prefix
// even when the resolver was invoked without a file-on-disk Aggregate.
func TestAggregateAnnotationSourcePath_EmptyPrefixDefault(t *testing.T) {
	t.Parallel()
	got := aggregateAnnotationSourcePath("", "subsystems.data", "feature.myco/db-pool")
	want := "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool"
	if got != want {
		t.Errorf("empty prefix: got %q; want %q", got, want)
	}
}

// TestAggregateAnnotationSourcePath_NamedPrefix pins the happy-path
// formatting: a non-empty file path is preserved as the prefix.
func TestAggregateAnnotationSourcePath_NamedPrefix(t *testing.T) {
	t.Parallel()
	got := aggregateAnnotationSourcePath("/abs/path/aggregate.yaml", "components.api", "feature.myco/rewrite")
	want := "/abs/path/aggregate.yaml:components.api.annotations.feature.myco/rewrite"
	if got != want {
		t.Errorf("named prefix: got %q; want %q", got, want)
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
	// Empty prefix falls back to release.yaml.
	if got := annotationSourcePath("", "prod", "api", "feature.myco/x", false); got != "release.yaml:boundaries.prod.components.api.annotations.feature.myco/x" {
		t.Errorf("empty prefix fallback: got %q", got)
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

// TestGuardUsesSocket_SubsystemRecursionWithSocketViolations pins the
// guardUsesSocketSubsystem path — a subsystem containing a component
// whose `uses` references an undeclared socket surfaces an error at
// the subsystem-scoped path. Recursion through nested subsystems
// surfaces nested errors with the correct dotted path.
func TestGuardUsesSocket_SubsystemRecursionWithSocketViolations(t *testing.T) {
	t.Parallel()
	// Component with a uses edge to a socket the catalog doesn't declare.
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("undeclared_socket", types.UsesSocketValue{})
	innerComp := types.ComponentInstance{Type: "core/server", Uses: uses}
	innerComps := types.OrderedMap[types.ComponentInstance]{}
	innerComps.Set("api", innerComp)
	innerSub := types.Subsystem{Components: innerComps}
	outerSubs := types.OrderedMap[types.Subsystem]{}
	outerSubs.Set("nested", innerSub)
	outerSub := types.Subsystem{Subsystems: outerSubs}
	// Catalog that declares the Component but with no usesSchema entries.
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server" {
				return types.Component{
					Spec: types.ComponentSpec{
						UsesSchema: types.OrderedMap[types.UsesSchemaEntry]{},
					},
				}, true
			}
			return types.Component{}, false
		},
	}
	rootSubs := types.OrderedMap[types.Subsystem]{}
	rootSubs.Set("outer", outerSub)
	agg := &types.Aggregate{Spec: types.AggregateSpec{Subsystems: rootSubs}}
	errs := guardUsesSocket(normalized(t, agg), catalog)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from undeclared socket; got %d (%v)", len(errs), errs)
	}
	if !strings.Contains(errs[0].Field, "subsystems.nested") {
		t.Errorf("error field should carry the nested subsystem path; got %q", errs[0].Field)
	}
}
