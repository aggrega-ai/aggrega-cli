// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the rule that every attachment slot a component
// type declares required is bound by some attachment, evaluated over the
// finalised graph. They sit in this package for the same reason the two slot- and
// edge-keyed proofs beside them do: the presentation and the enumeration the rule
// needs are both carried on an unexported output, and a proof over an enumeration
// a test assembles proves the test's account of the graph rather than the
// compiler's.
//
// # Why the fixture is spliced rather than chosen
//
// One committed workspace declares a slot `required: true` — the daemon's own
// slot fixture, whose two components share ONE component type. A slot axis whose
// every member sits on one node cannot show a narrowed node axis, and a per-node
// walk stopped after the first node is then arithmetically indistinguishable from
// a complete one. So the declaration is written into a temporary copy of the
// fixture catalog on two component types, through the same mechanism the
// slot-constraint proofs beside these use.

// secondSlotName is a second attachment slot, used only by the leg that needs two
// findings about one component. It is a name no committed entity uses, for the
// same reason the shared one is.
const secondSlotName = "ledger"

// The slot schemas the splices below write. Each slot carries no label
// constraint, so the only rule their presence can make raise is the one under
// proof.
const (
	// One required slot: the shape every count in this file is measured against.
	requiredSlotDeclaration = "    " + slotName + ":\n      required: true\n"

	// The same slot declared optional. It is the control that separates "the rule
	// reported an unbound slot" from "the rule reports every unbound slot it is
	// asked about".
	optionalSlotDeclaration = "    " + slotName + ":\n      required: false\n"

	// Two required slots on one entity, so one component produces two findings
	// that share every value but the slot's name. It is the only shape on which
	// the slot name in the message is load-bearing, and therefore the only shape
	// on which its absence is detectable.
	twoRequiredSlotsDeclaration = "    " + slotName + ":\n      required: true\n" +
		"    " + secondSlotName + ":\n      required: true\n"
)

// compileRequiredSlotWorkspace compiles the fixture against a catalog declaring
// the shared slot with the supplied body.
func compileRequiredSlotWorkspace(
	t *testing.T,
	declarationBody string,
	aggregate []byte,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()
	return compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   catalogDeclaringSlots(t, declarationBody),
	}, aggregate)
}

// countingRequiredSlotRule is the declared rule with a counter around its
// predicate, so a proof can assert how many times it was invoked and for which
// coordinates. It delegates rather than reimplementing, so what is counted is the
// rule itself.
func countingRequiredSlotRule(
	visited *[]string,
) constraint.Check[constraint.GraphView, constraint.NodeSlotSubject] {
	rule := analyzer.RequiredAttachmentSlotBound
	return constraint.Check[constraint.GraphView, constraint.NodeSlotSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
			*visited = append(*visited, fmt.Sprintf("%s|%s", s.NodeID(), s.SlotName()))
			return rule.Run(v, s)
		},
	}
}

// unboundRequiredSlotsTheGraphHolds is the (node, slot) coordinate of every slot
// a node's component type declares required that nothing binds on that node. It
// is the yardstick the production violation set is measured against, so it cannot
// be a list the proof writes down.
//
// Its two halves are derived by different routes from the ones the rule takes.
// The component type is read from the graph's own node record where the
// enumeration reads it through the presentation's accessor, and the requirement
// is read off the catalog entity's own schema where the rule reads it through a
// declared fact. The binding half has one source in the compilation and is read
// from it by both, which is stated rather than dressed up: what a second
// derivation would compare there is nothing.
func unboundRequiredSlotsTheGraphHolds(
	t *testing.T,
	out *stepsOneToFourOutput,
	catalogLookup resolver.CatalogLookup,
) []string {
	t.Helper()
	if catalogLookup.Component == nil {
		t.Fatal("the workspace compiled without a component lookup; the slot axis would be empty for every node")
	}
	var held []string
	for id, node := range out.IRGraph.Spec.Nodes.All() {
		component, found := catalogLookup.Component(node.Component)
		if !found {
			continue
		}
		bound := out.GraphView.MergedAttachmentNames(id)
		for slot, declaration := range component.Spec.FeaturesSchema.All() {
			if !declaration.Required || slices.Contains(bound, slot) {
				continue
			}
			held = append(held, fmt.Sprintf("%s|%s", id, slot))
		}
	}
	return held
}

