// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"go.yaml.in/yaml/v3"
)

// compileTestSchema compiles a compact pillar schema to a compactform.Schema.
func compileTestSchema(t *testing.T, schemaYAML string) compactform.Schema {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(schemaYAML), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	compiled, err := compactform.Compile(&node)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return compiled
}

// catalogFromExtendsChain builds a CatalogLookup whose Component
// "app/svc:1.0.0" is the RESOLVED child of a base that authored the supplied
// pillar schema. The child declares extends and the inheritance marker, exactly
// as an author writes it, and the returned entity is what the real merge
// produces — so a test using this fixture exercises inheritance rather than
// asserting against a hand-built stand-in for it.
func catalogFromExtendsChain(t *testing.T, baseSchemaYAML string) CatalogLookup {
	t.Helper()
	var baseNode, markerNode yaml.Node
	if err := yaml.Unmarshal([]byte(baseSchemaYAML), &baseNode); err != nil {
		t.Fatalf("base schema fixture parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(validator.InheritedSchemaMarker), &markerNode); err != nil {
		t.Fatalf("marker parse: %v", err)
	}
	base := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/base", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Schema: baseNode},
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "app/svc", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "app/base", Schema: markerNode},
	}
	resolved, errs := validator.ResolveComponentExtends(child, func(uri string) (types.Component, bool) {
		if uri == "app/base" {
			return base, true
		}
		return types.Component{}, false
	})
	if len(errs) > 0 {
		t.Fatalf("ResolveComponentExtends: %v", errs)
	}
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "app/svc:1.0.0" {
				return resolved, true
			}
			if typeURI == "app/base:1.0.0" {
				return base, true
			}
			return types.Component{}, false
		},
	}
}

// TestGuardSchemaConformance_InheritedConstraintEnforced is the extends proof
// case: a constraint the BASE declared and the extending Component never
// authored is enforced on the extending Component's instance, because the merge
// copied the base's pillar block onto it. The base requires both `name` and
// `tls_enabled`; the instance supplies only `name`, so it halts. Nothing here
// consults a second, composed view of the Component's schema — there is none.
func TestGuardSchemaConformance_InheritedConstraintEnforced(t *testing.T) {
	t.Parallel()
	cl := catalogFromExtendsChain(t, "values:\n  name: { type: string, required: true }\n  tls_enabled: { type: boolean, required: true }\n")
	// name supplied, tls_enabled (inherited, required, no default) omitted.
	errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, nil, nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values")
}

// TestGuardSchemaConformance_ExtendsPredicateReadsParameter confirms the
// resolved parameter map reaches predicates carried on an INHERITED schema —
// i.e. .parameters-in-predicates is honored on the extends surface, not just
// where the Component authored the predicate itself.
func TestGuardSchemaConformance_ExtendsPredicateReadsParameter(t *testing.T) {
	t.Parallel()
	cl := catalogFromExtendsChain(t, "values:\n  tier:\n    type: string\n    validate: '{{ eq . (param \"allowedTier\") }}'\n    validateMessage: \"tier must equal allowedTier\"\n")
	agg := aggWithValues("tier", "gold")
	if errs := guardSchemaConformance(normalized(t, agg), cl, nil, map[string]any{"allowedTier": "gold"}); len(errs) != 0 {
		t.Fatalf("tier=gold under allowedTier=gold should pass on the inherited schema; got %v", errs)
	}
	errs := guardSchemaConformance(normalized(t, agg), cl, nil, map[string]any{"allowedTier": "silver"})
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values.tier")
}

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
	return guardSchemaConformance(normalized(t, agg), schemaComponentCatalog(t), nil, nil)
}

// aggWithValuesInSubsystem builds an Aggregate with component "svc" inside a
// declared (non-synthesized) subsystem, its values pillar the supplied pairs.
func aggWithValuesInSubsystem(subName string, pairs ...any) *types.Aggregate {
	vals := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		vals.Set(pairs[i].(string), pairs[i+1])
	}
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0", Values: vals})
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set(subName, types.Subsystem{Components: comps})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "demo"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	}
}

