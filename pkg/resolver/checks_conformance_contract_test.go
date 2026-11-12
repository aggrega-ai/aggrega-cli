// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The rules that measure a payload against its declared contract read a contract
// the presentation resolved once. What the tests beside this comment pin is what
// that resolution has to have DONE for the rules to be right about a document:
// an inherited constraint carried onto the extending entity, a reference into the
// entity's own definitions followed, a predicate handed the resolved parameters,
// and a key declared with a default treated as supplied.
//
// None of it is visible in a rule's own declaration — each rule asks the contract
// and reports what it answers — so a resolution that quietly stopped doing any of
// these would leave every rule passing and a document silently unvalidated.

// catalogFromComponentSchema builds a CatalogLookup with one Component
// "app/svc:1.0.0" whose contract is the supplied document.
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

// catalogFromExtendsChain builds a CatalogLookup whose Component
// "app/svc:1.0.0" is the RESOLVED child of a base that authored the supplied
// contract. The child declares extends and the inheritance marker, exactly as an
// author writes it, and the returned entity is what the real merge produces — so
// a test using this fixture exercises inheritance rather than asserting against a
// hand-built stand-in for it.
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

// aggWithValues builds a one-component Aggregate ("svc" of type app/svc:1.0.0)
// whose configuration pillar is the supplied key/value pairs.
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

// configurationFindings asks the configuration-conformance rule over one document
// and returns the locators it raised, in order.
func configurationFindings(
	t *testing.T, agg *types.Aggregate, catalog CatalogLookup, parameters map[string]any,
) []string {
	t.Helper()
	na := normalized(t, agg)
	view := newNormalizedView(na, nil, catalog, parameters)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentValuesConformToTheType)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return locatorsOf(objections)
}

// sizingFindings asks the sizing-conformance rule over one Aggregate document
// and one Release boundary block, and returns the locators it raised.
func sizingFindings(
	t *testing.T, agg *types.Aggregate, catalog CatalogLookup, boundaries string,
) []string {
	t.Helper()
	na := normalized(t, agg)
	release := loadRelease(t, releaseDocument(boundaries))
	view := newNormalizedView(na, release, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentInfraConformsToTheType)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return locatorsOf(objections)
}

// svcSizing is the Release block the sizing rows below write for component
// "svc", indented into the boundary envelope.
func svcSizing(body string) string {
	return `    prod:
      directory: ./output
      components:
        svc:
` + body
}

// TestTheConfigurationRuleEnforcesAConstraintTheBaseEntityDeclared is the
// inheritance proof: a constraint the BASE declared and the extending entity
// never authored is enforced on the extending entity's instance, because the
// merge carried the base's pillar onto it.
//
// The fixture runs the real merge rather than stubbing a second view of the
// contract. A stub would pass whether or not inheritance works.
func TestTheConfigurationRuleEnforcesAConstraintTheBaseEntityDeclared(t *testing.T) {
	t.Parallel()

	catalog := catalogFromExtendsChain(t,
		"values:\n  name: { type: string, required: true }\n  tls_enabled: { type: boolean, required: true }\n")

	// name supplied, tls_enabled (inherited, required, no default) omitted.
	got := configurationFindings(t, aggWithValues("name", "api"), catalog, nil)
	want := []string{".spec.components.svc.values.tls_enabled"}
	if !equalStrings(got, want) {
		t.Errorf("raised sequence = %v; want %v — the inherited required key is enforced on the "+
			"extending entity's instance", got, want)
	}

	// The control: supplying the inherited key satisfies it, so the report above
	// is attributable to that key rather than to the merge producing a contract
	// nothing can satisfy.
	if got := configurationFindings(t, aggWithValues("name", "api", "tls_enabled", true), catalog, nil); len(got) != 0 {
		t.Errorf("an instance supplying every inherited key must pass; got %v", got)
	}
}

