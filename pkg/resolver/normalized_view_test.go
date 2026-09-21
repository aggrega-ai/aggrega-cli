// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// componentTypeURI is the one type every fixture below declares, so a test that
// swaps what the catalog answers for it changes only the answer.
const componentTypeURI = "app/svc:1.0.0"

// schemaComponent builds a catalog Component carrying the supplied authored
// schema document verbatim.
func schemaComponent(t *testing.T, name, schemaYAML string) types.Component {
	t.Helper()
	comp := types.Component{
		Metadata: types.ComponentMetadata{Name: name, Version: "1.0.0"},
	}
	if schemaYAML != "" {
		if err := yaml.Unmarshal([]byte(schemaYAML), &comp.Spec.Schema); err != nil {
			t.Fatalf("schema fixture parse: %v", err)
		}
	}
	return comp
}

// singleTypeCatalog answers one component type and nothing else.
func singleTypeCatalog(comp types.Component) CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == componentTypeURI {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// oneInstanceAggregate is an Aggregate declaring one component of the fixture
// type, carrying the supplied authored values.
func oneInstanceAggregate(t *testing.T, name string, values types.OrderedMap[any]) types.NormalizedAggregate {
	t.Helper()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set(name, types.ComponentInstance{Type: componentTypeURI, Values: values})
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cross-check"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

// ruleFindings asks the configuration-conformance rule over one presentation and
// returns its objections. The rule is reached through the framework's traversal
// rather than by calling its predicate, so what is measured is what the suite
// asks in production.
func ruleFindings(t *testing.T, view *normalizedView) []constraint.Objection {
	t.Helper()
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentValuesConformToTheType)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return objections
}

// TestTheRuleReadsTheResolvedContractRatherThanResolvingItAgain is the
// production-once property, observed rather than asserted.
//
// The presentation is built against one authored contract and the catalog is
// then made to answer with a different one. A reader that resolves the contract
// for itself would measure against the second; a reader that reads the resolved
// fact measures against the first. The two contracts disagree about the same
// instance — one refuses it and the other accepts it — so the verdict alone
// separates the two implementations, with no counter to instrument and nothing
// about the mechanism assumed.
//
// INVARIANT: a derived contract is resolved once, at the point its derivation
// completes, and every reader downstream reads that result.
// CONSEQUENCE: were this to fail, each rule would resolve the same contract
// again — multiplying the work by the number of readers, and giving each of them
// its own chance to read a resolution failure as a type that declares nothing.
func TestTheRuleReadsTheResolvedContractRatherThanResolvingItAgain(t *testing.T) {
	t.Parallel()

	// The first contract requires a key the instance does not write; the second
	// requires nothing. The instance is refused under the first and accepted
	// under the second.
	refusing := schemaComponent(t, "app/svc", "values:\n  size:\n    type: string\n    required: true\n")
	accepting := schemaComponent(t, "app/svc", "values: {}\n")

	na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})

	answered := refusing
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == componentTypeURI {
				return answered, true
			}
			return types.Component{}, false
		},
	}

	// Establish that the two contracts really do disagree about this instance,
	// so a passing verdict below cannot come from a pair that agrees.
	view := newNormalizedView(na, nil, catalog, nil)
	underRefusing := ruleFindings(t, view)
	if len(underRefusing) == 0 {
		t.Fatalf("the first contract accepted an instance that omits a key it declares required.\n" +
			"INVARIANT: the two fixtures must disagree about this instance for the swap below to " +
			"separate anything.\n" +
			"CONSEQUENCE: without the disagreement the assertion that follows passes whatever the " +
			"guard reads, and proves nothing.")
	}
	answered = accepting
	underAccepting := ruleFindings(t, newNormalizedView(na, nil, catalog, nil))
	if len(underAccepting) != 0 {
		t.Fatalf("the second contract refused an instance it declares nothing about; got %v.\n"+
			"INVARIANT: the two fixtures must disagree about this instance.\n"+
			"CONSEQUENCE: without the disagreement the assertion that follows proves nothing.",
			underAccepting)
	}

	// The catalog now answers with the accepting contract, and the presentation
	// still holds the refusing one it resolved. A guard reading the resolved
	// fact refuses; a guard resolving again accepts.
	got := ruleFindings(t, view)
	if len(got) == 0 {
		t.Fatalf("the rule raised nothing after the catalog was made to answer with a different " +
			"contract.\n" +
			"INVARIANT: the rule reads the contract resolved when the view was built.\n" +
			"CONSEQUENCE: raising nothing here means the contract is resolved per reader rather " +
			"than once per build, and each reader gets its own chance to read a resolution " +
			"failure as a type that declares nothing.")
	}
	if !errors.Is(got[0].Sentinel(), ErrSchemaConformance()) {
		t.Errorf("the finding carries %v; want the conformance rule.\n"+
			"INVARIANT: the rule that measures a pillar against its declared contract is identified "+
			"by its own sentinel.\n"+
			"CONSEQUENCE: a caller matching on the sentinel stops seeing this refusal.", got[0])
	}
}