// TestGuardSchemaConformance_RealSubsystemRendersQualified is the values-
// conformance accuracy proof: a violation on a component in a DECLARED subsystem
// renders the qualified locator the other guards already use, not the flattened
// `.spec.components.svc` this site emitted before it joined the shared path
// helper. The prefix is qualified — a root render would fail this assertion —
// so this pins the fix, not just the synthesized-default elision.
func TestGuardSchemaConformance_RealSubsystemRendersQualified(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValuesInSubsystem("payments", "name", "api", "port", "not-an-int"), t)
	requireConformance(t, errs, 1, `.spec.subsystems.payments.components["svc"].values.port`)
}

// --- Negative: values pillar ---------------------------------------------

func TestGuardSchemaConformance_ValuesWrongType(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "port", "not-an-int"), t)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values.port")
}

func TestGuardSchemaConformance_ValuesOutOfRange(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "port", 0), t)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values.port")
}

func TestGuardSchemaConformance_ValuesMissingRequiredNonDefaulted(t *testing.T) {
	t.Parallel()
	// name is required with no default; omitting it must halt.
	errs := runSchemaGuard(aggWithValues("port", 8080), t)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values")
}

func TestGuardSchemaConformance_ValuesUnknownProperty(t *testing.T) {
	t.Parallel()
	errs := runSchemaGuard(aggWithValues("name", "api", "bogus", "x"), t)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values")
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
	errs := guardSchemaConformance(normalized(t, aggWithValues("port", 0)), catalog, nil, nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values")
}

// TestGuardSchemaConformance_NestedViolationSingleError pins dedup: a nested
// values.db.port violation produces exactly ONE error, at the deepest path —
// not one per ancestor object along the failure path.
func TestGuardSchemaConformance_NestedViolationSingleError(t *testing.T) {
	t.Parallel()
	db := types.OrderedMap[any]{}
	db.Set("port", 0) // violates db.port minimum 1
	errs := runSchemaGuard(aggWithValues("name", "api", "db", db), t)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values.db.port")
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
		if !errors.Is(e, ErrSchemaConformance()) {
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
	if errs := guardSchemaConformance(normalized(t, agg), catalog, nil, map[string]any{"allowedTier": "gold"}); len(errs) != 0 {
		t.Fatalf("tier=gold under allowedTier=gold should pass; got %v", errs)
	}
	// allowedTier=silver → same value, now violates → exactly one halt.
	errs := guardSchemaConformance(normalized(t, agg), catalog, nil, map[string]any{"allowedTier": "silver"})
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].values.tier")
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
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), CatalogLookup{}, nil, nil); errs != nil {
		t.Fatalf("catalog-less path must skip; got %v", errs)
	}
}

func TestGuardSchemaConformance_RelativePathTypeSkips(t *testing.T) {
	t.Parallel()
	// A genuinely-non-catalog type — a Tier-1 relative-path reference — has no
	// catalog entity and is legitimately skipped (NOT a view inconsistency). The
	// halt arm (a bare catalog ref absent from the view) is in
	// guard_view_consistency_test.go.
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "./local-variant"})
	agg := &types.Aggregate{Metadata: types.AggregateMetadata{Name: "demo"}, Spec: types.AggregateSpec{Components: comps}}
	if errs := guardSchemaConformance(normalized(t, agg), schemaComponentCatalog(t), nil, nil); errs != nil {
		t.Fatalf("relative-path (non-catalog) type must be skipped; got %v", errs)
	}
}

// --- Infra pillar (closed against schema.infra, symmetric with values) ------

// infraCatalog returns a CatalogLookup whose Component "app/svc:1.0.0" declares
//
//	values: name (string, required)
//	infra:  replicas (integer, minimum 1) · tier (string, default "prod", required)
func infraCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const schemaYAML = `values:
  name: { type: string, required: true }
infra:
  replicas: { type: integer, minimum: 1 }
  tier: { type: string, default: prod, required: true }
`
	return catalogFromComponentSchema(t, schemaYAML)
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

// TestGuardInfraConformance_TypoCaught pins the core contract: a key absent from
// schema.infra halts (the pillar is validated closed, symmetric with values).
func TestGuardInfraConformance_TypoCaught(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	bi := infraBoundary("replicas", 2, "replcas", 3)
	errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].infra")
}

// TestGuardInfraConformance_UndeclaredKeyRejected: a key not present in the
// Component's own schema.infra is rejected — the Component schema is the sole
// authoring contract for the infra pillar.
func TestGuardInfraConformance_UndeclaredKeyRejected(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	bi := infraBoundary("replicas", 2, "workers", 4, "tier", "gold")
	errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].infra")
}