// TestTheConfigurationRuleHandsTheResolvedParametersToAPredicate pins that a
// predicate carried on a contract is evaluated against the resolved parameter
// context — on a contract the entity authored itself AND on one it inherited.
//
// Both are asserted because they are separate resolutions: a predicate reaching
// the parameters on the authored surface says nothing about one carried onto the
// entity by the merge, and the inherited case is the one that stops silently.
//
// Each half is stated as a pair over ONE authored value under two parameter sets,
// so what separates the verdicts is the parameter context and nothing else.
func TestTheConfigurationRuleHandsTheResolvedParametersToAPredicate(t *testing.T) {
	t.Parallel()

	const predicateSchema = `values:
  tier:
    type: string
    validate: '{{ eq . (param "allowedTier") }}'
    validateMessage: "tier must equal the allowedTier parameter"
`
	for _, tc := range []struct {
		name    string
		catalog CatalogLookup
	}{
		{"a contract the entity authored", catalogFromComponentSchema(t, predicateSchema)},
		{"a contract the entity inherited", catalogFromExtendsChain(t, predicateSchema)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			agg := aggWithValues("tier", "gold")
			if got := configurationFindings(t, agg, tc.catalog, map[string]any{"allowedTier": "gold"}); len(got) != 0 {
				t.Fatalf("tier=gold under allowedTier=gold must pass; got %v", got)
			}
			got := configurationFindings(t, agg, tc.catalog, map[string]any{"allowedTier": "silver"})
			want := []string{".spec.components.svc.values.tier"}
			if !equalStrings(got, want) {
				t.Errorf("raised sequence = %v; want %v — the same authored value under a "+
					"different parameter set", got, want)
			}
		})
	}
}

// TestTheConformanceRulesFollowAReferenceIntoTheContractsOwnDefinitions pins that
// a pillar composed through a reference into the same document's definitions is
// RESOLVED rather than skipped — on both pillars, because they are compiled by
// separate calls and a reference followed on one says nothing about the other.
//
// A skipped reference is silent: the pillar reads as declaring nothing, every
// instance conforms, and no build changes to say so.
func TestTheConformanceRulesFollowAReferenceIntoTheContractsOwnDefinitions(t *testing.T) {
	t.Parallel()

	t.Run("the configuration pillar", func(t *testing.T) {
		t.Parallel()
		catalog := catalogFromComponentSchema(t, `$defs:
  PortParams:
    port: { type: integer, minimum: 1 }
values:
  $ref: "#/$defs/PortParams"
`)
		if got := configurationFindings(t, aggWithValues("port", 8080), catalog, nil); len(got) != 0 {
			t.Fatalf("a value the referenced definition admits must pass; got %v", got)
		}
		got := configurationFindings(t, aggWithValues("port", 0), catalog, nil)
		want := []string{".spec.components.svc.values.port"}
		if !equalStrings(got, want) {
			t.Errorf("raised sequence = %v; want %v — the constraint behind the reference is "+
				"enforced", got, want)
		}
	})

	t.Run("the sizing pillar", func(t *testing.T) {
		t.Parallel()
		catalog := catalogFromComponentSchema(t, `$defs:
  SizingParams:
    replicas: { type: integer, minimum: 1 }
values:
  name: { type: string, required: true }
infra:
  $ref: "#/$defs/SizingParams"
`)
		agg := aggWithValues("name", "api")
		if got := sizingFindings(t, agg, catalog, svcSizing("          infra:\n            replicas: 2\n")); len(got) != 0 {
			t.Fatalf("a key the referenced definition declares must be admitted; got %v", got)
		}
		got := sizingFindings(t, agg, catalog, svcSizing("          infra:\n            replicas: 0\n"))
		want := []string{".spec.components.svc.infra.replicas"}
		if !equalStrings(got, want) {
			t.Errorf("raised sequence = %v; want %v — the constraint behind the reference is "+
				"enforced", got, want)
		}
	})
}

