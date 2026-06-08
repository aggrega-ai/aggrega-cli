// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// schemaComponentCatalog returns a CatalogLookup whose single Component
// "app/svc:1.0.0" carries a values schema for the conformance tests:
//
//	values: name (string, required, no default) · port (integer, minimum 1) ·
//	        count (integer, required, default 3)   ← required-with-default ·
//	        db.port (nested integer, minimum 1)    ← nested-violation vehicle
//
// compactform compiles the pillar closed (additionalProperties: false).
func schemaComponentCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const schemaYAML = `values:
  name: { type: string, required: true }
  port: { type: integer, minimum: 1 }
  count: { type: integer, default: 3, required: true }
  db:
    port: { type: integer, minimum: 1 }
`
	return catalogFromComponentSchema(t, schemaYAML)
}

// catalogFromComponentSchema builds a CatalogLookup with one Component
// "app/svc:1.0.0" whose spec.schema is the supplied YAML.
func catalogFromComponentSchema(t *testing.T, schemaYAML string) CatalogLookup {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(schemaYAML), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "app/svc:1.0.0" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "app/svc", Version: "1.0.0"},
					Spec:     types.ComponentSpec{Schema: node},
				}, true
			}
			return types.Component{}, false
		},
	}
}

// aggWithValues builds a one-component Aggregate ("svc" of type app/svc:1.0.0)
// whose values pillar is the supplied key/value pairs.
func aggWithValues(pairs ...any) *types.Aggregate {
	vals := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		vals.Set(pairs[i].(string), pairs[i+1])
	}
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0", Values: vals})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "demo"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

func runSchemaGuard(agg *types.Aggregate, t *testing.T) []*ResolverError {
	return guardSchemaConformance(agg, schemaComponentCatalog(t), nil)
}

// --- Negative: values pillar ---------------------------------------------

func TestGuardSchemaConformance_ValuesWrongType(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "port", "not-an-int"), t)
	requireConformance(t, errs, 1, ".spec.components.svc.values.port")
}

func TestGuardSchemaConformance_ValuesOutOfRange(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "port", 0), t)
	requireConformance(t, errs, 1, ".spec.components.svc.values.port")
}

func TestGuardSchemaConformance_ValuesMissingRequiredNonDefaulted(t *testing.T) {
	t.Parallel()
	// name is required with no default; omitting it must halt.
	errs := runSchemaGuard(aggWithValues("port", 8080), t)
	requireConformance(t, errs, 1, ".spec.components.svc.values")
}

func TestGuardSchemaConformance_ValuesUnknownProperty(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "bogus", "x"), t)
	requireConformance(t, errs, 1, ".spec.components.svc.values")
}

// TestGuardSchemaConformance_ComposedValuesSchemaValidates pins correction (a):
// a values schema composed via a single-document #/$defs $ref must be VALIDATED
// (not silently skipped because a bare sub-schema cannot resolve standalone). A
// violating value behind the $ref halts.
func TestGuardSchemaConformance_ComposedValuesSchemaValidates(t *testing.T) {
	t.Parallel()
	const composedSchema = `$defs:
  PortParams:
    port: { type: integer, minimum: 1 }
values:
  $ref: "#/$defs/PortParams"
`
	catalog := catalogFromComponentSchema(t, composedSchema)
	errs := guardSchemaConformance(aggWithValues("port", 0), catalog, nil)
	requireConformance(t, errs, 1, ".spec.components.svc.values")
}

// TestGuardSchemaConformance_NestedViolationSingleError pins dedup: a nested
// values.db.port violation produces exactly ONE error, at the deepest path —
// not one per ancestor object along the failure path.
func TestGuardSchemaConformance_NestedViolationSingleError(t *testing.T) {
	t.Parallel()
	db := types.OrderedMap[any]{}
	db.Set("port", 0) // violates db.port minimum 1
	errs := runSchemaGuard(aggWithValues("name", "api", "db", db), t)
	requireConformance(t, errs, 1, ".spec.components.svc.values.db.port")
}

// TestGuardSchemaConformance_TwoIndependentViolations pins dedup: two
// independent top-level violations produce exactly TWO errors — the shared
// ancestor finding is collapsed, the two distinct leaves preserved.
func TestGuardSchemaConformance_TwoIndependentViolations(t *testing.T) {
	t.Parallel()
	// name wrong type (int, want string) + port wrong type (string, want int).
	errs := runSchemaGuard(aggWithValues("name", 123, "port", "bad"), t)
	if len(errs) != 2 {
		t.Fatalf("want exactly 2 errors for two independent violations; got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if !errors.Is(e, ErrSchemaConformance) {
			t.Errorf("error %v does not wrap ErrSchemaConformance", e)
		}
	}
}

// --- Positive ------------------------------------------------------------

func TestGuardSchemaConformance_RequiredWithDefaultNotMissing(t *testing.T) {
	t.Parallel()
	// name supplied; count is required BUT defaulted (3) → omitting it must NOT
	// false-positive (default-aware).
	errs := runSchemaGuard(aggWithValues("name", "api"), t)
	if len(errs) != 0 {
		t.Fatalf("required-with-default must not be flagged missing; got %v", errs)
	}
}

func TestGuardSchemaConformance_Conforming(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "port", 8080, "count", 2), t)
	if len(errs) != 0 {
		t.Fatalf("conforming values must pass; got %v", errs)
	}
}

func TestGuardSchemaConformance_CatalogLessSkips(t *testing.T) {
	t.Parallel()
	if errs := guardSchemaConformance(aggWithValues("name", "api"), CatalogLookup{}, nil); errs != nil {
		t.Fatalf("catalog-less path must skip; got %v", errs)
	}
}

func TestGuardSchemaConformance_UnknownTypeSkips(t *testing.T) {
	t.Parallel()
	// A component whose type is absent from the catalog is skipped here (unknown
	// type is reported by other guards), not flagged for conformance.
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "absent/type:1.0.0"})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "demo"}, Spec: types.AggregateSpec{Components: comps}}
	if errs := guardSchemaConformance(agg, schemaComponentCatalog(t), nil); errs != nil {
		t.Fatalf("unknown component type must be skipped; got %v", errs)
	}
}

// requireConformance asserts the errors all wrap ErrSchemaConformance, that
// their count is exactly wantCount (dedup correctness — one error per
// independent violation), and that at least one names the wantFieldPrefix locus.
func requireConformance(t *testing.T, errs []*ResolverError, wantCount int, wantFieldPrefix string) {
	t.Helper()
	if len(errs) != wantCount {
		t.Fatalf("want exactly %d ErrSchemaConformance error(s) at/under %s; got %d: %v", wantCount, wantFieldPrefix, len(errs), errs)
	}
	for _, e := range errs {
		if !errors.Is(e, ErrSchemaConformance) {
			t.Errorf("error %v does not wrap ErrSchemaConformance", e)
		}
	}
	for _, e := range errs {
		if len(e.Field) >= len(wantFieldPrefix) && e.Field[:len(wantFieldPrefix)] == wantFieldPrefix {
			return
		}
	}
	t.Errorf("no error at field %q; got %v", wantFieldPrefix, errs)
}