// TestAContractThatFailsToResolveIsNotABareAbsence pins the three outcomes the
// producer keeps apart, and the reason it is worth pinning is that two of them
// answer the accessor identically.
//
// A type declaring no contract and a type whose declared contract could not be
// resolved both read as "no contract to measure against" — which is correct for
// the first and is a diagnostic that stopped firing for the second. The producer
// is the one place that can still tell them apart, and the third case here is
// the one the distinction must not collapse onto: a contract that resolved to
// nothing, which reads as present.
//
// INVARIANT: a failed resolution is a distinguishable outcome at the producer,
// never a bare not-ok shared with an absent contract.
// CONSEQUENCE: were this to fail, a component whose contract uses a
// cross-document reference the resolution could not follow would silently stop
// being validated, with no build changing to say so.
func TestAContractThatFailsToResolveIsNotABareAbsence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// schema is the authored contract document, empty when the type
		// declares none.
		schema string
		// wantResolved is what the fact answers with its second result.
		wantResolved bool
		// wantFailure is whether the producer names this type as one whose
		// declared contract did not resolve.
		wantFailure bool
	}{
		{
			name:         "no contract declared",
			schema:       "",
			wantResolved: false,
			wantFailure:  false,
		},
		{
			name:         "contract resolves to nothing",
			schema:       "values: {}\n",
			wantResolved: true,
			wantFailure:  false,
		},
		{
			name: "declared contract does not resolve",
			// A reference into a library this build supplies no entry for. It
			// is the shape the trap is about: the resolution needs documents
			// the caller has to supply, and the compile entry reports its
			// absence with the same not-ok an unauthored contract returns.
			schema:       "values:\n  $ref: \"AbsentLibrary#/schemas/Size\"\n",
			wantResolved: false,
			wantFailure:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			catalog := singleTypeCatalog(schemaComponent(t, "app/svc", tc.schema))
			na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})
			view := newNormalizedView(na, nil, catalog, nil)

			schema, resolved := view.CompiledComponentSchema(componentTypeURI)
			if resolved != tc.wantResolved {
				t.Fatalf("the resolved-contract fact answered %v; want %v.\n"+
					"INVARIANT: the second result reports whether there is a contract to read.\n"+
					"CONSEQUENCE: a rule over this fact measures against a contract that is not "+
					"there, or skips one that is.", resolved, tc.wantResolved)
			}
			if resolved && schema == nil {
				t.Errorf("the fact reported a contract and handed back nothing.\n" +
					"INVARIANT: a contract that resolves to nothing is an empty document, not an " +
					"absent one.\n" +
					"CONSEQUENCE: a successful resolution becomes indistinguishable from a failed " +
					"one, which is the distinction this producer exists to keep.")
			}

			failures := view.UnresolvableContracts()
			named := false
			for _, f := range failures {
				if f.ComponentType == componentTypeURI && f.Socket == "" {
					named = true
					if f.Err == nil {
						t.Errorf("the producer named a failed resolution and carried no reason.\n" +
							"INVARIANT: a failure is reported where the resolving happens, with " +
							"what went wrong.\n" +
							"CONSEQUENCE: the one site that can say why says nothing, and every " +
							"reader downstream sees a bare absence.")
					}
				}
			}
			if named != tc.wantFailure {
				t.Fatalf("the producer named this type as unresolvable: %v; want %v (failures: %v).\n"+
					"INVARIANT: the producer separates a contract that failed to resolve from a "+
					"type that declares none, even though the fact answers both the same way.\n"+
					"CONSEQUENCE: if the two are merged, a resolution failure is reported nowhere "+
					"and reads exactly like a component with nothing to validate.",
					named, tc.wantFailure, failures)
			}
		})
	}
}