// TestGuardInfraConformance_DeclaredKeyAdmitted: keys all present in schema.infra
// validate cleanly.
func TestGuardInfraConformance_DeclaredKeyAdmitted(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	bi := infraBoundary("replicas", 2, "tier", "gold")
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("schema.infra-declared keys must be admitted; got %v", errs)
	}
}

// TestGuardInfraConformance_DeclaredKeyWrongType: a schema.infra-declared key
// supplied with the wrong type halts at that key.
func TestGuardInfraConformance_DeclaredKeyWrongType(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	bi := infraBoundary("replicas", "two", "tier", "gold")
	errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].infra.replicas")
}

// TestGuardInfraConformance_RequiredWithDefaultNotMissing: a required schema.infra
// key carrying a default is satisfied by the default (default-aware).
func TestGuardInfraConformance_RequiredWithDefaultNotMissing(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	bi := infraBoundary("replicas", 2) // tier omitted; defaulted "prod"
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("required-with-default infra key must not be flagged missing; got %v", errs)
	}
}

// TestGuardInfraConformance_ComposedInfraSchemaValidates pins parity with the
// values pillar: a schema.infra composed via a single-document #/$defs $ref is
// RESOLVED to its properties — not skipped. The declared-via-$ref key is
// admitted and a constraint behind the $ref is still enforced.
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

	// The composed infra $ref is resolved (not skipped): the declared-via-$ref
	// key (replicas) is admitted.
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl,
		infraBoundary("replicas", 2), nil); len(errs) != 0 {
		t.Fatalf("composed infra $ref must resolve and admit the declared key; got %v", errs)
	}
	// The constraint behind the $ref is enforced: replicas below the minimum halts.
	// The finding is reported at the pillar root (matching the values-pillar $ref
	// parity), since the violation surfaces through the $ref indirection.
	errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl,
		infraBoundary("replicas", 0), nil)
	requireConformance(t, errs, 1, ".spec.components[\"svc\"].infra")
}

// TestGuardInfraConformance_SkipsWithoutBoundaryInfra: a component with no
// resolved boundary infra is not infra-validated.
func TestGuardInfraConformance_SkipsWithoutBoundaryInfra(t *testing.T) {
	t.Parallel()
	cl := infraCatalog(t)
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, nil, nil); len(errs) != 0 {
		t.Fatalf("no boundary infra → infra validation skipped; got %v", errs)
	}
}

// TestGuardInfraConformance_NoInfraPillarSkips: a Component that declares no
// schema.infra pillar is not infra-validated even when boundary infra is set —
// the component opted into no infra contract.
func TestGuardInfraConformance_NoInfraPillarSkips(t *testing.T) {
	t.Parallel()
	// schemaComponentCatalog declares only a values pillar (no infra).
	cl := schemaComponentCatalog(t)
	bi := infraBoundary("anything", "goes")
	if errs := guardSchemaConformance(normalized(t, aggWithValues("name", "api")), cl, bi, nil); len(errs) != 0 {
		t.Fatalf("component without schema.infra must not be infra-validated; got %v", errs)
	}
}

// --- Declared-sensitive leaves: the value never reaches the diagnostic -----
//
// The validator interpolates the offending instance value into its failure
// text, so a leaf an author declared sensitive would otherwise have its secret
// printed back in a build diagnostic. Every assertion below is paired: a
// withheld message sits beside a NON-sensitive sibling in the SAME call whose
// message must survive byte for byte, because a redaction that never fires is
// indistinguishable from correct behaviour in a test that only looks for the
// placeholder.

const (
	// The plaintext each declared-sensitive leaf carries. No message may
	// contain these.
	withheldValuesSecret = "hunter2-PLAINTEXT"
	withheldNestedSecret = "adm1n-PLAINTEXT"
	withheldInfraSecret  = "ADMIN-PLAINTEXT"

	// The plaintext each non-sensitive sibling carries. Every one must survive
	// in the clear.
	shownValuesPlain = "DEMO_123"
	shownInfraPlain  = "INFRA-IN-THE-CLEAR"
)

// sensitiveLeafSchema declares, in one schema, the three shapes the withholding
// must tell apart: a sensitive leaf beside a plain sibling in values; a
// sensitive OBJECT whose failing child is two levels down; and one leaf name
// declared in BOTH pillars but sensitive in only one.
const sensitiveLeafSchema = `values:
  dbPassword: { type: string, sensitive: true, pattern: "^pg://" }
  appName:    { type: string, pattern: "^[a-z-]+$" }
  creds:
    type: object
    sensitive: true
    properties:
      adminToken: { type: string, minLength: 40 }
  password: { type: string, sensitive: true, minLength: 40 }
infra:
  password: { type: string, minLength: 40 }
`