// locatorsOf reduces a violation set to the authoring keys it positions on.
func locatorsOf(violations []analyzer.AnalysisError) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.Field)
	}
	return out
}

// TestTheRequiredSlotRuleIsInvokedOncePerSlotAComponentTypeDeclares asserts the
// subject set the rule is asked about is the whole declared slot set, and that
// the production invocation reports on exactly the unbound required members of
// it — both by count and by coordinate.
//
// What it excludes, and what nothing else here can: an enumeration that yields
// fewer slots than the component types declare, or a production call handed a
// narrower one than the enumeration the proofs exercise directly. Either leaves
// the rule silently uninvoked over the slots it omits, and a comparison against a
// second reading, a clean-input control, and any content assertion about a slot
// the omission did not reach all still pass — a rule asked about nothing reports
// exactly what a rule asked and satisfied reports. So both counts are compared
// against yardsticks read off the graph and the catalog, the missing coordinates
// are named rather than counted, and the fixture is required to hold enough
// slots, spread over enough nodes, for either comparison to be able to fail.
//
// One bound is worth stating rather than leaving a reader to over-read this. The
// slot axis is reached through the node walk, so a narrowed node axis is caught
// here only when the node it drops carries a slot; dropping a node that declares
// none removes no subject this rule would have been invoked for, and the count is
// right to stay green. What that narrowing costs is coverage of the axes keyed on
// nodes alone, which their own assertions own.
func TestTheRequiredSlotRuleIsInvokedOncePerSlotAComponentTypeDeclares(t *testing.T) {
	out, catalogLookup := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, readAggregate(t))
	coordinates := coordinatesOf(t, out, catalogLookup)

	declared := slotsTheGraphHolds(t, out, catalogLookup)
	if len(declared) < 2 {
		t.Fatalf("the workspace declares %d attachment slot(s); a per-slot count cannot fail on fewer than two", len(declared))
	}
	// The slot axis is reached through a node walk, so it can be narrowed in two
	// independent places. A yardstick sitting entirely on one node would leave a
	// narrowed node axis undetectable.
	if spread := nodesCarryingASlot(declared); spread < 2 {
		t.Fatalf("every declared slot sits on %d node(s); a narrowed node axis would be invisible", spread)
	}

	var enumerated int
	for _, node := range coordinates.Nodes() {
		enumerated += len(coordinates.Slots(node.ID))
	}
	if enumerated != len(declared) {
		t.Errorf("the enumeration yields %d slot(s) where the component types declare %d", enumerated, len(declared))
	}

	var visited []string
	if _, err := constraint.RunOnNodeSlots(
		context.Background().Err,
		coordinates,
		coordinates,
		out.GraphView,
		countingRequiredSlotRule(&visited),
	); err != nil {
		t.Fatalf("traversal: %v", err)
	}

	if len(visited) != len(declared) {
		t.Errorf("the rule was invoked %d time(s) for %d declared slot(s)", len(visited), len(declared))
	}
	for _, slot := range declared {
		if !slices.Contains(visited, slot) {
			t.Errorf("the rule was never invoked for the slot %q the component type declares", slot)
		}
	}
	for _, slot := range visited {
		if !slices.Contains(declared, slot) {
			t.Errorf("the rule was invoked for %q, which no component type declares", slot)
		}
	}

	// The half that binds the PRODUCTION call to the same yardstick. The
	// assertions above run the traversal themselves, so they establish what the
	// axis holds and say nothing about what the build handed the rule; a build
	// that passed a narrower enumeration would leave every one of them green.
	unbound := unboundRequiredSlotsTheGraphHolds(t, out, catalogLookup)
	if len(unbound) < 2 {
		t.Fatalf("the workspace leaves %d required slot(s) unbound; a per-finding count cannot fail on fewer than two", len(unbound))
	}
	wantLocators := make([]string, 0, len(unbound))
	for _, coordinate := range unbound {
		node, slot, _ := strings.Cut(coordinate, "|")
		wantLocators = append(wantLocators, ".spec.components."+node+".features."+slot)
	}
	got := locatorsOf(out.Analysis.Errors)
	if len(got) != len(wantLocators) {
		t.Errorf("the build reported %d violation(s) for %d unbound required slot(s): %v", len(got), len(wantLocators), got)
	}
	for _, want := range wantLocators {
		if !slices.Contains(got, want) {
			t.Errorf("the build reported nothing at %q, the authoring key of a required slot nothing binds", want)
		}
	}
}

