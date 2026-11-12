// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// The proofs in this file cover the label constraint an attachment slot declares
// over what may fill it, evaluated over the finalised graph. They sit in this
// package for the same reason the socket-constraint proofs beside them do: the
// presentation and the enumeration the rule needs are both carried on an
// unexported output, and a proof over an enumeration a test assembles proves the
// test's account of the graph rather than the compiler's.
//
// # Why this rule's fixture is spliced on two surfaces rather than one
//
// A slot's constraint is declared on the component catalog entity and the fill
// that fails it is declared in the Aggregate, so making the rule raise needs
// both. No committed workspace declares a slot carrying a `selector` at all, so
// a proof compiling one unspliced would exercise a rule that ends on its first
// condition for every subject — which reports exactly what a satisfied rule
// reports. The catalog half is written into a temporary copy so the committed
// fixture keeps producing what every other proof over it expects, and the
// Aggregate half is spliced in memory for the same reason.

// slotName is the attachment slot the splices below declare and fill. It is a
// name no committed entity uses, so a fixture that stopped carrying the splice
// would fail the anchor check rather than silently reuse something else.
const slotName = "audit"

// slotFillType is the Feature the splices bind to the slot. It declares no
// applicability of its own, so the rule under proof is the only one its
// attachment can make raise.
const slotFillType = "core/proof-injector"

// catalogDeclaringSlots copies the fixture catalog into a temporary tree and
// gives the two component types the Aggregate's own components are declared
// with the supplied attachment-slot schema.
//
// Two entities rather than one, and that is what every enumeration and
// cancellation proof over the slot axis rests on: a slot axis whose every member
// sits on one node cannot show a narrowed node axis, and a per-node walk stopped
// after the first node is arithmetically indistinguishable from a complete one.
//
// It is shared by the proofs of both rules keyed on a slot — the one about the
// constraint a slot declares over its fill, and the one about a slot the entity
// declares required — because the two need the same splice and differ only in what
// the slots declare. A second copy of the copy-and-splice mechanism would agree
// with this one on the day it was written and be free to drift afterwards, with
// nothing comparing them.
//
// The body is the whole schema, slot keys included, indented to the depth those
// keys sit at. A caller therefore reads the YAML it is writing, and a proof
// needing more than one slot writes more than one rather than asking this helper
// for a shape it decides.
func catalogDeclaringSlots(t *testing.T, featuresSchemaBody string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(root, os.DirFS(invocationCatalogPath)); err != nil {
		t.Fatalf("copy catalog: %v", err)
	}

	slot := "spec:\n" +
		"  featuresSchema:\n" +
		featuresSchemaBody
	for _, entity := range []string{"api-server.yaml", "web-ui.yaml"} {
		path := filepath.Join(root, "base", "components", entity)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entity, err)
		}
		const anchor = "spec:\n"
		if !bytes.Contains(source, []byte(anchor)) {
			t.Fatalf("the entity %s no longer opens a %q block; the rewrite would be silently inert", entity, anchor)
		}
		if err := os.WriteFile(path, bytes.Replace(source, []byte(anchor), []byte(slot), 1), 0o600); err != nil {
			t.Fatalf("write %s: %v", entity, err)
		}
	}
	return root
}

// catalogWithASlotConstraint declares the shared slot carrying the supplied label
// constraint, and labels the Feature that fills it.
//
// The label lives here rather than in the shared splice above because it is one
// half of the comparison THIS rule performs: the constraint and the labels it is
// evaluated against are decided by one mechanism, so a proof cannot end up
// asserting against a pairing nothing produced.
func catalogWithASlotConstraint(t *testing.T, declaration string) string {
	t.Helper()
	root := catalogDeclaringSlots(t,
		"    "+slotName+":\n"+
			"      required: false\n"+
			"      selector: "+declaration+"\n")

	path := filepath.Join(root, "base", "features", "proof-injector.yaml")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read feature entity: %v", err)
	}
	const anchor = "  version: \"1.0.0\"\n"
	if !bytes.Contains(source, []byte(anchor)) {
		t.Fatalf("the feature entity no longer carries %q; the rewrite would be silently inert", anchor)
	}
	labelled := bytes.Replace(source, []byte(anchor),
		[]byte(anchor+"  labels:\n    fill.aggrega.ai/grade: \"managed\"\n"), 1)
	if err := os.WriteFile(path, labelled, 0o600); err != nil {
		t.Fatalf("write feature entity: %v", err)
	}
	return root
}

