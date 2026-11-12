// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The required-socket rule reads three surfaces, and the last of them is the
// connection set this composition produced — which is why the rule is asked
// here, over what a real flatten built, rather than over a hand-assembled
// presentation. A fixture whose evidence was assigned rather than composed
// could not tell an evidence surface that works from one that is never reached.
//
// The rule's production invocation is not in this package: it is made by the
// pass that reads this stage's presentation, and the proof that it is invoked
// there — and that a build halts on it — lives beside that pass. What is proved
// here is the rule's own reading, over every shape of input that can satisfy or
// fail it.

// askRequiredSocket builds the fixture and asks the rule about it, returning
// the findings in the order the traversal raised them.
//
// It invokes the rule through the framework's own traversal over the axes the
// build published, so the subjects and the locators are the ones production
// produces. Assembling either here would prove the predicate and say nothing
// about what a reader of the diagnostic sees.
func askRequiredSocket(t *testing.T, aggYAML, relYAML, modYAML string) []*resolver.ResolverError {
	t.Helper()
	result, err := buildResultWith(t, aggYAML, relYAML, modYAML)
	if err != nil {
		t.Fatalf("the fixture must compose; the build halted with %v", err)
	}
	if result.StageView == nil || result.StageCoordinates == nil {
		t.Fatal("the build published no view or no enumeration; the rule could not be asked")
	}
	objections, err := constraint.RunOnNodeSockets(
		nil, result.StageCoordinates, result.StageCoordinates,
		result.StageView, resolver.RequiredSocketIsPopulated)
	if err != nil {
		t.Fatalf("the socket walk returned an error with no cancellation signal: %v", err)
	}
	return resolver.FindingsFromObjections(objections, "Component")
}

// sameLocators compares two locator sequences, order included, because the
// order a rule raises in is part of what makes identical input produce
// byte-identical diagnostics.
func sameLocators(a, b []string) bool {
	return slices.Equal(a, b)
}

// locatorsOfFindings projects what a front end positions on, in raise order.
func locatorsOfFindings(findings []*resolver.ResolverError) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Field)
	}
	return out
}

// The Aggregate shapes. Each names a target that exists, so a fixture halts
// for the rule under test and not for a connection reaching nothing.
const (
	// requiredSocketUnbound leaves the required socket the type declares
	// entirely unwritten.
	requiredSocketUnbound = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
    db:
      type: app/db:1.0.0
`
	// requiredSocketVerbose writes the long authoring form.
	requiredSocketVerbose = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: db
    db:
      type: app/db:1.0.0
`
	// requiredSocketCompact writes the short authoring form.
	requiredSocketCompact = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          - db
    db:
      type: app/db:1.0.0
`
	// requiredSocketEmptyVerbose names the socket and writes no entry under it.
	requiredSocketEmptyVerbose = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases: {}
    db:
      type: app/db:1.0.0
`
	// optionalSocketUnbound leaves the socket the type declares WITHOUT a
	// requirement unwritten, and binds the required one.
	optionalSocketUnbound = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          - db
    db:
      type: app/db:1.0.0
`
	// requiredSocketInDeclaredSubsystem puts the offending component inside a
	// scope the author declared, alongside the component a Release can wire
	// it to.
	requiredSocketInDeclaredSubsystem = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    data:
      components:
        worker:
          type: app/svc:1.0.0
        store:
          type: app/db:1.0.0
`
	// requiredSocketInNestedSubsystem puts it two scopes deep.
	requiredSocketInNestedSubsystem = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    outer:
      subsystems:
        inner:
          components:
            worker:
              type: app/svc:1.0.0
            store:
              type: app/db:1.0.0
`
	// twoConsumersUnbound leaves the requirement unwritten on two components,
	// so a count assertion has two members and their order is observable.
	twoConsumersUnbound = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    zebra:
      type: app/svc:1.0.0
    alpha:
      type: app/svc:1.0.0
    db:
      type: app/db:1.0.0
`
	// twoConsumersOneBound carries two components declaring the same
	// requirement, so a Release binding one of them leaves the other's
	// socket unpopulated.
	twoConsumersOneBound = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
    sibling:
      type: app/svc:1.0.0
    db:
      type: app/db:1.0.0
`
	// requiredSocketCompactEmptyItem writes the short form with an item naming
	// no target at all.
	requiredSocketCompactEmptyItem = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          - ""
    db:
      type: app/db:1.0.0
`
	// componentNamingAnAbsentType names a catalog identity the catalog holds no
	// entry for, alongside a component that offends so the fixture also shows
	// the rule being invoked.
	componentNamingAnAbsentType = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
    stray:
      type: app/absent:9.9.9
    db:
      type: app/db:1.0.0
`
	// moduleImport imports the module the loader answers with.
	moduleImport = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: ./mod.yaml
`
	// moduleLeavingItUnbound is the case only the composed connection set can
	// answer: the component is declared in the module's own document, which is
	// read when the composition is flattened.
	moduleLeavingItUnbound = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
    store:
      type: app/db:1.0.0
`
	// moduleBindingIt writes the connection inside the module.
	moduleBindingIt = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    consumer:
      type: app/svc:1.0.0
      uses:
        databases:
          main:
            target: store
    store:
      type: app/db:1.0.0
`
)