// TestAnUnresolvableContractIsNotSmuggledIntoTheRule is the other half of the
// policy above, and it is asserted rather than left implicit because the obvious
// reading of "report it where it happens" is to make the rule do the reporting.
//
// A build carrying such a contract DOES halt — the refusal is asserted, with its
// diagnostic, in guards_contract_compile_test.go. What is asserted here is that
// the refusal is not the rule's: the rule reads the resolved-contract fact, that
// fact answers a failed resolution exactly as it answers an absent contract, and
// a rule that started raising on it would be reporting a distinction its own
// input cannot carry.
//
// INVARIANT: the conformance rule ends on a contract that did not resolve, and the
// refusal is composed where the classification lives.
// CONSEQUENCE: were this to fail, the rule would have grown a second reading of the
// classification — so one condition would be reported from two places, free to
// disagree about which subjects it covers, and the rule would be raising on an
// absence it cannot tell apart from a type that declares nothing.
func TestAnUnresolvableContractIsNotSmuggledIntoTheRule(t *testing.T) {
	t.Parallel()

	catalog := singleTypeCatalog(schemaComponent(t, "app/svc",
		"values:\n  $ref: \"AbsentLibrary#/schemas/Size\"\n"))
	na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})
	view := newNormalizedView(na, nil, catalog, nil)

	if len(view.UnresolvableContracts()) == 0 {
		t.Fatalf("the fixture's contract resolved; this case has nothing to measure.\n" +
			"INVARIANT: the fixture must reach the failed-resolution outcome.\n" +
			"CONSEQUENCE: the assertion below passes for the wrong reason.")
	}
	if errs := ruleFindings(t, view); len(errs) != 0 {
		t.Fatalf("the conformance rule raised %v on a contract that did not resolve.\n"+
			"INVARIANT: the rule ends there; the refusal is composed from the classification "+
			"the resolution kept, not re-derived inside a rule.\n"+
			"CONSEQUENCE: one condition acquires two reporting sites, which are then free to "+
			"disagree about which subjects it covers.", errs)
	}
}

// ---------------------------------------------------------------- coordinates

// The coordinates the rules are invoked once per come from the same derivation
// production uses, rather than from a second one written here.
//
// A test-local derivation would be a second answer to "which coordinates does
// this document declare", and the two would agree on the day they were written
// with nothing comparing them afterwards.