// fillBothSlots binds the declared slot on both of the Aggregate's own
// components.
//
// Two fills rather than one, because a proof that the rule reports every
// offending slot cannot be taken on a document with one offence — a reading that
// returned after the first would pass it.
func fillBothSlots(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	fill := "      features:\n        " + slotName + ":\n          type: " + slotFillType + "\n"
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

// compileSlotWorkspace compiles the fixture against a catalog declaring the
// supplied slot constraint.
func compileSlotWorkspace(
	t *testing.T,
	declaration string,
	aggregate []byte,
) (*stepsOneToFourOutput, resolver.CatalogLookup) {
	t.Helper()
	return compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   catalogWithASlotConstraint(t, declaration),
	}, aggregate)
}

// The two constraints the proofs below compile against. The Feature filling the
// slot carries the managed grade, so one is satisfied and the other is not, and
// the pair differs in nothing else.
const (
	satisfiedSlotConstraint = `"fill.aggrega.ai/grade=managed"`
	offendedSlotConstraint  = `"fill.aggrega.ai/grade=hardened"`
)

// countingSlotFillRule is the declared rule with a counter around its predicate,
// so a proof can assert how many times it was invoked and for which coordinates.
// It delegates rather than reimplementing, so what is counted is the rule itself.
func countingSlotFillRule(
	visited *[]string,
) constraint.Check[constraint.GraphView, constraint.NodeSlotSubject] {
	rule := analyzer.SlotFillSatisfiesSlotSelector
	return constraint.Check[constraint.GraphView, constraint.NodeSlotSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
			*visited = append(*visited, fmt.Sprintf("%s|%s", s.NodeID(), s.SlotName()))
			return rule.Run(v, s)
		},
	}
}

// slotsTheGraphHolds is the (node, slot) coordinate of every attachment slot the
// component type of every node in the compiled graph declares. It is the
// yardstick the invocation count is measured against, so it cannot be a list the
// proof writes down.
//
// It is derived by a different route from the one the enumeration takes: the
// component type is read from the graph's own node record, where the enumeration
// reads it through the presentation's accessor. Two derivations that could
// disagree are what make the comparison mean something; one derivation compared
// with itself would agree however narrow it was.
func slotsTheGraphHolds(
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
		for _, slot := range component.Spec.FeaturesSchema.Keys() {
			held = append(held, fmt.Sprintf("%s|%s", id, slot))
		}
	}
	return held
}

// armCaptions is the caption the rule put on each of a finding's detail values,
// in the order the rule supplied them and empty where the rule left a value
// uncaptioned.
//
// The captions are not exposed on their own, so they are recovered from the
// rendered form, which is the caption, a space and the quoted value — and the
// unrendered value sits beside it, so the caption is what is left when the quoted
// value comes off the end. It fails rather than returning a prefix it could not
// account for: a rendered element not ending in its own quoted value means the
// spelling convention moved, and a silently truncated caption would then read as a
// discriminator that no longer discriminates.
func armCaptions(t *testing.T, objection constraint.Objection) []string {
	t.Helper()
	detail, rendered := objection.Detail(), objection.RenderedDetail()
	if len(detail) != len(rendered) {
		t.Fatalf("the finding carries %d detail value(s) and %d rendered one(s); the two cannot be read against each other", len(detail), len(rendered))
	}
	captions := make([]string, 0, len(detail))
	for i, value := range detail {
		quoted := fmt.Sprintf("%q", value)
		if rendered[i] == quoted {
			captions = append(captions, "")
			continue
		}
		caption, captioned := strings.CutSuffix(rendered[i], " "+quoted)
		if !captioned {
			t.Fatalf("the rendered detail %q does not end in the quoted value %q it was rendered from", rendered[i], quoted)
		}
		captions = append(captions, caption)
	}
	return captions
}

// armDiscriminators is what tells two violation classes of one rule apart, read
// off a finding that rule raised: the advice, which names the edit this
// occurrence calls for, and the captions, which name what each value the finding
// carries is.
//
// Neither the sentinel nor the number of detail values does that job. Two classes
// of one rule share the sentinel by construction, and a class raising a value the
// other does not carry is the exception rather than the rule — so a count is
// equally the same on both sides of the discrimination, and a proof reading the
// classes apart by it is reading something the rule never promised.
type armDiscriminators struct {
	advice   string
	captions []string
}