// The Release shapes. Each places the module so a routed fixture is not
// reported for being unplaced.
const (
	releasePlacingTheModule = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      aggregates:
        mod: {}
`
	// releaseBindingTheSocket creates the connection the Aggregate left out.
	releaseBindingTheSocket = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        api:
          uses:
            databases:
              wired:
                target: db
        db: {}
`
	// releaseBindingTheSocketBesideASibling is the same binding for the
	// Aggregate that declares a second consumer. It is a fixture of its own
	// rather than a widening of the one above, because that one is shared with an
	// Aggregate declaring no sibling, and naming a component that Aggregate
	// does not declare would replace the fault under test with a different one.
	//
	// It places every component its Aggregate declares, so the composition the
	// rule is asked about is one a Release could really produce: a component
	// belonging to no boundary halts before any rule about sockets is reached.
	releaseBindingTheSocketBesideASibling = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        api:
          uses:
            databases:
              wired:
                target: db
        sibling: {}
        db: {}
`
	// releaseConfiguringWithoutATarget writes an entry that names no target, so
	// it configures a connection that must already exist rather than creating
	// one.
	//
	// The entry carries nothing else, and that is deliberate: an entry naming no
	// target AND carrying operator sizing is reported by the rule about sizing
	// that reaches no connection, which halts the composition before this rule is
	// asked. Written that way, the fixture would measure the other rule.
	releaseConfiguringWithoutATarget = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        api:
          uses:
            databases:
              wired: {}
        db: {}
`
	// releaseBindingASubsystemComponent creates the connection for a component
	// the Aggregate declared inside a scope, addressing it by the bare
	// instance name — which is how a Release names a component at every
	// placement.
	releaseBindingASubsystemComponent = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        worker:
          uses:
            databases:
              wired:
                target: store
        store: {}
`
	// releaseBindingAnotherSocket creates a connection on the socket the type
	// declares without a requirement.
	releaseBindingAnotherSocket = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        api:
          uses:
            caches:
              hot:
                target: db
        db: {}
`
)

