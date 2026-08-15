// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"sort"
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

// oneInstanceAggregate is an application declaring one component of the fixture
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

// TestTheGuardReadsTheResolvedContractRatherThanResolvingItAgain is the
// production-once property, observed rather than asserted.
//
// The presentation is built against one authored contract and the catalog is
// then made to answer with a different one. A guard that resolves the contract
// for itself would measure against the second; a guard that reads the resolved
// fact measures against the first. The two contracts disagree about the same
// instance — one refuses it and the other accepts it — so the verdict alone
// separates the two implementations, with no counter to instrument and nothing
// about the mechanism assumed.
//
// INVARIANT: a derived contract is resolved once, at the point its derivation
// completes, and every reader downstream reads that result.
// CONSEQUENCE: were this to fail, each guard would resolve the same contract
// again — multiplying the work by the number of readers, and giving each of them
// its own chance to read a resolution failure as a type that declares nothing.
func TestTheGuardReadsTheResolvedContractRatherThanResolvingItAgain(t *testing.T) {
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
	underRefusing := guardSchemaConformance(na, catalog, nil, nil, view)
	if len(underRefusing) == 0 {
		t.Fatalf("the first contract accepted an instance that omits a key it declares required.\n" +
			"INVARIANT: the two fixtures must disagree about this instance for the swap below to " +
			"separate anything.\n" +
			"CONSEQUENCE: without the disagreement the assertion that follows passes whatever the " +
			"guard reads, and proves nothing.")
	}
	answered = accepting
	underAccepting := guardSchemaConformance(na, catalog, nil, nil, newNormalizedView(na, nil, catalog, nil))
	if len(underAccepting) != 0 {
		t.Fatalf("the second contract refused an instance it declares nothing about; got %v.\n"+
			"INVARIANT: the two fixtures must disagree about this instance.\n"+
			"CONSEQUENCE: without the disagreement the assertion that follows proves nothing.",
			underAccepting)
	}

	// The catalog now answers with the accepting contract, and the presentation
	// still holds the refusing one it resolved. A guard reading the resolved
	// fact refuses; a guard resolving again accepts.
	got := guardSchemaConformance(na, catalog, nil, nil, view)
	if len(got) == 0 {
		t.Fatalf("the guard raised nothing after the catalog was made to answer with a different " +
			"contract.\n" +
			"INVARIANT: the guard reads the contract resolved when the presentation was built.\n" +
			"CONSEQUENCE: raising nothing here means the guard resolved the contract for itself, " +
			"so the derived artefact is produced once per reader rather than once per build.")
	}
	if !errors.Is(got[0], ErrSchemaConformance()) {
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

// TestAnUnresolvableContractDoesNotChangeTheBuildVerdict is the other half of
// the policy above, and it is asserted rather than left implicit because the
// obvious reading of "report it where it happens" is to halt there.
//
// INVARIANT: naming a failed resolution does not refuse a build that is accepted
// today.
// CONSEQUENCE: were this to fail, the producer would start rejecting an input
// class nobody has measured the reachability of — which is a refusal decided by
// an implementation detail rather than by a rule.
func TestAnUnresolvableContractDoesNotChangeTheBuildVerdict(t *testing.T) {
	t.Parallel()

	catalog := singleTypeCatalog(schemaComponent(t, "app/svc",
		"values:\n  $ref: \"AbsentLibrary#/schemas/Size\"\n"))
	na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})
	view := newNormalizedView(na, nil, catalog, nil)

	if len(view.UnresolvableContracts()) == 0 {
		t.Fatalf("the fixture's contract resolved; this case has nothing to measure.\n" +
			"INVARIANT: the fixture must reach the failed-resolution outcome.\n" +
			"CONSEQUENCE: the verdict assertion below passes for the wrong reason.")
	}
	if errs := guardSchemaConformance(na, catalog, nil, nil, view); len(errs) != 0 {
		t.Fatalf("a contract that failed to resolve halted the build with %v.\n"+
			"INVARIANT: the failure is a distinguishable value, not a refusal.\n"+
			"CONSEQUENCE: a build that is accepted today starts failing, on an input class whose "+
			"reachability from an authored document nobody has established.", errs)
	}
}

// ---------------------------------------------------------------- the cross-check

// nodeSet, edgeSet, mechanismSet and deliveryKeySet enumerate the coordinates
// the declared rules are invoked once per.
//
// They are here rather than beside the presentation deliberately. Nothing in
// production invokes a declared rule at this stage, so an enumerator shipped
// beside the presentation would be production code with no consumer — and an
// enumerator that yields a plausible wrong coordinate set is silent, where an
// absent one is loud.
type nodeSet struct{ nodes []constraint.NodeInstance }

func (n nodeSet) Nodes() []constraint.NodeInstance { return n.nodes }

type edgeSet struct{ edges []constraint.EdgeInstance }

func (e edgeSet) Edges() []constraint.EdgeInstance { return e.edges }

type mechanismSet struct{ view *normalizedView }

func (m mechanismSet) Mechanisms(node string) []string {
	return m.view.AuthoredInjectMechanismNames(node)
}

type deliveryKeySet struct{ view *normalizedView }

func (k deliveryKeySet) DeliveryKeys(node, mechanism string) []string {
	return k.view.AuthoredInjectDeliveryKeys(node, mechanism)
}