// TestTheConfigurationRuleReportsOncePerOffendingLeaf pins the report shape a
// nested contract produces: one finding at the deepest key that failed, not one
// per ancestor object along the failure path — and two findings where two
// independent leaves fail, so the collapse of shared ancestors does not also
// collapse distinct faults.
func TestTheConfigurationRuleReportsOncePerOffendingLeaf(t *testing.T) {
	t.Parallel()

	catalog := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
  port: { type: integer, minimum: 1 }
  db:
    port: { type: integer, minimum: 1 }
`)

	nested := types.OrderedMap[any]{}
	nested.Set("port", 0)
	got := configurationFindings(t, aggWithValues("name", "api", "db", nested), catalog, nil)
	want := []string{".spec.components.svc.values.db.port"}
	if !equalStrings(got, want) {
		t.Errorf("a nested violation raised %v; want %v — one finding, at the deepest key", got, want)
	}

	got = configurationFindings(t, aggWithValues("name", 123, "port", "bad"), catalog, nil)
	want = []string{
		".spec.components.svc.values.name",
		".spec.components.svc.values.port",
	}
	if !equalStrings(got, want) {
		t.Errorf("two independent violations raised %v; want %v — the shared ancestor is "+
			"collapsed and the two leaves are not", got, want)
	}
}

// TestTheConformanceRulesTreatADeclaredDefaultAsSupplied pins that a key declared
// required AND carrying a default is satisfied by that default, on both pillars.
//
// Without it the rules report a required key missing on every document that
// simply relies on the default the contract itself declares — which is most of
// them, and the report would be about the contract rather than about the
// document.
func TestTheConformanceRulesTreatADeclaredDefaultAsSupplied(t *testing.T) {
	t.Parallel()

	catalog := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
  count: { type: integer, default: 3, required: true }
infra:
  replicas: { type: integer, minimum: 1 }
  tier: { type: string, default: prod, required: true }
`)
	agg := aggWithValues("name", "api")

	if got := configurationFindings(t, agg, catalog, nil); len(got) != 0 {
		t.Errorf("a required configuration key carrying a default must not be reported missing; got %v", got)
	}
	if got := sizingFindings(t, agg, catalog, svcSizing("          infra:\n            replicas: 2\n")); len(got) != 0 {
		t.Errorf("a required sizing key carrying a default must not be reported missing; got %v", got)
	}

	// The control: the same contract without the default reports, so the two
	// silences above are the default being read rather than the pillar not being
	// measured at all.
	strict := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
  count: { type: integer, required: true }
`)
	got := configurationFindings(t, agg, strict, nil)
	want := []string{".spec.components.svc.values.count"}
	if !equalStrings(got, want) {
		t.Errorf("the same key without a default raised %v; want %v", got, want)
	}
}

// TestTheConformanceRulesMeasureThePillarClosed pins that a key the contract does
// not declare is refused, on both pillars: the entity's contract is the whole
// authoring surface for the pillar, so an undeclared key is a typo rather than an
// extension.
func TestTheConformanceRulesMeasureThePillarClosed(t *testing.T) {
	t.Parallel()

	catalog := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
infra:
  replicas: { type: integer, minimum: 1 }
  tier: { type: string, default: prod, required: true }
`)

	got := configurationFindings(t, aggWithValues("name", "api", "bogus", "x"), catalog, nil)
	want := []string{".spec.components.svc.values.bogus"}
	if !equalStrings(got, want) {
		t.Errorf("an undeclared configuration key raised %v; want %v", got, want)
	}

	agg := aggWithValues("name", "api")
	got = sizingFindings(t, agg, catalog, svcSizing("          infra:\n            replicas: 2\n            replcas: 3\n"))
	want = []string{".spec.components.svc.infra.replcas"}
	if !equalStrings(got, want) {
		t.Errorf("an undeclared sizing key raised %v; want %v", got, want)
	}

	got = sizingFindings(t, agg, catalog, svcSizing("          infra:\n            replicas: two\n"))
	want = []string{".spec.components.svc.infra.replicas"}
	if !equalStrings(got, want) {
		t.Errorf("a declared sizing key of the wrong type raised %v; want %v — at the key, not "+
			"at the pillar", got, want)
	}
}