// TestTheRequiredSocketRuleReadsEverySurfaceThatCanPopulateASocket is the
// reading proof: every shape that populates the socket passes, every shape that
// does not is reported, and each report names the socket and positions at it.
//
// The rows are grouped by which surface the population came from, because that
// grouping is the rule's own structure: it reads the component's own dependency
// block, the Release's edge surface and the connection set the composition
// produced, and a row is a claim about one of those three. The negative rows
// beside each are what stop a surface being read too generously — a Release
// entry that configures rather than creates, a binding on a sibling socket, a
// binding on another component.
func TestTheRequiredSocketRuleReadsEverySurfaceThatCanPopulateASocket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		agg      string
		release  string
		module   string
		locators []string
	}{
		{
			name:     "nothing populates it",
			agg:      requiredSocketUnbound,
			locators: []string{".spec.components.api.uses.databases"},
		},
		{
			name: "the component's own long form populates it",
			agg:  requiredSocketVerbose,
		},
		{
			name: "the component's own short form populates it",
			agg:  requiredSocketCompact,
		},
		{
			name:     "the socket is named with no entry under it",
			agg:      requiredSocketEmptyVerbose,
			locators: []string{".spec.components.api.uses.databases"},
		},
		{
			name: "a socket carrying no requirement may be left unwritten",
			agg:  optionalSocketUnbound,
		},
		{
			name:    "the Release creates the connection",
			agg:     requiredSocketUnbound,
			release: releaseBindingTheSocket,
		},
		{
			name:     "a Release entry naming no target creates nothing",
			agg:      requiredSocketUnbound,
			release:  releaseConfiguringWithoutATarget,
			locators: []string{".spec.components.api.uses.databases"},
		},
		{
			name:     "the Release binds a sibling socket",
			agg:      requiredSocketUnbound,
			release:  releaseBindingAnotherSocket,
			locators: []string{".spec.components.api.uses.databases"},
		},
		{
			name:     "the offending component sits in a declared scope",
			agg:      requiredSocketInDeclaredSubsystem,
			locators: []string{".spec.subsystems.data.components.worker.uses.databases"},
		},
		{
			name:     "the offending component sits two scopes deep",
			agg:      requiredSocketInNestedSubsystem,
			locators: []string{".spec.subsystems.outer.subsystems.inner.components.worker.uses.databases"},
		},
		{
			name:    "a module leaves it unpopulated",
			agg:     moduleImport,
			release: releasePlacingTheModule,
			module:  moduleLeavingItUnbound,
			// The connection set the composition produced is the only surface
			// that can answer for this component: its dependency block lives in
			// the module's own document.
			locators: []string{".spec.components.consumer.uses.databases"},
		},
		{
			name:    "a module populates it in its own document",
			agg:     moduleImport,
			release: releasePlacingTheModule,
			module:  moduleBindingIt,
		},
		{
			// The other half of a one-home property: a type the catalog holds no
			// entry for declares no socket set, so this rule ends on that
			// component and leaves the classification to the rule that owns it.
			// The offending sibling is what makes the silence meaningful — it
			// shows the walk reaching this fixture's components at all.
			name:     "a component names a type the catalog holds no entry for",
			agg:      componentNamingAnAbsentType,
			locators: []string{".spec.components.api.uses.databases"},
		},
		{
			name:    "the Release binds a component the Aggregate declared inside a scope",
			agg:     requiredSocketInDeclaredSubsystem,
			release: releaseBindingASubsystemComponent,
		},
		{
			name:     "the Release binds the socket on a sibling component",
			agg:      twoConsumersOneBound,
			release:  releaseBindingTheSocketBesideASibling,
			locators: []string{".spec.components.sibling.uses.databases"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := askRequiredSocket(t, tc.agg, tc.release, tc.module)
			if got := locatorsOfFindings(findings); !sameLocators(got, tc.locators) {
				t.Fatalf("findings positioned at %v; want %v", got, tc.locators)
			}
			for _, f := range findings {
				if !errors.Is(f, resolver.ErrMissingRequiredSocket()) {
					t.Errorf("a finding must carry the rule's own identity; got %v", f.Err)
				}
				if !strings.Contains(f.Message, "databases") {
					t.Errorf("a finding must name the socket the author has to write; got %q", f.Message)
				}
				if !strings.Contains(f.Message, "app/svc") {
					t.Errorf("a finding must name the type that declared the requirement; got %q", f.Message)
				}
			}
		})
	}
}

// TestTheRequiredSocketRuleReportsEveryOffenderInCompositionOrder proves the
// rule reports each offending component rather than the first, and that the
// order is the one the composition produced.
//
// The order matters beyond tidiness: identical input has to produce a
// byte-identical diagnostic sequence, and the enumeration's order is what
// carries that. The fixture declares its two consumers in an order a sort would
// reverse, so a reading that sorted would fail here.
func TestTheRequiredSocketRuleReportsEveryOffenderInCompositionOrder(t *testing.T) {
	t.Parallel()
	findings := askRequiredSocket(t, twoConsumersUnbound, "", "")
	want := []string{
		".spec.components.zebra.uses.databases",
		".spec.components.alpha.uses.databases",
	}
	if got := locatorsOfFindings(findings); !sameLocators(got, want) {
		t.Fatalf("every offender must be reported in the order the document declares; got %v, want %v", got, want)
	}
}