// discriminatorsOf asserts a finding carries the advice and the captions the
// violation class it came from is meant to carry, and returns them.
//
// It returns them so one class's pair can be compared against another's, which is
// the assertion a per-class expectation structurally cannot make: a rule that gave
// two classes one advice and one caption would satisfy expectations written to
// match it, and its classes would then be indistinguishable to every caller while
// the proof stayed green. A proof driving one class alone has nothing to compare
// against and discards the result.
func discriminatorsOf(
	t *testing.T,
	objection constraint.Objection,
	wantCaptions []string,
	wantAdvice string,
) armDiscriminators {
	t.Helper()
	got := armDiscriminators{advice: objection.Advice(), captions: armCaptions(t, objection)}
	if got.advice != wantAdvice {
		t.Errorf("advice = %q, want the edit this violation class calls for: %q", got.advice, wantAdvice)
	}
	if !slices.Equal(got.captions, wantCaptions) {
		t.Errorf("captions = %q, want %q", got.captions, wantCaptions)
	}
	return got
}

// nodesCarryingASlot counts the distinct nodes the yardstick spreads over.
func nodesCarryingASlot(held []string) int {
	seen := map[string]bool{}
	for _, coordinate := range held {
		seen[strings.SplitN(coordinate, "|", 2)[0]] = true
	}
	return len(seen)
}

// TestTheSlotFillRuleIsInvokedOncePerSlotAComponentTypeDeclares asserts the
// subject set the rule is asked about is the whole declared slot set, by count
// and by coordinate.
//
// What it excludes, and what nothing else here can: an enumeration that yields
// fewer slots than the component types declare. Such an enumeration leaves the
// rule silently uninvoked over the slots it omits, and a comparison against a
// second reading, a clean-input control, and any content assertion about a slot
// the omission did not reach all still pass — a rule asked about nothing reports
// exactly what a rule asked and satisfied reports. So the count is compared
// against a yardstick read off the graph and the catalog, the missing
// coordinates are named rather than counted, and the fixture is required to hold
// enough slots, spread over enough nodes, for the comparison to be able to fail.
//
// One bound is worth stating rather than leaving a reader to over-read this. The
// slot axis is reached through the node walk, so a narrowed node axis is caught
// here only when the node it drops carries a slot; dropping a node that declares
// none removes no subject this rule would have been invoked for, and the count
// is right to stay green. What that narrowing costs is coverage of the axes
// keyed on nodes alone, which their own assertions own.
func TestTheSlotFillRuleIsInvokedOncePerSlotAComponentTypeDeclares(t *testing.T) {
	out, catalogLookup := compileSlotWorkspace(t, satisfiedSlotConstraint, fillBothSlots(t, readAggregate(t)))
	coordinates := coordinatesOf(t, out, catalogLookup)

	held := slotsTheGraphHolds(t, out, catalogLookup)
	if len(held) < 2 {
		t.Fatalf("the workspace declares %d attachment slot(s); a per-slot count cannot fail on fewer than two", len(held))
	}
	// The slot axis is reached through a node walk, so it can be narrowed in two
	// independent places. A yardstick sitting entirely on one node would leave a
	// narrowed node axis undetectable.
	if spread := nodesCarryingASlot(held); spread < 2 {
		t.Fatalf("every declared slot sits on %d node(s); a narrowed node axis would be invisible", spread)
	}

	var enumerated int
	for _, node := range coordinates.Nodes() {
		enumerated += len(coordinates.Slots(node.ID))
	}
	if enumerated != len(held) {
		t.Errorf("the enumeration yields %d slot(s) where the component types declare %d", enumerated, len(held))
	}

	var visited []string
	if _, err := constraint.RunOnNodeSlots(
		context.Background().Err,
		coordinates,
		coordinates,
		out.GraphView,
		countingSlotFillRule(&visited),
	); err != nil {
		t.Fatalf("traversal: %v", err)
	}

	if len(visited) != len(held) {
		t.Errorf("the rule was invoked %d time(s) for %d declared slot(s)", len(visited), len(held))
	}
	for _, slot := range held {
		if !slices.Contains(visited, slot) {
			t.Errorf("the rule was never invoked for the slot %q the component type declares", slot)
		}
	}
	for _, slot := range visited {
		if !slices.Contains(held, slot) {
			t.Errorf("the rule was invoked for %q, which no component type declares", slot)
		}
	}
}