// coordinatesOf derives the node and edge sets an application declares, read
// through the presentation rather than off the documents, so the walk and the
// facts cannot disagree about which coordinates exist.
func coordinatesOf(na types.NormalizedAggregate, view *normalizedView) (nodeSet, edgeSet) {
	var nodes nodeSet
	var edges edgeSet
	for _, p := range collectComponentInstances(na) {
		node := constraint.NodeInstance{
			ID:                   p.name,
			AuthoredName:         p.name,
			SubsystemPath:        p.subsystemPath,
			SynthesizedSubsystem: p.synthesized,
		}
		nodes.nodes = append(nodes.nodes, node)
		connections := view.AuthoredUsesSocketConnections(p.name)
		for _, socket := range connections.Keys() {
			for _, alias := range view.AuthoredUsesSocketEntryAliases(p.name, socket) {
				edges.edges = append(edges.edges, constraint.EdgeInstance{
					Node: node, Socket: socket, Alias: alias,
				})
			}
		}
	}
	return nodes, edges
}

// TestTheDeclaredRulesAgreeWithTheHandWrittenGuardsOverOneDocument is the
// equal-coverage cross-check, and it is what the declared rules' invocation
// waits on rather than a nicety alongside it.
//
// Both readings are driven from ONE authored document: the hand-written guards
// read it directly, and the declared rules read the same document through the
// presentation, invoked by the framework's own traversals. So what follows is
// one document read two ways rather than two fixtures that happen to agree.
//
// The comparison is by sentinel and by count. The finding SHAPE deliberately
// differs between the two readings — a hand-written finding composes its own
// sentence and locator, a raised objection carries the sentinel's registered
// wording and the framework's locator — so comparing text would measure that
// difference rather than coverage.
//
// INVARIANT: for one document, the declared rules raise exactly what the
// hand-written guards report, rule for rule.
// CONSEQUENCE: were this to fail, the declared rules could not replace the
// hand-written ones without changing which documents the compiler refuses — and
// the divergence would be invisible until a user hit it.
func TestTheDeclaredRulesAgreeWithTheHandWrittenGuardsOverOneDocument(t *testing.T) {
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
	nodes, edges := coordinatesOf(na, view)

	// The hand-written reading, per rule. Each guard is called directly rather
	// than through the guard sequence, so what is compared is one rule against
	// one rule.
	handWritten := map[string]int{}
	for _, err := range guardSchemaConformance(na, catalog, nil, nil, view) {
		handWritten[sentinelKey(err)]++
	}
	for _, err := range guardInjectKeys(na, catalog, view) {
		handWritten[sentinelKey(err)]++
	}

	declared := map[string]int{}
	for _, check := range []constraint.Check[constraint.NormalizedView, constraint.NodeSubject]{
		componentValuesConformToTheType,
		componentInfraConformsToTheType,
	} {
		objections, err := constraint.RunOnNodes[constraint.NormalizedView](nil, nodes, view, check)
		if err != nil {
			t.Fatalf("the node traversal returned an error: %v", err)
		}
		for _, o := range objections {
			declared[o.Sentinel().Error()]++
		}
	}
	selfInject, err := constraint.RunOnNodeMechanismDeliveryKeys[constraint.NormalizedView](
		nil, nodes, mechanismSet{view: view}, deliveryKeySet{view: view}, view,
		selfInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the delivery-key traversal returned an error: %v", err)
	}
	for _, o := range selfInject {
		declared[o.Sentinel().Error()]++
	}
	connectionInject, err := constraint.RunOnEdges[constraint.NormalizedView](
		nil, edges, view, connectionInjectReferenceIsPublished)
	if err != nil {
		t.Fatalf("the edge traversal returned an error: %v", err)
	}
	for _, o := range connectionInject {
		declared[o.Sentinel().Error()]++
	}

	// A positive control PER RULE, not one over the whole comparison. A count
	// of findings says nothing about which rules produced them, so a fixture
	// that stopped exercising one rule would leave that rule's two sides
	// agreeing at zero — an agreement over nothing, reported as coverage.
	for _, control := range []error{ErrSchemaConformance(), ErrInvalidInjectKey()} {
		if handWritten[control.Error()] == 0 {
			t.Fatalf("the hand-written reading raised nothing for rule %q.\n"+
				"INVARIANT: the fixture violates every rule being compared, so each rule has a "+
				"non-empty pair to compare.\n"+
				"CONSEQUENCE: that rule's comparison succeeds over two empty sets, and the "+
				"agreement is reported as coverage of a rule nothing exercised.", control.Error())
		}
	}

	for _, key := range unionOfKeys(handWritten, declared) {
		if handWritten[key] != declared[key] {
			t.Errorf("rule %q: the hand-written reading raised %d and the declared rule raised %d "+
				"over the same document.\n"+
				"INVARIANT: for one document the two readings agree rule for rule.\n"+
				"CONSEQUENCE: replacing the hand-written path with the declared one changes which "+
				"documents the compiler refuses, and the change is invisible until a user hits it.",
				key, handWritten[key], declared[key])
		}
	}
}

// sentinelKey is the identity a finding is counted under: the rule's own
// registered wording, which both readings carry and neither composes.
func sentinelKey(err *ResolverError) string {
	if err == nil || err.Err == nil {
		return ""
	}
	return err.Err.Error()
}

// unionOfKeys returns every rule either reading raised, sorted, so a rule
// present on one side only is compared rather than skipped.
func unionOfKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