// plainLeafSchema is sensitiveLeafSchema with every sensitivity declaration
// removed and nothing else changed. It is the control: the messages it produces
// are what an unwithheld diagnostic looks like, so a byte comparison against it
// proves the withholding touches only the declared leaves.
const plainLeafSchema = `values:
  dbPassword: { type: string, pattern: "^pg://" }
  appName:    { type: string, pattern: "^[a-z-]+$" }
  creds:
    type: object
    properties:
      adminToken: { type: string, minLength: 40 }
  password: { type: string, minLength: 40 }
infra:
  password: { type: string, minLength: 40 }
`

// violatingValues is the values instance every leaf of which violates its
// declared constraint, so one call produces a finding on each.
func violatingValues() types.OrderedMap[any] {
	creds := types.OrderedMap[any]{}
	creds.Set("adminToken", withheldNestedSecret)
	vals := types.OrderedMap[any]{}
	vals.Set("dbPassword", withheldValuesSecret)
	vals.Set("appName", shownValuesPlain)
	vals.Set("creds", creds)
	vals.Set("password", "")
	return vals
}

// messagesByField validates one pillar and returns the findings keyed by field
// path, failing when the instance conforms — an empty result would make every
// assertion downstream vacuous.
func messagesByField(t *testing.T, schemaYAML, pillar string, instance types.OrderedMap[any]) map[string]string {
	t.Helper()
	errs := ValidatePillarConformance(compileTestSchema(t, schemaYAML), pillar, instance, nil,
		"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
	if len(errs) == 0 {
		t.Fatalf("the %s fixture must violate its schema; it produced no finding", pillar)
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		out[e.Field] = e.Message
	}
	return out
}

// TestPillarConformance_SensitiveLeafWithheldPlainSiblingUnchanged is the
// discrimination proof. Four findings arrive from one call: three on declared
// leaves, one on a plain sibling. The three carry the placeholder; the sibling's
// message is byte-identical to the same finding produced against a schema that
// declares nothing sensitive.
func TestPillarConformance_SensitiveLeafWithheldPlainSiblingUnchanged(t *testing.T) {
	t.Parallel()
	const prefix = `.spec.components["svc"].values`

	got := messagesByField(t, sensitiveLeafSchema, "values", violatingValues())
	control := messagesByField(t, plainLeafSchema, "values", violatingValues())

	if len(got) != len(control) {
		t.Fatalf("the two schemas must produce the same finding set; got %d vs %d\n got: %v\n control: %v",
			len(got), len(control), got, control)
	}

	// The plain sibling: byte-identical to the control, value included.
	plainField := prefix + ".appName"
	if got[plainField] != control[plainField] {
		t.Errorf("a non-sensitive leaf's message must be unchanged.\n got:     %q\n control: %q", got[plainField], control[plainField])
	}
	if !strings.Contains(got[plainField], shownValuesPlain) {
		t.Errorf("a non-sensitive leaf must still show its offending value; got %q", got[plainField])
	}

	// The declared leaves: withheld, including the child of a sensitive object.
	for _, field := range []string{prefix + ".dbPassword", prefix + ".creds.adminToken", prefix + ".password"} {
		msg, ok := got[field]
		if !ok {
			t.Fatalf("no finding at %q; the fixture drifted. got: %v", field, got)
		}
		if !strings.Contains(msg, redact.Placeholder()) {
			t.Errorf("%s must be withheld; got %q", field, msg)
		}
		if msg == control[field] {
			t.Errorf("%s produced the control's message, so the withholding never fired: %q", field, msg)
		}
	}

	// No message anywhere may reproduce a declared-sensitive value.
	for field, msg := range got {
		for _, secret := range []string{withheldValuesSecret, withheldNestedSecret} {
			if strings.Contains(msg, secret) {
				t.Errorf("%s leaks %q: %s", field, secret, msg)
			}
		}
	}
}

// TestPillarConformance_SensitiveLeafKeepsLocatorAndKeyword pins the
// actionability bound: withholding the constraint text costs the regex, never
// the locator. The reader must still learn which entity, which field, which
// pillar and which contract, plus the keyword the finding carries.
func TestPillarConformance_SensitiveLeafKeepsLocatorAndKeyword(t *testing.T) {
	t.Parallel()
	errs := ValidatePillarConformance(compileTestSchema(t, sensitiveLeafSchema), "values",
		violatingValues(), nil, "Aggregate demo", `.spec.components["svc"]`, "the Component schema")

	var found *ResolverError
	for _, e := range errs {
		if e.Field == `.spec.components["svc"].values.dbPassword` {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("no finding at the sensitive leaf; got %v", errs)
	}
	if !errors.Is(found, ErrSchemaConformance()) {
		t.Errorf("a withheld finding must still wrap ErrSchemaConformance; got %v", found.Err)
	}
	if found.Entity != "Aggregate demo" {
		t.Errorf("entity lost: %q", found.Entity)
	}
	for _, want := range []string{"values pillar", "the Component schema", redact.Placeholder(), "(validation)"} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("a withheld message must retain %q; got %q", want, found.Message)
		}
	}
}

