// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the presence mandate a component type declares,
// evaluated over the finalised graph. They sit in this package for the same
// reason the proofs beside them do: the presentation and the enumeration the
// rule needs are both carried on an unexported output, and a proof over an
// enumeration a test assembles proves the test's account of the graph rather
// than the compiler's.
//
// # Why the fixture is spliced on the catalog rather than in the Aggregate
//
// A presence mandate is an annotation on the component catalog entity, and no
// committed workspace this package compiles declares one. A proof compiling one
// unspliced would exercise a rule that ends on its first condition for every
// subject, which reports exactly what a satisfied rule reports. The splice is
// written into a temporary copy of the fixture catalog so the committed fixture
// keeps producing what every other proof over it expects.

// The mandate the splices below declare and the Feature type it demands. The
// type is one the fixture's own catalog carries, so a leg that satisfies the
// mandate can attach something real rather than a name nothing resolves.
const (
	mandateLabel = "monitoring"
	mandateKey   = "features.aggrega.ai/requiredType." + mandateLabel
	mandateType  = slotFillType
)

// catalogDeclaringMandates copies the fixture catalog into a temporary tree and
// gives the two component types the Aggregate's own components are declared
// with the supplied metadata.annotations body.
//
// Two entities rather than one, and both enumeration proofs below rest on it: a
// fixture whose findings all sit on one node cannot show a narrowed node axis,
// and a walk stopped after the first node is arithmetically indistinguishable
// from a complete one. The two entities carry different type names, so a finding
// naming the declaring type is distinguishable across them.
//
// It is shared by the proofs of both rules keyed on a presence mandate — the one
// about a mandate no attachment satisfies and the one about a mandate whose
// declaration cannot be evaluated — because the two need the same splice and
// differ only in what the annotation says. A second copy of the copy-and-splice
// mechanism would agree with this one on the day it was written and be free to
// drift afterwards, with nothing comparing them.
//
// The body is the whole annotations block, indented to the depth those keys sit
// at, so a caller reads the YAML it is writing.
func catalogDeclaringMandates(t *testing.T, annotationsBody string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(root, os.DirFS(invocationCatalogPath)); err != nil {
		t.Fatalf("copy catalog: %v", err)
	}
	block := "  version: \"1.0.0\"\n  annotations:\n" + annotationsBody
	for _, entity := range []string{"api-server.yaml", "web-ui.yaml"} {
		path := filepath.Join(root, "base", "components", entity)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entity, err)
		}
		const anchor = "  version: \"1.0.0\"\n"
		if !bytes.Contains(source, []byte(anchor)) {
			t.Fatalf("the entity %s no longer carries %q; the rewrite would be silently inert", entity, anchor)
		}
		if err := os.WriteFile(path, bytes.Replace(source, []byte(anchor), []byte(block), 1), 0o600); err != nil {
			t.Fatalf("write %s: %v", entity, err)
		}
	}
	return root
}

// compileMandateWorkspace compiles the fixture against a catalog whose two
// application component types carry the supplied annotations.
func compileMandateWorkspace(
	t *testing.T,
	annotationsBody string,
	aggregate []byte,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()
	return compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   catalogDeclaringMandates(t, annotationsBody),
	}, aggregate)
}

// unsatisfiedMandate declares one well-formed mandate on both entities. Nothing
// in the fixture attaches the type it demands, so both nodes offend.
const unsatisfiedMandate = "    " + mandateKey + ": " + mandateType + "\n"

// attachTheDemandedType attaches the demanded Feature type on both of the
// Aggregate's own components, under a name no slot declares — which is the
// case a presence mandate exists for, since it asks about a type under any name.
func attachTheDemandedType(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	fill := "      features:\n        observability:\n          type: " + mandateType + "\n"
	for _, component := range []string{
		"    backend:\n      type: core/compute/api-server\n",
		"    frontend:\n      type: core/compute/web-ui\n",
	} {
		if !bytes.Contains(aggregate, []byte(component)) {
			t.Fatalf("fixture no longer declares %q the way the splice expects; the splice would be silently inert", component)
		}
		aggregate = bytes.Replace(aggregate, []byte(component), []byte(component+fill), 1)
	}
	return aggregate
}