// TestTheRulesReportEachFaultOfOneDocumentExactlyOnce holds the rules over ONE
// authored document that violates three of them at once, and pins what each
// contributes.
//
// 🔴 It is per rule rather than a total, and that is the point: a total says
// nothing about which rules produced it, so a document that stopped exercising
// one rule would still satisfy a count while that rule's coverage had gone. Each
// rule's own expectation is what makes a lost one visible.
func TestTheRulesReportEachFaultOfOneDocumentExactlyOnce(t *testing.T) {
	t.Parallel()

	// The type declares a required key, publishes one key, and declares one
	// dependency socket. The instance violates all three rules at once and each
	// violation is a different rule, so no rule's verdict can stand in for
	// another's.
	catalog := singleTypeCatalog(schemaComponent(t, "app/svc",
		"values:\n  size:\n    type: string\n    required: true\nprovides:\n  host:\n    type: string\n"))

	values := types.OrderedMap[any]{}
	inject := &types.InjectBlock{}
	env := types.OrderedMap[any]{}
	entry := types.OrderedMap[any]{}
	entry.Set("from", "not_published")
	env.Set("SVC_HOST", &entry)
	inject.Env = &env

	edgeInject := &types.InjectBlock{}
	edgeEnv := types.OrderedMap[any]{}
	edgeEnv.Set("PEER_HOST", "also_not_published")
	edgeInject.Env = &edgeEnv
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("peer", types.VerboseUsesEntry{Target: "peer", Inject: edgeInject})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("backend", types.UsesSocketValue{Verbose: verbose})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("svc", types.ComponentInstance{
		Type: componentTypeURI, Values: values, Inject: inject, Uses: uses,
	})
	components.Set("peer", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cross-check"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, catalog, nil)
	coordinates := newCoordinateSet(view)

	configuration, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, componentValuesConformToTheType)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	sizing, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, coordinates, view, componentInfraConformsToTheType)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	selfInject, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, coordinates, coordinates, coordinates, view, selfInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the delivery-key traversal returned an error: %v", err)
	}
	connectionInject, err := constraint.RunOnEdges[constraint.NormalizedView](
		nil, coordinates, view, connectionInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the edge traversal returned an error: %v", err)
	}

	for _, tc := range []struct {
		rule       string
		objections []constraint.Objection
		want       []string
	}{
		{
			rule:       "the configuration-conformance rule",
			objections: configuration,
			// Both instances omit the key the contract declares required, and the
			// components come back in instance-name order.
			want: []string{".spec.components.peer.values.size", ".spec.components.svc.values.size"},
		},
		{
			rule:       "the sizing-conformance rule",
			objections: sizing,
			// No Release document, so nothing wrote sizing to measure.
			want: nil,
		},
		{
			rule:       "the self-delivery reference rule",
			objections: selfInject,
			want:       []string{".spec.components.svc.inject.env.SVC_HOST"},
		},
		{
			rule:       "the connection delivery reference rule",
			objections: connectionInject,
			want:       []string{".spec.components.svc.uses.backend.peer"},
		},
	} {
		if got := locatorsOf(tc.objections); !equalStrings(got, tc.want) {
			t.Errorf("%s raised %v; want %v.\n"+
				"INVARIANT: each rule reports its own fault of this document, once.\n"+
				"CONSEQUENCE: a rule reporting less has stopped being asked about a coordinate "+
				"this document presents, and the loss is invisible from any total.",
				tc.rule, got, tc.want)
		}
	}
}

// TestTheResolvedContractIsTheSameDocumentTheCompileEntryProduces holds the
// presentation to the compile entry rather than to a fixture of its own, so the
// two cannot come to answer differently for one authored contract.
//
// INVARIANT: the resolved fact is what compiling the authored contract produces.
// CONSEQUENCE: were this to fail, the fact and the entry would be two readings
// of one contract, and a rule reading the fact would enforce something the entry
// never produced.
func TestTheResolvedContractIsTheSameDocumentTheCompileEntryProduces(t *testing.T) {
	t.Parallel()

	catalog := singleTypeCatalog(schemaComponent(t, "app/svc",
		"values:\n  size:\n    type: string\n"))
	na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})
	view := newNormalizedView(na, nil, catalog, nil)

	fromFact, factOK := view.CompiledComponentSchema(componentTypeURI)
	fromEntry, entryOK := CompileComponentSchema(catalog, componentTypeURI)
	if factOK != entryOK {
		t.Fatalf("the fact reported %v and the compile entry reported %v.\n"+
			"INVARIANT: both read one authored contract.\n"+
			"CONSEQUENCE: a rule over the fact and a reader of the entry disagree about whether "+
			"there is a contract at all.", factOK, entryOK)
	}
	if !factOK {
		t.Fatalf("neither reported a contract for a type that declares one.\n" +
			"INVARIANT: the fixture declares a contract.\n" +
			"CONSEQUENCE: the comparison below is over two absent values.")
	}
	if !sameSchema(fromFact, fromEntry) {
		t.Errorf("the fact resolved to %v and the compile entry to %v.\n"+
			"INVARIANT: the fact IS what the entry produces, resolved once rather than per reader.\n"+
			"CONSEQUENCE: the two become independent readings of one contract, free to drift.",
			fromFact, fromEntry)
	}
}

// sameSchema compares two resolved contracts by their serialized form, which is
// the comparison a resolved document admits: it holds no order, so comparing it
// any other way would compare map iteration.
func sameSchema(a, b compactform.Schema) bool {
	left, lerr := yaml.Marshal(a)
	right, rerr := yaml.Marshal(b)
	if lerr != nil || rerr != nil {
		return false
	}
	return string(left) == string(right)
}