// TestPillarConformance_SensitivityIsPerPillar pins the pillar prefix: `password`
// is one leaf name declared in both pillars and sensitive in only one, so a
// reader of the union of the two pillars' declarations would withhold the
// operator's infra sizing value for no reason. The two pillars are read apart.
func TestPillarConformance_SensitivityIsPerPillar(t *testing.T) {
	t.Parallel()
	infra := types.OrderedMap[any]{}
	infra.Set("password", shownInfraPlain)

	values := messagesByField(t, sensitiveLeafSchema, "values", violatingValues())
	got := messagesByField(t, sensitiveLeafSchema, "infra", infra)

	if msg := values[`.spec.components["svc"].values.password`]; !strings.Contains(msg, redact.Placeholder()) {
		t.Errorf("values.password is declared sensitive and must be withheld; got %q", msg)
	}
	msg := got[`.spec.components["svc"].infra.password`]
	if strings.Contains(msg, redact.Placeholder()) {
		t.Errorf("infra.password is NOT declared sensitive; withholding it means the pillars were unioned: %q", msg)
	}
	if !strings.Contains(msg, shownInfraPlain) {
		t.Errorf("infra.password must still show its offending value; got %q", msg)
	}
}

// TestPillarConformance_NoDeclarationsLeavesEveryMessageUntouched is the
// non-regression floor: a schema declaring nothing sensitive — every schema
// predating the axis — produces exactly the messages it always did.
func TestPillarConformance_NoDeclarationsLeavesEveryMessageUntouched(t *testing.T) {
	t.Parallel()
	for field, msg := range messagesByField(t, plainLeafSchema, "values", violatingValues()) {
		if strings.Contains(msg, redact.Placeholder()) {
			t.Errorf("%s was withheld against a schema that declares nothing sensitive: %q", field, msg)
		}
	}
	infra := types.OrderedMap[any]{}
	infra.Set("password", withheldInfraSecret)
	for field, msg := range messagesByField(t, plainLeafSchema, "infra", infra) {
		if !strings.Contains(msg, withheldInfraSecret) {
			t.Errorf("%s lost its offending value against an undeclared schema: %q", field, msg)
		}
	}
}

// TestFindingOnSensitiveLeaf_PathShapes covers the path arithmetic directly,
// including the shapes an end-to-end fixture cannot easily reach: a finding at
// the pillar root, at the instance root, and under a pillar this entry does not
// validate. Those messages name property keys rather than quoting values, so
// reporting false for them is what keeps a missing-required diagnostic readable.
func TestFindingOnSensitiveLeaf_PathShapes(t *testing.T) {
	t.Parallel()
	declared := SensitiveLeaves{
		Values:   map[string]bool{"dbPassword": true, "creds": true},
		Infra:    map[string]bool{"adminPassword": true},
		Provides: map[string]bool{"host": true},
	}
	for _, tc := range []struct {
		pillar, path string
		want         bool
	}{
		{"values", "values.dbPassword", true},
		{"values", "values.creds.adminToken", true}, // inside a sensitive object
		{"values", "values.creds.a.b.c", true},      // arbitrarily deep inside it
		{"values", "values.appName", false},         // plain sibling
		{"values", "values.dbPasswordExtra", false}, // shares a prefix, is not the leaf
		{"values", "values", false},                 // pillar root
		{"values", "", false},                       // instance root
		{"values", "infra.adminPassword", false},    // other pillar's path
		{"infra", "infra.adminPassword", true},      // same leaf, correct pillar
		{"infra", "infra.dbPassword", false},        // sensitive in values, not here
		{"provides", "provides.host", false},        // not validated through this entry
	} {
		if got := findingOnSensitiveLeaf(declared, tc.pillar, tc.path); got != tc.want {
			t.Errorf("findingOnSensitiveLeaf(%q, %q) = %v, want %v", tc.pillar, tc.path, got, tc.want)
		}
	}
}