// TestThePresenceMandateRuleHaltsThroughTheViolationChannel is the
// invocation-and-content proof.
//
// It compiles a real workspace whose catalog really declares a mandate nothing
// satisfies, and reads the finding off the production violation slice — the one
// the build gates on — rather than off a traversal the test ran itself. The
// clean leg is what stops the offending leg passing on a rule that reports
// unconditionally.
func TestThePresenceMandateRuleHaltsThroughTheViolationChannel(t *testing.T) {
	t.Parallel()

	t.Run("every unsatisfied mandate reaches the violation slice", func(t *testing.T) {
		t.Parallel()
		out, _ := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))

		// Two offences rather than one: a reading that returned on the first
		// would pass a single-offence document.
		if len(out.Analysis.Errors) != 2 {
			t.Fatalf("violations = %d, want 2 (one per component whose type declares the mandate): %+v",
				len(out.Analysis.Errors), out.Analysis.Errors)
		}
		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
		}

		wantLocators := []string{".spec.components.backend", ".spec.components.frontend"}
		wantTypes := []string{"core/compute/api-server", "core/compute/web-ui"}
		for i, violation := range out.Analysis.Errors {
			if !errors.Is(violation.Sentinel, resolver.ErrMissingRequiredFeature()) {
				t.Errorf("violation %d sentinel = %v, want the missing-required-feature sentinel", i, violation.Sentinel)
			}
			if violation.Kind != analyzer.AnalysisErrorLocated {
				t.Errorf("violation %d kind = %q, want %q", i, violation.Kind, analyzer.AnalysisErrorLocated)
			}
			if violation.Field != wantLocators[i] {
				t.Errorf("violation %d locator = %q, want %q", i, violation.Field, wantLocators[i])
			}
			if violation.OriginFile != invocationAggregatePath {
				t.Errorf("violation %d document = %q, want %q", i, violation.OriginFile, invocationAggregatePath)
			}
			// All three coordinates are in the text, because the editor renders
			// the message and recovers position as a range rather than reading
			// the locator as words. Without the declaring type, two components
			// demanding one type would read as the same string there.
			for _, want := range []string{wantTypes[i], mandateType, mandateLabel} {
				if !strings.Contains(violation.Message, want) {
					t.Errorf("violation %d message does not name %q: %q", i, want, violation.Message)
				}
			}
		}

		// The conversion the command line reads: a developer fault pointing at
		// the document, not at a node identifier.
		converted := out.Analysis.Errors[0].ToCompilerError()
		if converted.Category() != cerrors.DeveloperError {
			t.Errorf("category = %v, want %v", converted.Category(), cerrors.DeveloperError)
		}
		if converted.Source().File != invocationAggregatePath {
			t.Errorf("source pointer = %q, want the document the finding is about", converted.Source().File)
		}
	})

	t.Run("a document attaching the demanded type reports nothing", func(t *testing.T) {
		t.Parallel()
		out, _ := compileMandateWorkspace(t, unsatisfiedMandate, attachTheDemandedType(t, readAggregate(t)))
		if len(out.Analysis.Errors) != 0 {
			t.Fatalf("a document satisfying every mandate reported %d violation(s): %+v",
				len(out.Analysis.Errors), out.Analysis.Errors)
		}
	})

	t.Run("two mandates on one type report in the order the annotations declare them", func(t *testing.T) {
		t.Parallel()
		// Two mandates on each entity, so one component produces two findings.
		// That is the only shape on which the ordering claim is observable, and
		// it is also the only shape on which the label in the message is
		// load-bearing: without it the two would read alike.
		const twoMandates = "    " + mandateKey + ": " + mandateType + "\n" +
			"    features.aggrega.ai/requiredType.tracing: core/context-propagation\n"
		out, _ := compileMandateWorkspace(t, twoMandates, readAggregate(t))

		byNode := map[string][]string{}
		for _, violation := range out.Analysis.Errors {
			if !errors.Is(violation.Sentinel, resolver.ErrMissingRequiredFeature()) {
				continue
			}
			byNode[violation.Field] = append(byNode[violation.Field], violation.Message)
		}
		var withTwo int
		for node, messages := range byNode {
			if len(messages) != 2 {
				continue
			}
			withTwo++
			// Annotation author order: the monitoring mandate is declared first.
			if !strings.Contains(messages[0], mandateLabel) {
				t.Errorf("%s: the first finding does not name the mandate declared first: %q", node, messages[0])
			}
			if !strings.Contains(messages[1], "tracing") {
				t.Errorf("%s: the second finding does not name the mandate declared second: %q", node, messages[1])
			}
			// And the pair is distinguishable, which is what the label in the
			// message buys.
			if messages[0] == messages[1] {
				t.Errorf("%s: two findings about two different mandates read identically: %q", node, messages[0])
			}
		}
		if withTwo == 0 {
			t.Fatalf("no component produced two findings, so neither the ordering nor the distinguishability claim is exercised: %+v", byNode)
		}
	})

	t.Run("a component type declaring no mandate reports nothing", func(t *testing.T) {
		t.Parallel()
		out, _ := compileMandateWorkspace(t,
			"    component.aggrega.ai/unrelated: \"true\"\n", readAggregate(t))
		if len(out.Analysis.Errors) != 0 {
			t.Fatalf("an annotation that is not a mandate reported %d violation(s): %+v",
				len(out.Analysis.Errors), out.Analysis.Errors)
		}
	})
}