// TestTheRequiredSlotRuleHaltsThroughTheViolationChannel asserts the findings
// reach the slice the build gates on rather than the advisory stream, and that
// they carry what a caller matches and positions on.
//
// The expectations are the ones the reading this replaced produced on the same
// document, with three measured differences carried deliberately: the locator now
// names the slot's own authoring key under the component rather than the
// component alone, the source pointer of the converted fault is now the document
// rather than the violating node's identifier, and the message is the rule's own
// registered wording with the values it read rather than a sentence composed
// inside the reading.
func TestTheRequiredSlotRuleHaltsThroughTheViolationChannel(t *testing.T) {
	t.Run("every unbound required slot reaches the violation slice", func(t *testing.T) {
		out, _ := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, readAggregate(t))

		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
		}
		if len(out.Analysis.Errors) != 2 {
			t.Fatalf("violations = %d, want 2 (one per unbound required slot): %v", len(out.Analysis.Errors), out.Analysis.Errors)
		}

		wantLocators := []string{
			".spec.components.backend.features." + slotName,
			".spec.components.frontend.features." + slotName,
		}
		wantTypes := []string{"core/compute/api-server", "core/compute/web-ui"}
		for i, violation := range out.Analysis.Errors {
			if !errors.Is(violation.Sentinel, analyzer.ErrRequiredSlotUnfilled()) {
				t.Errorf("violation %d does not carry the rule's identity: %v", i, violation.Sentinel)
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
			// Both coordinates are in the text, because the editor renders the
			// message and recovers position as a range rather than reading the
			// locator as words. Naming only the component type would leave two
			// findings about two slots on two instances of one type reading as
			// the same string there.
			if !strings.Contains(violation.Message, wantTypes[i]) ||
				!strings.Contains(violation.Message, slotName) {
				t.Errorf("violation %d names neither the declaring component type nor the slot: %q", i, violation.Message)
			}
		}

	})

	t.Run("two unbound slots on one component read as two different findings", func(t *testing.T) {
		// The shape the slot name in the message is load-bearing on, and the only
		// one on which its absence is detectable. On this workspace's two nodes the
		// component types differ, so a pair of findings taken across nodes is
		// distinguishable by the type alone and would stay distinguishable with the
		// slot name removed — an assertion over that pair would be inert. Two slots
		// on ONE component share the type and differ in nothing else.
		out, catalogLookup := compileRequiredSlotWorkspace(t, twoRequiredSlotsDeclaration, readAggregate(t))

		perNode := map[string][]analyzer.AnalysisError{}
		for _, violation := range out.Analysis.Errors {
			node, _, ok := strings.Cut(strings.TrimPrefix(violation.Field, ".spec.components."), ".")
			if !ok {
				t.Fatalf("violation locator %q does not name a component", violation.Field)
			}
			perNode[node] = append(perNode[node], violation)
		}

		var compared int
		for node, violations := range perNode {
			if len(violations) < 2 {
				continue
			}
			compared++
			for i := 1; i < len(violations); i++ {
				if violations[i].Error() == violations[0].Error() {
					t.Errorf("two findings about two different slots on component %q read identically: %q",
						node, violations[0].Error())
				}
			}
		}
		if compared == 0 {
			t.Fatalf("no component carries two findings, so nothing was compared; the workspace reported %d violation(s) over %d component(s) for a catalog declaring two required slots on every entity",
				len(out.Analysis.Errors), len(perNode))
		}
		// And the pair really is the whole declared set, so the comparison above is
		// not running on a subset something narrowed.
		if want := len(unboundRequiredSlotsTheGraphHolds(t, out, catalogLookup)); len(out.Analysis.Errors) != want {
			t.Errorf("the build reported %d violation(s) for %d unbound required slot(s)", len(out.Analysis.Errors), want)
		}
	})

	t.Run("a violation converts to a developer fault pointing at its document", func(t *testing.T) {
		out, _ := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, readAggregate(t))
		if len(out.Analysis.Errors) == 0 {
			t.Fatal("no violation to convert")
		}
		converted := out.Analysis.Errors[0].ToCompilerError()
		if converted.Category() != cerrors.DeveloperError {
			t.Errorf("category = %v, want %v", converted.Category(), cerrors.DeveloperError)
		}
		if converted.Source().File != invocationAggregatePath {
			t.Errorf("source pointer = %q, want the document the finding is about %q", converted.Source().File, invocationAggregatePath)
		}
		if !errors.Is(converted, analyzer.ErrRequiredSlotUnfilled()) {
			t.Error("the converted fault does not carry the rule's identity")
		}
	})

	t.Run("a document binding every required slot reports nothing", func(t *testing.T) {
		out, _ := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, fillBothSlots(t, readAggregate(t)))
		if len(out.Analysis.Errors) != 0 {
			t.Errorf("violations on a document that binds every required slot: %v", out.Analysis.Errors)
		}
		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("advisories on a document that binds every required slot: %v", out.Analysis.Warnings)
		}
	})

	t.Run("an unbound slot the entity does not declare required reports nothing", func(t *testing.T) {
		// The same document and the same absent binding, differing from the
		// reporting case in the declaration alone. Without it, a rule reporting
		// every unbound slot would satisfy every assertion above.
		out, _ := compileRequiredSlotWorkspace(t, optionalSlotDeclaration, readAggregate(t))
		if len(out.Analysis.Errors) != 0 {
			t.Errorf("violations on an unbound slot the entity declares optional: %v", out.Analysis.Errors)
		}
	})
}