// TestPillarConformance_WithholdingFollowsTheDeclarationReader pins that this
// diagnostic surface withholds exactly what the declaration reader collects, and
// does so for free — there is no second walk here, so a declaration the reader
// learns to see is withheld here the same day.
//
// A declaration inside a reference TARGET is one such case: the reader resolves
// the reference, so the value that once reached this diagnostic in plaintext is
// now replaced. The test asserts the replacement rather than the leak, and it
// asserts the key survives, because a diagnostic that dropped the failing locus
// would be worse than one that printed it.
func TestPillarConformance_WithholdingFollowsTheDeclarationReader(t *testing.T) {
	t.Parallel()
	compiled := compileTestSchema(t, `$defs:
  MarkedCred: { type: string, minLength: 40, sensitive: true }
values:
  cred:
    $ref: "#/$defs/MarkedCred"
`)
	if !DeclaredSensitiveLeaves(compiled).Values["cred"] {
		t.Fatalf("a declaration inside the reference target must be collected at the referring path; got %v",
			DeclaredSensitiveLeaves(compiled).Values)
	}
	instance := types.OrderedMap[any]{}
	instance.Set("cred", "INSIDE-REF-PLAINTEXT")
	errs := ValidatePillarConformance(compiled, "values", instance, nil,
		"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
	if len(errs) == 0 {
		t.Fatalf("the fixture must violate its schema; it produced no finding")
	}
	var withheld bool
	for _, e := range errs {
		if strings.Contains(e.Message, "INSIDE-REF-PLAINTEXT") {
			t.Errorf("the declared value still reaches the diagnostic in plaintext: %v", e)
		}
		if strings.Contains(e.Message, redact.Placeholder()) {
			withheld = true
		}
	}
	if !withheld {
		t.Errorf("no finding carries the replacement text; got %v", errs)
	}
}

// TestPillarConformance_WholePillarBehindAReferenceStillEscapes pins the ONE
// shape the declaration reader still does not reach: a pillar whose own node is a
// reference, with no `properties` of its own. The reader locates a pillar's
// declarations through the property map that pillar exposes, so a pillar that
// exposes none has nothing to walk and the declaration inside the target is never
// seen.
//
// It is deliberately an assertion that the leak EXISTS, so the gap is an
// executable fact rather than a rediscovery. The day the reader learns to resolve
// a reference on the pillar node itself, this test fails and whoever closes that
// gap is told this diagnostic surface follows for free. The fix belongs to the
// reader, once, not to a second walk grown here.
func TestPillarConformance_WholePillarBehindAReferenceStillEscapes(t *testing.T) {
	t.Parallel()
	compiled := compileTestSchema(t, `$defs:
  Creds:
    marked: { type: string, minLength: 40, sensitive: true }
values:
  $ref: "#/$defs/Creds"
`)
	if DeclaredSensitiveLeaves(compiled).Any() {
		t.Fatalf("the declaration reader now resolves a reference on the pillar node. Re-point this test at the shape that still escapes it, or delete it — the withholding below follows the reader and needs no separate change.")
	}
	instance := types.OrderedMap[any]{}
	instance.Set("marked", "WHOLE-PILLAR-PLAINTEXT")
	errs := ValidatePillarConformance(compiled, "values", instance, nil,
		"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
	if len(errs) == 0 {
		t.Fatalf("the fixture must violate its schema; it produced no finding")
	}
	var carried bool
	for _, e := range errs {
		if strings.Contains(e.Message, "WHOLE-PILLAR-PLAINTEXT") {
			carried = true
		}
	}
	if !carried {
		t.Errorf("the value no longer reaches the diagnostic. If the reader was widened, update this test; if a second walk was grown here instead, remove it — sensitivity has one home.")
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
		if !errors.Is(e, ErrSchemaConformance()) {
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