// TestTheRequiredSocketWalkStopsPartwayOnCancellation proves the walk observes
// the signal per node rather than checking it once and running to completion.
//
// Counting the invocations is the whole point. An assertion that the walk
// returned an error cannot tell a walk that stopped early from one that ran
// every node and reported afterwards, and the second would defeat the reason
// the signal exists.
func TestTheRequiredSocketWalkStopsPartwayOnCancellation(t *testing.T) {
	t.Parallel()

	// A composition wide enough that stopping partway is unambiguous.
	const total = 40
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  components:\n")
	b.WriteString("    db:\n      type: app/db:1.0.0\n")
	for i := 0; i < total; i++ {
		fmt.Fprintf(&b, "    api-%02d:\n      type: app/svc:1.0.0\n", i)
	}

	result, err := buildResultWith(t, b.String(), "", "")
	if err != nil {
		t.Fatalf("the fixture must compose; the build halted with %v", err)
	}

	// The total is read off the enumeration rather than assumed equal to the
	// number of offenders: the fixture carries a component that offends against
	// nothing, and an assumed total would make this measure the fixture's shape
	// instead of the property.
	nodes := len(result.StageCoordinates.Nodes())
	if nodes <= 1 {
		t.Fatalf("the enumeration must supply more than one node for a partway stop to mean anything; got %d", nodes)
	}

	// The signal turns after a handful of reads, and the reads are counted so
	// the assertion is about how far the walk got.
	const allowed = 4
	reads := 0
	cancelled := func() error {
		reads++
		if reads > allowed {
			return errors.New("the caller gave up on this build")
		}
		return nil
	}

	invocations := 0
	counting := constraint.Check[constraint.TopologyView, constraint.NodeSocketSubject]{
		Sentinel: resolver.RequiredSocketIsPopulated.Sentinel,
		Severity: resolver.RequiredSocketIsPopulated.Severity,
		Run: func(v constraint.TopologyView, s constraint.NodeSocketSubject) []constraint.Objection {
			invocations++
			return resolver.RequiredSocketIsPopulated.Run(v, s)
		},
	}

	objections, err := constraint.RunOnNodeSockets(
		cancelled, result.StageCoordinates, result.StageCoordinates, result.StageView, counting)
	if err == nil {
		t.Fatal("a walk the caller cancelled must report why it stopped")
	}
	if objections != nil {
		t.Fatalf("a cancelled walk must hand back nothing rather than a partial set; got %d objections", len(objections))
	}
	if reads > allowed+1 {
		t.Errorf("the signal was read %d times for %d nodes; the walk must stop within one node of it turning", reads, nodes)
	}
	if invocations >= nodes {
		t.Fatalf("the check was invoked %d times over %d nodes; the walk ran to completion instead of stopping partway", invocations, nodes)
	}
	if invocations == 0 {
		t.Fatal("the check was never invoked; the counting wrapper proves nothing about where the walk stopped")
	}
}

// TestTheShortFormItemNamingNoTargetIsNotThisRulesToReport pins where an item
// that names no target is reported, because it is the one authoring shape whose
// answer is not readable off the rule.
//
// The item populates nothing an author could act on, so the component's own
// dependency block does not count it. But normalisation projects one entry per
// item the short form lists, so the composed connection set — which this rule
// also reads — does carry one, and the socket therefore reads as populated here.
// That is the right answer for this rule and not a gap: the entry names no
// component, so it is the rule about a connection reaching nothing that has the
// precise sentence for what the author wrote, and that rule is asked over the
// finalised graph. Its refusal is proved there.
//
// What this pins is that the two do not both report one input. A required-socket
// finding here would tell the author to populate a socket they did populate, over
// an entry whose real fault is the empty target.
//
// The row is here rather than in the table above because the table's helper
// requires a composition that composes, and this one carries a fault a later rule
// reports.
func TestTheShortFormItemNamingNoTargetIsNotThisRulesToReport(t *testing.T) {
	t.Parallel()
	result, err := buildResultWith(t, requiredSocketCompactEmptyItem, "", "")
	if err != nil {
		t.Fatalf("the composition must reach the graph for the rule over it to be asked; got %v", err)
	}
	if errors.Is(err, resolver.ErrMissingRequiredSocket()) {
		t.Fatalf("the required-socket rule must not report this input; got %v", err)
	}
	objections, runErr := constraint.RunOnNodeSockets(
		context.Background().Err,
		result.StageCoordinates,
		result.StageCoordinates,
		result.StageView,
		resolver.RequiredSocketIsPopulated,
	)
	if runErr != nil {
		t.Fatalf("traversal: %v", runErr)
	}
	if len(objections) != 0 {
		t.Fatalf("this rule must stay silent on an item naming no target; it raised %d objection(s)", len(objections))
	}
	if len(result.Edges) != 1 || result.Edges[0].Target != "" {
		t.Fatalf("edges = %+v; want the empty target carried through to the rule that reports it", result.Edges)
	}
}