// countingPresenceMandateRule is the declared rule with a counter around its
// predicate, so a proof can assert what the rule was ASKED about rather than
// only what it raised.
func countingPresenceMandateRule(
	visited *[]string,
) constraint.Check[constraint.GraphView, constraint.NodeSubject] {
	rule := analyzer.RequiredAttachmentTypeAttached
	return constraint.Check[constraint.GraphView, constraint.NodeSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.GraphView, s constraint.NodeSubject) []constraint.Objection {
			*visited = append(*visited, s.NodeID())
			return rule.Run(v, s)
		},
	}
}

// TestThePresenceMandateRuleIsInvokedOncePerNodeTheGraphHolds is the named
// enumeration assertion, and it has two halves because one of them structurally
// cannot cover what the other does.
//
// The AXIS half runs the traversal itself and establishes what the enumeration
// holds. It cannot say anything about what the BUILD handed the rule: a build
// passing a narrower enumeration to its one call would leave the axis half
// green. So the PRODUCTION half compares the build's own violation slice against
// a yardstick derived by a different route — every node whose component type
// declares a mandate the node's merged attachments do not satisfy, read off the
// catalog entity's own annotations rather than through the fact the rule reads.
//
// A green equivalence check cannot substitute for either half. A narrowed axis
// asks both readings about fewer things, they agree, and nothing is raised,
// which is indistinguishable from a satisfied rule.
func TestThePresenceMandateRuleIsInvokedOncePerNodeTheGraphHolds(t *testing.T) {
	t.Parallel()

	out, catalogLookup := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))
	coordinates := coordinatesOf(t, out, catalogLookup)

	held := out.IRGraph.Spec.Nodes.Keys()
	// Anti-vacuity. Fewer than two nodes and a walk stopped after the first is
	// arithmetically the same as a complete one; fewer than two offending nodes
	// and a reading that returned on the first offence would pass the production
	// half below.
	if len(held) < 2 {
		t.Fatalf("the workspace holds %d node(s); a node-axis assertion needs at least 2", len(held))
	}

	var visited []string
	objections, err := constraint.RunOnNodes(
		context.Background().Err, coordinates, out.GraphView, countingPresenceMandateRule(&visited))
	if err != nil {
		t.Fatalf("the walk over an uncancelled signal returned %v", err)
	}

	// The enumeration's own total against the graph's, taken from the graph
	// rather than from a figure written here.
	if got := len(coordinates.Nodes()); got != len(held) {
		t.Errorf("the enumeration yields %d node(s) where the graph holds %d", got, len(held))
	}
	if len(visited) != len(held) {
		t.Errorf("the rule was invoked %d time(s) for %d node(s) the graph holds", len(visited), len(held))
	}
	// A set difference rather than a count comparison: both sides short by one
	// still agree on the count, and the missing coordinate is what a reader
	// needs.
	for _, node := range held {
		if !slices.Contains(visited, node) {
			t.Errorf("the rule was never invoked for the node %q the graph holds", node)
		}
	}

	// The production half. The yardstick is derived from the catalog entity's
	// own annotations and the graph's merged attachment set — a different route
	// from the declared facts the rule reads — so a narrowing on either side
	// shows up as a difference rather than as agreement.
	var wantOffending []string
	for _, node := range held {
		record, ok := out.IRGraph.Spec.Nodes.Get(node)
		if !ok {
			continue
		}
		entity, found := catalogLookup.Component(record.Component)
		if !found {
			continue
		}
		attached := map[string]bool{}
		if entries, held := out.IRGraph.Spec.FeatureValuesMapping.Get(node); held {
			for _, entry := range entries.All() {
				attached[entry.Type] = true
			}
		}
		for _, mandate := range types.RequiredAttachmentTypes(entity.Metadata.Annotations) {
			if mandate.Malformed != "" || attached[mandate.Type] {
				continue
			}
			wantOffending = append(wantOffending, ".spec.components."+constraint.AuthoredComponentName(node))
		}
	}
	if len(wantOffending) < 2 {
		t.Fatalf("the workspace leaves %d mandate(s) unsatisfied; the production half needs at least 2 so a reading that returned on the first would fail", len(wantOffending))
	}

	var reported []string
	for _, violation := range out.Analysis.Errors {
		if errors.Is(violation.Sentinel, resolver.ErrMissingRequiredFeature()) {
			reported = append(reported, violation.Field)
		}
	}
	if len(reported) != len(wantOffending) {
		t.Errorf("the build reported %d violation(s) for %d unsatisfied mandate(s): %v",
			len(reported), len(wantOffending), reported)
	}
	for _, want := range wantOffending {
		if !slices.Contains(reported, want) {
			t.Errorf("the build reported nothing at %q, the authoring key of a component whose type declares a mandate nothing satisfies", want)
		}
	}

	// The walk raised what the build reported, so the axis half above is about a
	// walk that did the work rather than an empty loop.
	if len(objections) != len(wantOffending) {
		t.Errorf("the walk raised %d objection(s) for %d unsatisfied mandate(s)", len(objections), len(wantOffending))
	}
}

