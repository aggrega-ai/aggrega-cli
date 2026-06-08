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
	return guardSchemaConformance(agg, schemaComponentCatalog(t), nil, nil)
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
	errs := guardSchemaConformance(aggWithValues("port", 0), catalog, nil, nil)
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

// TestGuardSchemaConformance_PredicateReadsParameter is the end-to-end proof
// 10-pre3 deferred: a schema.values predicate that reads a resolved parameter
// (via param) gates the instance — legal under one parameter set, an
// ErrSchemaConformance halt under another, the same authored value either way.
func TestGuardSchemaConformance_PredicateReadsParameter(t *testing.T) {
	t.Parallel()
	const schemaYAML = `values:
  tier:
    type: string
    validate: '{{ eq . (param "allowedTier") }}'
    validateMessage: "tier must equal the allowedTier parameter"
`
	catalog := catalogFromComponentSchema(t, schemaYAML)
	agg := aggWithValues("tier", "gold")

	// allowedTier=gold → conforms.
	if errs := guardSchemaConformance(agg, catalog, nil, map[string]any{"allowedTier": "gold"}); len(errs) != 0 {
		t.Fatalf("tier=gold under allowedTier=gold should pass; got %v", errs)
	}
	// allowedTier=silver → same value, now violates → exactly one halt.
	errs := guardSchemaConformance(agg, catalog, nil, map[string]any{"allowedTier": "silver"})
	requireConformance(t, errs, 1, ".spec.components.svc.values.tier")
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
	if errs := guardSchemaConformance(aggWithValues("name", "api"), CatalogLookup{}, nil, nil); errs != nil {
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
	if errs := guardSchemaConformance(agg, schemaComponentCatalog(t), nil, nil); errs != nil {
		t.Fatalf("unknown component type must be skipped; got %v", errs)
	}
}

// --- Infra pillar (union of schema.infra and provider baseSchema) -----------

// providerBaseWithWorkers is a Provider baseSchema (authored as a YAML
// JSON-Schema) declaring a provider-layered key `workers` plus an output-only
// `image` it marks required. The infra union must admit `workers`, must NOT
// carry `required: [image]`, and must drop the baseSchema's open
// additionalProperties.
const providerBaseWithWorkers = `type: object
properties:
  image: { type: string }
  workers: { type: integer, minimum: 1 }
required: [image]
additionalProperties: true
`

// infraCatalog returns a CatalogLookup whose Component "app/svc:1.0.0" declares
//
//	values: name (string, required)
//	infra:  replicas (integer, minimum 1) · tier (string, default "prod", required)
//
// and a ProviderInfraSchema resolver returning providerBase (or no provider when
// providerBase is empty).
func infraCatalog(t *testing.T, providerBase string) CatalogLookup {
	t.Helper()
	const schemaYAML = `values:
  name: { type: string, required: true }
infra:
  replicas: { type: integer, minimum: 1 }
  tier: { type: string, default: prod, required: true }
`
	cl := catalogFromComponentSchema(t, schemaYAML)
	cl.ProviderInfraSchema = func(_, _ string) (string, bool) {
		if providerBase == "" {
			return "", false
		}
		return providerBase, true
	}
	return cl
}

// infraBoundary builds a per-component BoundaryInfra map for instance "svc"
// whose resolved infra is the supplied key/value pairs.
func infraBoundary(pairs ...any) map[string]*BoundaryInfra {
	infra := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		infra.Set(pairs[i].(string), pairs[i+1])
	}
	return map[string]*BoundaryInfra{"svc": {Infra: infra}}
}

// TestGuardInfraConformance_TypoCaught pins the core contract: a key in neither
// schema.infra nor the provider baseSchema halts (the union is closed).
func TestGuardInfraConformance_TypoCaught(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	bi := infraBoundary("replicas", 2, "replcas", 3)
	errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components.svc.infra")
}

// TestGuardInfraConformance_ProviderKeyAdmitted: a provider-layered key present
// in the baseSchema but not in schema.infra is admitted.
func TestGuardInfraConformance_ProviderKeyAdmitted(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	bi := infraBoundary("replicas", 2, "workers", 4, "tier", "gold")
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("provider-declared infra key must be admitted; got %v", errs)
	}
}

// TestGuardInfraConformance_DeclaredKeyWrongType: a schema.infra-declared key
// supplied with the wrong type halts at that key (declared-key constraints are
// still enforced through the union).
func TestGuardInfraConformance_DeclaredKeyWrongType(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	bi := infraBoundary("replicas", "two", "tier", "gold")
	errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components.svc.infra.replicas")
}