// TestTheSlotFillRuleIsAskedAboutASlotNothingBinds covers the arm the reading
// this replaced structurally could not reach.
//
// That reading walked the node's attachments and kept the ones whose name
// matched a declared slot, so an unbound slot never became a subject at all. The
// declared axis is the set the component type declares, so the rule is asked and
// ends on the unbound case without raising — which makes its silence there a
// decision rather than an omission.
func TestTheSlotFillRuleIsAskedAboutASlotNothingBinds(t *testing.T) {
	// The unspliced Aggregate binds nothing under the declared slot name.
	out, catalogLookup := compileSlotWorkspace(t, offendedSlotConstraint, readAggregate(t))
	coordinates := coordinatesOf(t, out, catalogLookup)

	var unbound int
	for _, node := range coordinates.Nodes() {
		bound := out.GraphView.MergedAttachmentNames(node.ID)
		for _, slot := range coordinates.Slots(node.ID) {
			if !slices.Contains(bound, slot) {
				unbound++
			}
		}
	}
	if unbound == 0 {
		t.Fatal("every declared slot is bound, so the unbound arm is not exercised at all")
	}

	var visited []string
	objections, err := constraint.RunOnNodeSlots(
		context.Background().Err,
		coordinates,
		coordinates,
		out.GraphView,
		countingSlotFillRule(&visited),
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(visited) < unbound {
		t.Errorf("the rule was invoked %d time(s) for %d unbound slot(s); an unbound slot was not presented to it", len(visited), unbound)
	}
	// A constraint the fill would fail, over slots nothing fills: the rule ends
	// on the binding question rather than judging an absent fill against it.
	if len(objections) != 0 {
		t.Errorf("an unbound slot was reported against its constraint: %v", objections)
	}
	if len(out.Analysis.Errors) != 0 {
		t.Errorf("the build gated on an unbound slot: %v", out.Analysis.Errors)
	}
}

// TestTheSlotFillRuleHaltsThroughTheViolationChannel asserts the findings reach
// the slice the build gates on rather than the advisory stream, and that they
// carry what a caller matches and positions on.
//
// The expectations are the ones the reading this replaced produced on the same
// document, with three measured differences carried deliberately: the locator
// now names the offending authoring key where the reading had none at all, the
// document is now carried where the reading carried none, and the message is the
// rule's own registered wording with the fact values it read rather than a
// sentence composed inside the reading.
func TestTheSlotFillRuleHaltsThroughTheViolationChannel(t *testing.T) {
	filled := fillBothSlots(t, readAggregate(t))

	t.Run("every offending fill reaches the violation slice", func(t *testing.T) {
		out, _ := compileSlotWorkspace(t, offendedSlotConstraint, filled)

		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
		}
		if len(out.Analysis.Errors) != 2 {
			t.Fatalf("violations = %d, want 2 (one per offending fill): %v", len(out.Analysis.Errors), out.Analysis.Errors)
		}

		wantLocators := []string{
			".spec.components.backend.features." + slotName,
			".spec.components.frontend.features." + slotName,
		}
		for i, violation := range out.Analysis.Errors {
			if !errors.Is(violation.Sentinel, analyzer.ErrSlotSelectorUnsatisfied()) {
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
			// The offending values the catalog and the compiled graph supplied.
			if !strings.Contains(violation.Message, "fill.aggrega.ai/grade=hardened") ||
				!strings.Contains(violation.Message, slotFillType) {
				t.Errorf("violation %d names neither the declared constraint nor the fill: %q", i, violation.Message)
			}
		}
	})

	t.Run("a violation converts to a developer fault pointing at its document", func(t *testing.T) {
		out, _ := compileSlotWorkspace(t, offendedSlotConstraint, filled)
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
		if !errors.Is(converted, analyzer.ErrSlotSelectorUnsatisfied()) {
			t.Error("the converted fault does not carry the rule's identity")
		}
	})

	t.Run("a document whose fills satisfy every slot constraint reports nothing", func(t *testing.T) {
		out, _ := compileSlotWorkspace(t, satisfiedSlotConstraint, filled)
		if len(out.Analysis.Errors) != 0 {
			t.Errorf("violations on a satisfying document: %v", out.Analysis.Errors)
		}
		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("advisories on a satisfying document: %v", out.Analysis.Warnings)
		}
	})
}