// TestThePresenceMandateRuleStopsPartwayOnACancelledWalk proves the property the
// acceptance names: a cancelled walk stops, rather than running to completion
// and reporting the withdrawal afterwards.
//
// The two are indistinguishable by the returned error — both return the
// caller's withdrawal — so the assertion is the exact invocation count.
func TestThePresenceMandateRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	t.Parallel()

	out, catalogLookup := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))
	coordinates := coordinatesOf(t, out, catalogLookup)
	nodes := len(coordinates.Nodes())
	if nodes < 2 {
		t.Fatalf("the enumeration holds %d node(s); a stops-partway assertion needs at least 2", nodes)
	}

	t.Run("the walk stops on the node after the signal turns", func(t *testing.T) {
		var reads int
		// Turns on its second read: the first node is entered, the second is not.
		signal := func() error {
			reads++
			if reads > 1 {
				return context.Canceled
			}
			return nil
		}
		var visited []string
		objections, err := constraint.RunOnNodes(
			signal, coordinates, out.GraphView, countingPresenceMandateRule(&visited))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the withdrawal", err)
		}
		if len(visited) != 1 {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly 1 out of the enumeration's %d node(s)",
				len(visited), nodes)
		}
		// A cancelled walk hands back nothing rather than a truncated set, so a
		// caller cannot mistake a partial reading for a complete one.
		if len(objections) != 0 {
			t.Errorf("a cancelled walk returned %d objection(s)", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every node", func(t *testing.T) {
		var visited []string
		if _, err := constraint.RunOnNodes(
			context.Background().Err, coordinates, out.GraphView, countingPresenceMandateRule(&visited)); err != nil {
			t.Fatalf("the walk over an uncancelled signal returned %v", err)
		}
		if len(visited) != nodes {
			t.Errorf("the rule was invoked %d time(s) for %d node(s)", len(visited), nodes)
		}
	})
}