// TestGuardInfraConformance_RequiredWithDefaultNotMissing: a required schema.infra
// key carrying a default is satisfied by the default (default-aware).
func TestGuardInfraConformance_RequiredWithDefaultNotMissing(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	bi := infraBoundary("replicas", 2) // tier omitted; defaulted "prod"
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("required-with-default infra key must not be flagged missing; got %v", errs)
	}
}

// TestGuardInfraConformance_ProviderRequiredNotCarried pins the load-bearing
// construction choice: the provider baseSchema's own required (an output-only
// key like image) is NOT imposed on operator infra.
func TestGuardInfraConformance_ProviderRequiredNotCarried(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	bi := infraBoundary("replicas", 2, "tier", "gold") // image omitted
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("provider baseSchema required must not be carried into the infra union; got %v", errs)
	}
}

// TestGuardInfraConformance_ComposedInfraSchemaValidates pins parity with the
// values pillar: a schema.infra composed via a single-document #/$defs $ref is
// RESOLVED to its properties and merged with the provider keys — not skipped.
// The declared-via-$ref key and a provider key are admitted, and a constraint
// behind the $ref is still enforced.
func TestGuardInfraConformance_ComposedInfraSchemaValidates(t *testing.T) {
	t.Parallel()
	const composedSchema = `$defs:
  SizingParams:
    replicas: { type: integer, minimum: 1 }
values:
  name: { type: string, required: true }
infra:
  $ref: "#/$defs/SizingParams"
`
	cl := catalogFromComponentSchema(t, composedSchema)
	cl.ProviderInfraSchema = func(_, _ string) (string, bool) { return providerBaseWithWorkers, true }

	// The composed infra $ref is resolved (not skipped): the declared-via-$ref
	// key (replicas) and the provider key (workers) are both admitted.
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl,
		infraBoundary("replicas", 2, "workers", 4), nil); len(errs) != 0 {
		t.Fatalf("composed infra $ref must resolve and admit declared + provider keys; got %v", errs)
	}
	// The constraint behind the $ref is enforced: replicas below the minimum halts.
	errs := guardSchemaConformance(aggWithValues("name", "api"), cl,
		infraBoundary("replicas", 0), nil)
	requireConformance(t, errs, 1, ".spec.components.svc.infra.replicas")
}

// TestGuardInfraConformance_SkipsWithoutBoundaryInfra: a component with no
// resolved boundary infra is not infra-validated.
func TestGuardInfraConformance_SkipsWithoutBoundaryInfra(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, providerBaseWithWorkers)
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, nil, nil); len(errs) != 0 {
		t.Fatalf("no boundary infra → infra validation skipped; got %v", errs)
	}
}

// TestGuardInfraConformance_SkipsWhenProviderUnresolvable: with no resolvable
// provider the union cannot be formed, so infra is skipped gracefully rather
// than closed against schema.infra alone (which would false-flag a legitimate
// provider-layered key). Here even an apparent typo is left unvalidated.
func TestGuardInfraConformance_SkipsWhenProviderUnresolvable(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t, "") // ProviderInfraSchema returns ("", false) → unresolvable
	bi := infraBoundary("replicas", 2, "replcas", 3)
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("unresolvable provider → infra validation skipped; got %v", errs)
	}
}

// TestGuardInfraConformance_ResolvedProviderNoKeysClosesInfraOnly: a provider
// that resolves but declares no infra keys (open baseSchema) still validates —
// the union degenerates to the Component schema.infra, closed — so a typo of a
// declared key halts and a key the schema.infra does not declare is rejected.
func TestGuardInfraConformance_ResolvedProviderNoKeysClosesInfraOnly(t *testing.T) {
	t.Parallel()
	const openBaseSchema = "type: object\nadditionalProperties: true\n"
	cl := infraCatalog(t, openBaseSchema) // resolves, but no properties
	bi := infraBoundary("replicas", 2, "replcas", 3)
	errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components.svc.infra")
}

// TestGuardInfraConformance_NoInfraPillarSkips: a Component that declares no
// schema.infra pillar is not infra-validated even when a provider resolves and
// boundary infra is set — the component opted into no infra contract.
func TestGuardInfraConformance_NoInfraPillarSkips(t *testing.T) {
	t.Parallel()
	// schemaComponentCatalog declares only a values pillar (no infra); attach a
	// resolvable provider so the skip is attributable solely to the absent
	// schema.infra pillar.
	cl := schemaComponentCatalog(t)
	cl.ProviderInfraSchema = func(_, _ string) (string, bool) { return providerBaseWithWorkers, true }
	bi := infraBoundary("anything", "goes")
	if errs := guardSchemaConformance(aggWithValues("name", "api"), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("component without schema.infra must not be infra-validated; got %v", errs)
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