// TestTheConformanceRulesReachAComponentWhereverTheDocumentDeclaresIt is the
// placement claim, and it is asserted in both directions at each placement.
//
// A rule that stopped reaching a placement reports nothing for it, and reporting
// nothing is what a conforming document also produces — so the passing half is
// the load-bearing one: together they distinguish inspection from omission.
//
// The locator names the placement the author wrote, which differs per placement
// even though the component's identity does not.
func TestTheConformanceRulesReachAComponentWhereverTheDocumentDeclaresIt(t *testing.T) {
	t.Parallel()

	catalog := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
  port: { type: integer, minimum: 1 }
`)

	// placed builds a document declaring component "svc" under the given
	// subsystem nesting, carrying the supplied configuration.
	placed := func(levels []string, pairs ...any) *types.Aggregate {
		vals := types.OrderedMap[any]{}
		for i := 0; i+1 < len(pairs); i += 2 {
			vals.Set(pairs[i].(string), pairs[i+1])
		}
		comps := types.OrderedMap[types.ComponentInstance]{}
		comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0", Values: vals})
		if len(levels) == 0 {
			return &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "demo"},
				Spec:     types.AggregateSpec{Components: comps},
			}
		}
		sub := types.Subsystem{Components: comps}
		for i := len(levels) - 1; i > 0; i-- {
			inner := types.OrderedMap[types.Subsystem]{}
			inner.Set(levels[i], sub)
			sub = types.Subsystem{Subsystems: inner}
		}
		subs := types.OrderedMap[types.Subsystem]{}
		subs.Set(levels[0], sub)
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "demo"},
			Spec:     types.AggregateSpec{Subsystems: subs},
		}
	}

	for _, tc := range []struct {
		name   string
		levels []string
		want   string
	}{
		{"top level", nil, ".spec.components.svc.values.port"},
		{"declared subsystem", []string{"payments"}, ".spec.subsystems.payments.components.svc.values.port"},
		{"nested subsystem", []string{"payments", "inner"},
			".spec.subsystems.payments.subsystems.inner.components.svc.values.port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := configurationFindings(t, placed(tc.levels, "name", "api", "port", "not-an-int"), catalog, nil)
			if !equalStrings(got, []string{tc.want}) {
				t.Errorf("raised sequence = %v; want [%s]", got, tc.want)
			}
			if got := configurationFindings(t, placed(tc.levels, "name", "api", "port", 8080), catalog, nil); len(got) != 0 {
				t.Errorf("a conforming component at this placement must pass; got %v", got)
			}
		})
	}
}

// TestARelativePathTypeEndsTheConformanceRuleAndIsReportedAsAbsent pins how the
// two rules treat a relative-path type. The conformance rule ends on it: it
// addresses no catalog entity, so there is no contract to measure its values
// against. The catalog-entry rule reports it as an absent catalog identity,
// because a type is never read as a file — the same report a bare component
// with such a type receives.
//
// The sibling of a catalog type sits beside it in the same document, so each
// rule is proven against a document holding both: the conformance rule reports
// only the sibling, and the catalog-entry rule reports only the relative-path
// instance.
func TestARelativePathTypeEndsTheConformanceRuleAndIsReportedAsAbsent(t *testing.T) {
	t.Parallel()

	catalog := catalogFromComponentSchema(t, `values:
  name: { type: string, required: true }
`)
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("local", types.ComponentInstance{Type: "./local-variant"})
	comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "demo"},
		Spec:     types.AggregateSpec{Components: comps},
	}

	got := configurationFindings(t, agg, catalog, nil)
	want := []string{".spec.components.svc.values.name"}
	if !equalStrings(got, want) {
		t.Errorf("the conformance rule raised %v; want %v — the relative-path type has no "+
			"contract to measure against", got, want)
	}

	na := normalized(t, agg)
	view := newNormalizedView(na, nil, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	wantAbsent := []string{".spec.components.local"}
	if got := locatorsOf(objections); !equalStrings(got, wantAbsent) {
		t.Errorf("the catalog-entry rule raised %v; want %v — the relative-path type is an absent catalog "+
			"identity, and its catalog-typed sibling resolves", got, wantAbsent)
	}
}