// TestTheRequiredSlotRuleStopsPartwayOnACancelledWalk asserts the property a
// cancellation signal exists for, which is not the same as asserting an error
// came back.
//
// A walk that ran to completion and reported the cancellation afterwards returns
// exactly the same error as one that stopped, so the error alone distinguishes
// nothing. What distinguishes them is how many slots the rule was invoked for.
//
// The granularity is per node, not per slot: the traversal reads its signal as
// the first statement of the node loop and then walks that node's whole slot
// list. That is the same granularity the reading this replaced had — its own
// signal read sat in its outer loop over node ids — so the proof is written
// against per-node stopping rather than against a finer promise nothing makes.
func TestTheRequiredSlotRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	out, catalogLookup := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, readAggregate(t))
	coordinates := coordinatesOf(t, out, catalogLookup)

	declared := slotsTheGraphHolds(t, out, catalogLookup)
	share := len(coordinates.Slots(coordinates.Nodes()[0].ID))
	// Both bounds are what keep the assertion falsifiable. A first node carrying
	// no slot makes a stopped walk read as zero, which a walk that never ran also
	// reads as; a first node carrying all of them makes a stopped walk
	// indistinguishable from a complete one.
	if share == 0 || share == len(declared) {
		t.Fatalf("the first node carries %d of the workspace's %d slot(s); stopping partway is indistinguishable from not running or from running to the end", share, len(declared))
	}

	t.Run("the walk stops on the node after the signal turns", func(t *testing.T) {
		stop := errors.New("the caller withdrew")
		reads := 0
		signal := func() error {
			reads++
			if reads > 1 {
				return stop
			}
			return nil
		}

		var visited []string
		objections, err := constraint.RunOnNodeSlots(
			constraint.Cancellation(signal),
			coordinates,
			coordinates,
			out.GraphView,
			countingRequiredSlotRule(&visited),
		)
		if !errors.Is(err, stop) {
			t.Fatalf("traversal error = %v, want the caller's own reason", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every slot and reported at the end,
		// which is the reading this rules out.
		if len(visited) != share {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly %d, the first node's own share of %d",
				len(visited), share, len(declared))
		}
		if len(objections) != 0 {
			t.Errorf("a cancelled walk handed back %d finding(s); a truncated set is not a result", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every declared slot and reports every unbound one", func(t *testing.T) {
		var visited []string
		objections, err := constraint.RunOnNodeSlots(
			nil,
			coordinates,
			coordinates,
			out.GraphView,
			countingRequiredSlotRule(&visited),
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(visited) != len(declared) {
			t.Errorf("the rule was invoked %d time(s) for %d declared slot(s)", len(visited), len(declared))
		}
		// The control carries the finding count too, so the cancelled leg above
		// cannot be satisfied by a walk that ran everywhere and raised nothing.
		if want := len(unboundRequiredSlotsTheGraphHolds(t, out, catalogLookup)); len(objections) != want {
			t.Errorf("the complete walk raised %d finding(s) for %d unbound required slot(s)", len(objections), want)
		}
	})
}

// TestTheGraphStageRulesThreadTheCallersSignalIntoTheRequiredSlotWalk asserts the
// signal this walk reads is the caller's own, threaded through the function the
// build invokes it from — not one supplied at a proof's convenience.
//
// It cannot be taken by withdrawing the caller before the function is entered.
// The first walk that reads the signal returns and every walk after it is never
// reached, so a withdrawal made up front says nothing about the fourth one. The
// withdrawal here is therefore made *during* this walk, by the only thing the
// slot axis consults that a caller supplies: the component lookup it reads a
// component type's declared slots from, which nothing else in that function
// consults.
//
// The count of those lookups is what discriminates, and the returned error is
// not. Two walks consult the lookup — this one and the slot-constraint walk
// before it — once per node each, so a build whose fourth walk reads the caller's
// signal consults it once per node for the third walk, then once more for this
// walk's first node before the withdrawal is seen at its second: exactly one more
// than the graph holds nodes. A fourth walk handed some other signal consults it
// once per node again, twice the node count — and the function still returns the
// withdrawal, from the advisory walk after it. So the error alone cannot tell the
// two apart and the lookup count can.
func TestTheGraphStageRulesThreadTheCallersSignalIntoTheRequiredSlotWalk(t *testing.T) {
	out, catalogLookup := compileRequiredSlotWorkspace(t, requiredSlotDeclaration, readAggregate(t))

	nodes := out.IRGraph.Spec.Nodes.Len()
	// Below two nodes the two readings of the lookup count coincide: one walk's
	// worth plus one is the same number as two walks' worth.
	if nodes < 2 {
		t.Fatalf("the graph holds %d node(s); nodes+1 and 2*nodes are the same number", nodes)
	}
	before := len(out.Analysis.Errors)
	if before == 0 {
		t.Fatal("the spliced catalog produced no violation, so nothing distinguishes a stopped walk from a silent one")
	}

	withdrawn, withdraw := context.WithCancel(context.Background())
	defer withdraw()

	var lookups int
	watched := catalogLookup
	watched.Component = func(componentType string) (types.Component, bool) {
		lookups++
		// The withdrawal lands inside the FIRST consultation this walk makes,
		// which is one past the whole of the walk before it. So that walk runs to
		// completion, this walk enters its first node, and the signal it reads on
		// its second node is the one that turns.
		if lookups == nodes+1 {
			withdraw()
		}
		return catalogLookup.Component(componentType)
	}
	// The enumeration is a product the phase carries, and the catalog lookup it
	// reads is inside it — so the watched lookup has to reach it there. Rebuilding
	// the enumeration over the same graph and the same presentation changes nothing
	// a rule is asked; it changes only which lookup answers the slot axis.
	out.GraphCoordinates = ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, watched, releaseFileOf(out.ResolveResult), out.AttachmentSites, out.CarriedConnections,
	)

	if err := runGraphStageRules(withdrawn, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("the graph-stage rules returned %v for a caller that withdrew, want the withdrawal", err)
	}
	if lookups != nodes+1 {
		t.Errorf("the slot axis was consulted %d time(s) for a caller that withdrew during this walk's first node; want exactly %d — the walk before it over the graph's %d node(s), plus this walk's first",
			lookups, nodes+1, nodes)
	}
	if after := len(out.Analysis.Errors); after != before {
		t.Errorf("a stopped walk still appended %d violation(s)", after-before)
	}
}