// TestTheSlotFillRuleReportsAConstraintItCannotEvaluate covers the second thing
// the rule reports: a declared constraint the label matcher cannot parse at all,
// as against one it parses and finds unsatisfied.
//
// The two arms share one sentinel and carry the same number of detail values, so
// neither the identity nor the count separates them. What does is the pair the
// framework carries per occurrence: the advice, which names a different edit for
// each arm — correct the declaration, versus give the fill the labels it is asked
// for — and the caption the rule puts on the value that differs, which says which
// of the two things that value is. So each leg pins both, and no leg infers an arm
// from how long the detail list is.
//
// Both arms are asserted from one mechanism, because the evaluated arm is what
// stops the unevaluable one being satisfied by a rule that reports on everything.
// And the two arms' discriminators are compared with each other once both have
// been read, which is the assertion a per-leg pin structurally cannot make: a rule
// that gave both arms one advice and one caption would satisfy per-leg
// expectations written to match it, and the arms would then be indistinguishable
// to every caller while the proof stayed green.
func TestTheSlotFillRuleReportsAConstraintItCannotEvaluate(t *testing.T) {
	filled := fillBothSlots(t, readAggregate(t))

	legs := []struct {
		name         string
		declaration  string
		wantCaptions []string
		wantAdvice   string
	}{
		{
			name:         "a constraint the matcher rejects names the reason it could not be evaluated",
			declaration:  `"!"`,
			wantCaptions: []string{"", "declared on slot", "which could not be evaluated:"},
			wantAdvice:   "Correct the selector on the slot's declaration in the component's catalog entity.",
		},
		{
			name:         "a constraint the matcher evaluates and finds unsatisfied names the fill that failed it",
			declaration:  offendedSlotConstraint,
			wantCaptions: []string{"", "declared on slot", "not satisfied by the labels of the Feature filling it,"},
			wantAdvice: "Add the labels the slot's selector requires to the Feature's labels, " +
				"or fill the slot with a Feature that already carries them.",
		},
	}

	var read []armDiscriminators
	for _, leg := range legs {
		t.Run(leg.name, func(t *testing.T) {
			out, catalogLookup := compileSlotWorkspace(t, leg.declaration, filled)
			objections, err := constraint.RunOnNodeSlots(
				context.Background().Err,
				coordinatesOf(t, out, catalogLookup),
				coordinatesOf(t, out, catalogLookup),
				out.GraphView,
				analyzer.SlotFillSatisfiesSlotSelector,
			)
			if err != nil {
				t.Fatalf("traversal: %v", err)
			}
			if len(objections) != 2 {
				t.Fatalf("objections = %d, want one per offending fill", len(objections))
			}
			// A content check and no longer the switch. Both arms carry the
			// constraint, the slot it is declared on, and one value naming what went
			// wrong with it, so this number is the same on both sides of the
			// discrimination and cannot perform it.
			detail := objections[0].Detail()
			if len(detail) != 3 {
				t.Fatalf("detail = %v, want the constraint, the slot and the value naming what went wrong", detail)
			}
			if detail[len(detail)-1] == "" {
				t.Errorf("the value this arm's own caption names is empty: %v", detail)
			}
			read = append(read, discriminatorsOf(t, objections[0], leg.wantCaptions, leg.wantAdvice))

			violations := analyzer.ErrorsFromObjections(objections)
			if len(violations) != 2 || violations[0].Field != ".spec.components.backend.features."+slotName {
				t.Errorf("violations = %+v, want two positioned on the offending slots", violations)
			}
		})
	}

	// The cross-arm comparison, which is what the per-leg pins above cannot do.
	// It is stated as a failure rather than skipped when an arm did not report,
	// because a comparison over one arm is vacuous and reads exactly like a
	// comparison that passed.
	if len(read) != len(legs) {
		t.Fatalf("%d of the %d arms yielded a discriminator; comparing them would be vacuous", len(read), len(legs))
	}
	for i := range read {
		for j := i + 1; j < len(read); j++ {
			if read[i].advice == read[j].advice {
				t.Errorf("the arms %q and %q carry one advice, %q; the rule tells its own violation classes apart by it",
					legs[i].name, legs[j].name, read[i].advice)
			}
			if slices.Equal(read[i].captions, read[j].captions) {
				t.Errorf("the arms %q and %q carry one caption list, %q; the rule tells its own violation classes apart by it",
					legs[i].name, legs[j].name, read[i].captions)
			}
		}
	}
}