// TestTheGraphStageRulesThreadTheCallersSignalIntoThePresenceMandateWalk proves
// that the signal THIS walk reads is the caller's own.
//
// 🔴 Withdrawing before the function is entered proves nothing about this walk:
// the first walk to read the signal returns, every walk after it is never
// entered, and errors.Is against the withdrawal passes either way. So the
// withdrawal is timed to land inside this walk, and the assertions are about
// which walks reported rather than about what the function returned.
//
// The trigger index is derived from the walks that precede this one rather than
// copied from a sibling proof, because it is a function of walk order and moves
// whenever that order does. The walks before this one, in the order the site runs
// them, read the signal once per subject of their own outermost axis: the
// connection walk once per edge, then two node-keyed walks once per node each. So
// this walk's first read is number len(edges)+2*len(nodes)+1, and a signal that
// turns on the read after that one stops this walk on its second node.
//
// # The bound a total read count cannot cross, measured rather than assumed
//
// 🔴 A count of the caller's signal reads is NOT a discriminator on this site,
// and asserting one would be inert. Every walk after this one is keyed on the
// same node axis and reads the signal exactly as many times, so handing this walk
// some other signal does not change the total — it only moves which later walk
// consumes the reads and stops. The total is identical under both wirings.
//
// What discriminates is which walks reported. A workspace offending a rule BEFORE
// this one is therefore compiled deliberately: the earlier finding must be
// present, which is what rules out "the site stopped before reaching this walk",
// and this walk's finding must be absent, which is what rules out "this walk ran
// to the end of its axis on a signal that never turns". Neither half alone
// separates the three cases.
func TestTheGraphStageRulesThreadTheCallersSignalIntoThePresenceMandateWalk(t *testing.T) {
	t.Parallel()

	// The aggregate offends the connection-constraint rule, which this site runs
	// FIRST, as well as carrying the mandate this walk reports. See the bound above.
	out, _ := compileMandateWorkspace(
		t, unsatisfiedMandate, misdirectTheDatabaseSocket(t, readAggregate(t)))
	nodes := out.IRGraph.Spec.Nodes.Len()
	edges := len(out.IRGraph.Spec.Edges)
	if nodes < 2 {
		t.Fatalf("the graph holds %d node(s); the arithmetic below needs at least 2 so a stopped walk differs from a complete one", nodes)
	}

	// Established before the withdrawal is timed, so the assertions below are
	// about a document that really offends both rules rather than about one that
	// happens to offend neither.
	var uncancelledEarlier, uncancelledThisWalk int
	for _, violation := range out.Violations {
		switch {
		case errors.Is(violation.Sentinel(), resolver.ErrMissingRequiredFeature()):
			uncancelledThisWalk++
		default:
			uncancelledEarlier++
		}
	}
	if uncancelledEarlier == 0 || uncancelledThisWalk == 0 {
		t.Fatalf("the fixture must offend a rule before this walk AND this walk, or neither half of the assertion below separates anything; got %d earlier and %d here",
			uncancelledEarlier, uncancelledThisWalk)
	}

	// The read that ends this walk's first node, derived above.
	firstReadOfThisWalk := edges + 2*nodes + 1

	var reads int
	signal := countingCancellation{reads: &reads, withdrawAfter: firstReadOfThisWalk}
	// A fresh collection, so what is read below is what this invocation appended
	// rather than what the compile already had.
	out.Violations = nil
	err := runGraphStageRules(signal, out.GraphScope)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want the caller's withdrawal", err)
	}

	var earlier, thisWalk int
	for _, violation := range out.Violations {
		switch {
		case errors.Is(violation.Sentinel(), resolver.ErrMissingRequiredFeature()):
			thisWalk++
		default:
			earlier++
		}
	}
	// Half one: the walks before this one ran, so the withdrawal did not land
	// before this walk was reached.
	if earlier != uncancelledEarlier {
		t.Errorf("the walks before this one reported %d finding(s) where an uncancelled run reports %d; the withdrawal landed before this walk was reached",
			earlier, uncancelledEarlier)
	}
	// Half two: this walk stopped, so it was reading the signal that turned
	// rather than one that never does.
	if thisWalk != 0 {
		t.Errorf("this walk appended %d finding(s) after a caller that withdrew during its first node; it read a signal that never turned",
			thisWalk)
	}
	// The read count is reported rather than asserted, because on this site it
	// cannot separate the two wirings — see the bound above. It is here so a
	// later reader who changes the walk order sees the arithmetic move.
	t.Logf("signal reads: %d (this walk's first read is number %d, from %d edge read(s) and %d node read(s) before it)",
		reads, firstReadOfThisWalk, edges, 2*nodes)
}

// countingCancellation is a caller whose cancellation signal counts its reads
// and withdraws after the read named by withdrawAfter.
//
// It satisfies context.Context so it can be handed to a site that takes one, and
// only Err is meaningful: the site under proof reads the signal and nothing else
// off the context.
type countingCancellation struct {
	context.Context
	reads         *int
	withdrawAfter int
}

func (c countingCancellation) Err() error {
	*c.reads++
	if *c.reads > c.withdrawAfter {
		return context.Canceled
	}
	return nil
}