// TestTheSlotFillRuleStopsPartwayOnACancelledWalk asserts the property a
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
func TestTheSlotFillRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	out, catalogLookup := compileSlotWorkspace(t, satisfiedSlotConstraint, fillBothSlots(t, readAggregate(t)))
	coordinates := coordinatesOf(t, out, catalogLookup)

	held := slotsTheGraphHolds(t, out, catalogLookup)
	share := len(coordinates.Slots(coordinates.Nodes()[0].ID))
	// Both bounds are what keep the assertion falsifiable. A first node carrying
	// no slot makes a stopped walk read as zero, which a walk that never ran also
	// reads as; a first node carrying all of them makes a stopped walk
	// indistinguishable from a complete one.
	if share == 0 || share == len(held) {
		t.Fatalf("the first node carries %d of the workspace's %d slot(s); stopping partway is indistinguishable from not running or from running to the end", share, len(held))
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
			countingSlotFillRule(&visited),
		)
		if !errors.Is(err, stop) {
			t.Fatalf("traversal error = %v, want the caller's own reason", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every slot and reported at the end,
		// which is the reading this rules out.
		if len(visited) != share {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly %d, the first node's own share of %d",
				len(visited), share, len(held))
		}
		if len(objections) != 0 {
			t.Errorf("a cancelled walk handed back %d finding(s); a truncated set is not a result", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every declared slot", func(t *testing.T) {
		var visited []string
		if _, err := constraint.RunOnNodeSlots(
			nil,
			coordinates,
			coordinates,
			out.GraphView,
			countingSlotFillRule(&visited),
		); err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(visited) != len(held) {
			t.Errorf("the rule was invoked %d time(s) for %d declared slot(s)", len(visited), len(held))
		}
	})
}

// TestTheGraphStageRulesThreadTheCallersSignalIntoTheSlotWalk asserts the signal
// the slot walk reads is the caller's own, threaded through the function the
// build invokes it from — not one supplied at a proof's convenience.
//
// It cannot be taken the way the two walks before it take theirs. Those withdraw
// the caller before the function is entered, and the first walk that reads the
// signal returns; every walk after it is then never reached, so a withdrawal
// made up front says nothing about this one. The withdrawal here is therefore
// made *during* the slot walk, by the only thing the walk consults that a caller
// can supply: the component lookup the slot axis reads a component type's
// declared slots from, which nothing else in this function consults.
//
// The count of those lookups is what discriminates. The walk consults the lookup
// once per node, after that node's signal read, so a walk reading the caller's
// signal consults it exactly once — the first node's, before the withdrawal is
// seen at the second. A walk handed a signal that never turns consults it once
// per node, and the function still returns the withdrawal, from a later walk. So
// the returned error alone cannot tell the two apart and the lookup count can.
func TestTheGraphStageRulesThreadTheCallersSignalIntoTheSlotWalk(t *testing.T) {
	out, catalogLookup := compileSlotWorkspace(t, offendedSlotConstraint, fillBothSlots(t, readAggregate(t)))

	nodes := out.IRGraph.Spec.Nodes.Len()
	if nodes < 2 {
		t.Fatalf("the graph holds %d node(s); one consultation and one per node are the same number", nodes)
	}
	before := len(out.Violations)
	if before == 0 {
		t.Fatal("the spliced document produced no violation, so nothing distinguishes a stopped walk from a silent one")
	}

	withdrawn, withdraw := context.WithCancel(context.Background())
	defer withdraw()

	var lookups int
	watched := catalogLookup
	watched.Component = func(componentType string) (types.Component, bool) {
		lookups++
		// The withdrawal lands inside the first node's own consultation, so the
		// walk has already read the signal once and gets it on the next node.
		withdraw()
		return catalogLookup.Component(componentType)
	}
	// The enumeration is a product the phase carries, and the catalog lookup it
	// reads is inside it — so the watched lookup has to reach it there. Rebuilding
	// the enumeration over the same graph and the same presentation changes nothing
	// a rule is asked; it changes only which lookup answers the slot axis.
	out.GraphCoordinates = ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, watched, releaseFileOf(out.ResolveResult), out.AttachmentSites, out.OriginatedConnections,
	)

	if err := runGraphStageRules(withdrawn, out.GraphScope); !errors.Is(err, context.Canceled) {
		t.Fatalf("the graph-stage rules returned %v for a caller that withdrew, want the withdrawal", err)
	}
	if lookups != 1 {
		t.Errorf("the slot axis was consulted %d time(s) for a caller that withdrew during the first node; want exactly 1 of the graph's %d node(s)",
			lookups, nodes)
	}
	if after := len(out.Violations); after != before {
		t.Errorf("a stopped walk still appended %d violation(s)", after-before)
	}
}
