// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
)

// The proofs in this file cover the applicability a Feature declares over what
// it may attach to, evaluated over the finalised graph. They sit in this
// package rather than beside the declaration because the presentation and the
// enumeration the rule needs are both carried on an unexported output, and
// reaching them is the point: a proof over an enumeration a test assembles
// proves the test's account of the graph rather than the compiler's.
//
// The workspace is a different one from the composed fixture the socket rule is
// proven over, and for a reason the axis makes unavoidable: that workspace
// carries one attachment on one component, and an axis with one coordinate
// cannot show a narrowing, a partial walk, or a rule reporting every offence
// rather than the first. This one attaches six Features to one component, each
// declaring a distinct applicability, so all three are observable.
const (
	applicabilityAggregatePath = "../../../internal/compiler/testdata/selector-surface/aggregate-attach.yaml"
	applicabilityReleasePath   = "../../../internal/compiler/testdata/selector-surface/release-attach.yaml"
	applicabilityManifestPath  = "../../../internal/compiler/testdata/selector-surface/manifest.yaml"
	applicabilityCatalogPath   = "../../../internal/compiler/testdata/selector-surface/catalog/base"

	// The document whose one attachment declares an applicability the label
	// matcher cannot parse at all. It is committed rather than spliced because
	// it is the shape the shipped negative corpus already carries.
	unevaluableAggregatePath = "../../../internal/compiler/testdata/selector-surface/aggregate-malformed.yaml"
	unevaluableReleasePath   = "../../../internal/compiler/testdata/selector-surface/release-malformed.yaml"
)

// applicabilityWorkspace names the four documents these proofs compile.
func applicabilityWorkspace(catalogPath string) workspaceFixture {
	return workspaceFixture{
		aggregatePath: applicabilityAggregatePath,
		releasePath:   applicabilityReleasePath,
		manifestPath:  applicabilityManifestPath,
		catalogPath:   catalogPath,
	}
}

// retypeTheAttachingComponent gives the component carrying the six attachments
// the other component type the fixture declares.
//
// The two types carry different labels, and three of the six attachments declare
// an applicability the second type's labels fail: one requires the first type's
// own type label, one requires the zone it sits in, and one excludes the zone the
// second type sits in. The other three still hold. So the splice makes the rule's
// subjects real attachments whose targets really do fail the applicability their
// Features really declare — three offences and three clean coordinates in one
// document, which is what a proof that the rule reports every offence needs. The
// splice is in-memory: the fixture on disk is shared with other proofs and must
// keep producing exactly what they expect.
func retypeTheAttachingComponent(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const anchor = "    web:\n      type: core/web\n"
	const retyped = "    web:\n      type: core/worker\n"
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer declares %q the way the splice expects; the splice would be silently inert", "web")
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(retyped), 1)
}

// alsoAttachToTheSecondComponent gives the fixture's other component an
// attachment of its own.
//
// It is what makes a partial walk observable. The signal is read once per node,
// so a workspace whose attachments all hang off one component cannot tell a walk
// that stopped after that component from one that reached the end — both visited
// every attachment. With two components carrying attachments the two readings
// differ by the second component's share. The Feature attached here declares an
// applicability the second component satisfies, so the document stays clean.
func alsoAttachToTheSecondComponent(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const anchor = "    worker:\n      type: core/worker\n"
	const withAttachment = "    worker:\n      type: core/worker\n" +
		"      features:\n" +
		"        either:\n" +
		"          type: core/feat-anyof\n"
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer declares %q the way the splice expects; the splice would be silently inert", "worker")
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(withAttachment), 1)
}

// attachmentsTheGraphHolds is the coordinate pair of every attachment the
// compiled graph carries, read off the graph itself. It is the yardstick the
// invocation count is measured against, so it cannot be a list the proof writes
// down.
//
// It is restricted to the nodes the graph holds because that is the intersection
// both readings walk: an attachment set keyed by a node the node set does not
// carry has no component block for a locator to be named relative to.
func attachmentsTheGraphHolds(out *stepsOneToFourOutput) []string {
	var held []string
	for _, node := range out.IRGraph.Spec.Nodes.Keys() {
		attachments, carries := out.IRGraph.Spec.FeatureValuesMapping.Get(node)
		if !carries {
			continue
		}
		for _, attachment := range attachments.Keys() {
			held = append(held, fmt.Sprintf("%s|%s", node, attachment))
		}
	}
	return held
}

// nodesCarryingAttachments counts how many nodes contribute at least one
// coordinate to the axis, which is the granularity the cancellation signal is
// read at.
func nodesCarryingAttachments(out *stepsOneToFourOutput) int {
	var carrying int
	for _, node := range out.IRGraph.Spec.Nodes.Keys() {
		attachments, carries := out.IRGraph.Spec.FeatureValuesMapping.Get(node)
		if carries && attachments.Len() > 0 {
			carrying++
		}
	}
	return carrying
}

// countingApplicabilityRule is the declared rule with a counter around its
// predicate, so a proof can assert how many times it was invoked and for which
// coordinates. It delegates rather than reimplementing, so what is counted is
// the rule itself.
func countingApplicabilityRule(
	visited *[]string,
) constraint.Check[constraint.GraphView, constraint.NodeAttachmentSubject] {
	rule := analyzer.AttachmentSatisfiesFeatureApplicability
	return constraint.Check[constraint.GraphView, constraint.NodeAttachmentSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.GraphView, s constraint.NodeAttachmentSubject) []constraint.Objection {
			*visited = append(*visited, fmt.Sprintf("%s|%s", s.NodeID(), s.AttachmentName()))
			return rule.Run(v, s)
		},
	}
}

// catalogWithFeatureApplicability copies the fixture catalog into a temporary
// tree and rewrites one Feature entity's declared applicability to the supplied
// block. The copy is what keeps the committed fixture producing exactly what
// every other proof over it expects.
//
// The entity rewritten is the one the first attachment names, so a rewrite
// reaches exactly one of the six coordinates and the other five stay as
// authored.
func catalogWithFeatureApplicability(t *testing.T, declaration string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(root, os.DirFS(applicabilityCatalogPath)); err != nil {
		t.Fatalf("copy catalog: %v", err)
	}
	entity := filepath.Join(root, "features", "feat-equality.yaml")
	source, err := os.ReadFile(entity)
	if err != nil {
		t.Fatalf("read feature entity: %v", err)
	}
	const anchor = "  appliesTo: \"component.aggrega.ai/type=server\"\n"
	if !bytes.Contains(source, []byte(anchor)) {
		t.Fatalf("the entity no longer declares %q; the rewrite would be silently inert", anchor)
	}
	rewritten := bytes.Replace(source, []byte(anchor), []byte(declaration), 1)
	if err := os.WriteFile(entity, rewritten, 0o600); err != nil {
		t.Fatalf("write feature entity: %v", err)
	}
	return root
}

// findingsAboutTheRewrittenAttachment narrows a result to the one coordinate a
// catalog rewrite reaches, so a leg asserting on it is not counting what the
// five untouched attachments did.
func findingsAboutTheRewrittenAttachment(errs []analyzer.AnalysisError) []analyzer.AnalysisError {
	var out []analyzer.AnalysisError
	for _, e := range errs {
		if strings.HasSuffix(e.Field, ".features.eq") {
			out = append(out, e)
		}
	}
	return out
}

// TestTheFeatureApplicabilityRuleHaltsThroughTheViolationChannel asserts the
// findings reach the slice the build gates on rather than the advisory stream,
// and that they carry what a caller matches and positions on.
//
// The expectations are the ones the reading this replaced produced on the same
// document, with three measured differences carried deliberately: the locator
// now names the offending attachment key rather than being empty, the source
// pointer names the document rather than the violating node's identifier, and
// the message is the rule's own registered wording with the fact values it read
// rather than a sentence composed inside the reading.
func TestTheFeatureApplicabilityRuleHaltsThroughTheViolationChannel(t *testing.T) {
	fixture := applicabilityWorkspace(applicabilityCatalogPath)
	clean := bytesOf(t, applicabilityAggregatePath)

	t.Run("every offending attachment reaches the violation slice", func(t *testing.T) {
		out, _ := compileWorkspace(t, fixture, retypeTheAttachingComponent(t, clean))

		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
		}
		// Three of the six attachments declare an applicability the retyped
		// component fails. A reading that returned after the first would report
		// one, so the count is the property rather than a detail of it.
		if len(out.Analysis.Errors) != 3 {
			t.Fatalf("violations = %d, want 3 (one per offending attachment): %v", len(out.Analysis.Errors), out.Analysis.Errors)
		}

		wantLocators := []string{
			".spec.components.web.features.eq",
			".spec.components.web.features.zone-in",
			".spec.components.web.features.zone-notin",
		}
		for i, violation := range out.Analysis.Errors {
			if !errors.Is(violation.Sentinel, analyzer.ErrSelectorViolation()) {
				t.Errorf("violation %d does not carry the rule's identity: %v", i, violation.Sentinel)
			}
			if violation.Kind != analyzer.AnalysisErrorLocated {
				t.Errorf("violation %d kind = %q, want %q", i, violation.Kind, analyzer.AnalysisErrorLocated)
			}
			if violation.Field != wantLocators[i] {
				t.Errorf("violation %d locator = %q, want %q", i, violation.Field, wantLocators[i])
			}
			if violation.OriginFile != applicabilityAggregatePath {
				t.Errorf("violation %d document = %q, want %q", i, violation.OriginFile, applicabilityAggregatePath)
			}
		}

		// The offending values the compiled graph and the catalog supplied: the
		// Feature type the attachment resolved to, and the applicability that
		// Feature declares.
		first := out.Analysis.Errors[0].Message
		if !strings.Contains(first, "core/feat-equality") || !strings.Contains(first, "component.aggrega.ai/type=server") {
			t.Errorf("first violation names neither the attached Feature type nor its declared applicability: %q", first)
		}
		second := out.Analysis.Errors[1].Message
		if !strings.Contains(second, "core/feat-in") || !strings.Contains(second, "tier.aggrega.ai/zone in (frontend, edge)") {
			t.Errorf("second violation names neither the attached Feature type nor its declared applicability: %q", second)
		}
	})

	t.Run("a violation converts to a developer fault pointing at its document", func(t *testing.T) {
		out, _ := compileWorkspace(t, fixture, retypeTheAttachingComponent(t, clean))
		if len(out.Analysis.Errors) == 0 {
			t.Fatal("no violation to convert")
		}
		converted := out.Analysis.Errors[0].ToCompilerError()
		if converted.Category() != cerrors.DeveloperError {
			t.Errorf("category = %v, want %v", converted.Category(), cerrors.DeveloperError)
		}
		if converted.Source().File != applicabilityAggregatePath {
			t.Errorf("source pointer = %q, want the document the finding is about %q", converted.Source().File, applicabilityAggregatePath)
		}
		if !errors.Is(converted, analyzer.ErrSelectorViolation()) {
			t.Error("the converted fault does not carry the rule's identity")
		}
	})

	t.Run("a document every attachment applies to reports nothing", func(t *testing.T) {
		out, _ := compileWorkspace(t, fixture, clean)
		if len(out.Analysis.Errors) != 0 {
			t.Errorf("violations on a document every attachment applies to: %v", out.Analysis.Errors)
		}
		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("advisories on a document every attachment applies to: %v", out.Analysis.Warnings)
		}
	})
}

// TestTheFeatureApplicabilityRuleIsInvokedOncePerAttachmentTheGraphHolds
// asserts the subject set the rule is asked about is the graph's whole
// attachment set, by count and by coordinate.
//
// What it excludes, and what nothing else here can: an enumeration that yields
// fewer attachments than the graph holds, on either of its two axes. Such an
// enumeration leaves the rule silently uninvoked over the coordinates it omits,
// and a comparison against a second reading, a clean-input control, and any
// content assertion about a coordinate the omission did not reach all still
// pass — a rule asked about nothing reports exactly what a rule asked and
// satisfied reports, and two readings asked about fewer things agree. So the
// count here is compared against the graph's own attachment set rather than
// against a figure written down, the missing coordinates are named rather than
// counted, and the graph is required to spread more than one attachment over
// more than one node so a narrowing of either axis can fail it.
func TestTheFeatureApplicabilityRuleIsInvokedOncePerAttachmentTheGraphHolds(t *testing.T) {
	fixture := applicabilityWorkspace(applicabilityCatalogPath)
	out, catalogLookup := compileWorkspace(t, fixture,
		alsoAttachToTheSecondComponent(t, bytesOf(t, applicabilityAggregatePath)))
	coordinates := coordinatesOf(t, out, catalogLookup)

	held := attachmentsTheGraphHolds(out)
	if len(held) < 2 {
		t.Fatalf("the fixture graph holds %d attachment(s); a per-attachment count cannot fail on fewer than two", len(held))
	}
	if carrying := nodesCarryingAttachments(out); carrying < 2 {
		t.Fatalf("the fixture spreads its attachments over %d node(s); a narrowed node axis would be invisible", carrying)
	}

	var enumerated int
	for _, node := range coordinates.Nodes() {
		enumerated += len(coordinates.Attachments(node.ID))
	}
	if enumerated != len(held) {
		t.Errorf("the enumeration yields %d attachment(s) where the graph holds %d", enumerated, len(held))
	}

	var visited []string
	if _, err := constraint.RunOnNodeAttachments(
		context.Background().Err,
		coordinates,
		coordinates,
		out.GraphView,
		countingApplicabilityRule(&visited),
	); err != nil {
		t.Fatalf("traversal: %v", err)
	}

	if len(visited) != len(held) {
		t.Errorf("the rule was invoked %d time(s) for %d attachment(s)", len(visited), len(held))
	}
	for _, attachment := range held {
		if !slices.Contains(visited, attachment) {
			t.Errorf("the rule was never invoked for the attachment %q the graph holds", attachment)
		}
	}
	for _, attachment := range visited {
		if !slices.Contains(held, attachment) {
			t.Errorf("the rule was invoked for %q, which the graph does not hold", attachment)
		}
	}
}

// TestTheFeatureApplicabilityRuleStopsPartwayOnACancelledWalk asserts the
// property a cancellation signal exists for, which is not the same as asserting
// an error came back.
//
// A walk that ran to completion and reported the cancellation afterwards
// returns exactly the same error as one that stopped, so the error alone
// distinguishes nothing. What distinguishes them is how many attachments the
// rule was invoked for, so that is what is asserted, against a graph spreading
// its attachments over more than one node so the two readings differ.
//
// The expected count is the first node's own share rather than a figure written
// down, because the signal is read once per node: a walk stopped by a signal
// that turns on its second read has finished the first node and reached no
// other. That is the granularity the traversal promises and the same one the
// reading this replaced had.
func TestTheFeatureApplicabilityRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	fixture := applicabilityWorkspace(applicabilityCatalogPath)
	out, catalogLookup := compileWorkspace(t, fixture,
		alsoAttachToTheSecondComponent(t, bytesOf(t, applicabilityAggregatePath)))
	coordinates := coordinatesOf(t, out, catalogLookup)

	held := attachmentsTheGraphHolds(out)
	firstNodeShare := len(coordinates.Attachments(coordinates.Nodes()[0].ID))
	if firstNodeShare == 0 || firstNodeShare == len(held) {
		t.Fatalf("the first enumerated node carries %d of the graph's %d attachment(s); stopping after it is indistinguishable from running to the end",
			firstNodeShare, len(held))
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
		objections, err := constraint.RunOnNodeAttachments(
			constraint.Cancellation(signal),
			coordinates,
			coordinates,
			out.GraphView,
			countingApplicabilityRule(&visited),
		)
		if !errors.Is(err, stop) {
			t.Fatalf("traversal error = %v, want the caller's own reason", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every attachment and reported at the
		// end, which is the reading this rules out.
		if len(visited) != firstNodeShare {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly %d, the first node's own share of %d",
				len(visited), firstNodeShare, len(held))
		}
		if len(objections) != 0 {
			t.Errorf("a cancelled walk handed back %d finding(s); a truncated set is not a result", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every attachment", func(t *testing.T) {
		var visited []string
		if _, err := constraint.RunOnNodeAttachments(
			nil,
			coordinates,
			coordinates,
			out.GraphView,
			countingApplicabilityRule(&visited),
		); err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(visited) != len(held) {
			t.Errorf("the rule was invoked %d time(s) for %d attachment(s)", len(visited), len(held))
		}
	})
}

// TestTheGraphStageRulesThreadTheCallersSignalIntoTheApplicabilityWalk asserts
// the signal this rule's traversal reads is the caller's own, threaded through
// the function the build invokes it from.
//
// The workspace is one whose graph holds no connection at all, which is what
// makes the assertion about this traversal rather than the one before it: a walk
// over an empty enumeration never reaches its own signal read, so the first
// traversal here that can observe a withdrawn caller is this one. The document
// is spliced to offend, so a signal that was not threaded would append its
// findings instead of stopping.
func TestTheGraphStageRulesThreadTheCallersSignalIntoTheApplicabilityWalk(t *testing.T) {
	fixture := applicabilityWorkspace(applicabilityCatalogPath)
	out, _ := compileWorkspace(t, fixture,
		retypeTheAttachingComponent(t, bytesOf(t, applicabilityAggregatePath)))

	if len(out.IRGraph.Spec.Edges) != 0 {
		t.Fatalf("the fixture graph holds %d connection(s); the connection walk would stop first and this proof would be about that one",
			len(out.IRGraph.Spec.Edges))
	}
	before := len(out.Violations)
	if before == 0 {
		t.Fatal("the spliced document produced no violation, so nothing distinguishes a stopped walk from a silent one")
	}

	withdrawn, withdraw := context.WithCancel(context.Background())
	withdraw()
	if err := runGraphStageRules(withdrawn, out.GraphScope); !errors.Is(err, context.Canceled) {
		t.Fatalf("the graph-stage rules returned %v for a caller that withdrew, want the withdrawal", err)
	}
	if after := len(out.Violations); after != before {
		t.Errorf("a stopped walk still appended %d violation(s)", after-before)
	}
}

// TestTheFeatureApplicabilityRuleReportsAnApplicabilityItCannotEvaluate covers
// the second thing the rule reports: a declared applicability the label matcher
// cannot parse at all, as against one it parses and finds unsatisfied.
//
// It compiles the committed document whose attached Feature declares exactly
// that, so the offending declaration is a property of an entity the loader
// accepted rather than of the assertion.
func TestTheFeatureApplicabilityRuleReportsAnApplicabilityItCannotEvaluate(t *testing.T) {
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: unevaluableAggregatePath,
		releasePath:   unevaluableReleasePath,
		manifestPath:  applicabilityManifestPath,
		catalogPath:   applicabilityCatalogPath,
	}, bytesOf(t, unevaluableAggregatePath))

	objections, err := constraint.RunOnNodeAttachments(
		context.Background().Err,
		coordinatesOf(t, out, catalogLookup),
		coordinatesOf(t, out, catalogLookup),
		out.GraphView,
		analyzer.AttachmentSatisfiesFeatureApplicability,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(objections) != 1 {
		t.Fatalf("objections = %d, want 1 for the one attachment whose applicability cannot be evaluated", len(objections))
	}
	detail := objections[0].Detail()
	if len(detail) != 3 {
		t.Fatalf("detail = %v, want the declared applicability, the Feature type and why it could not be evaluated", detail)
	}
	if detail[len(detail)-1] == "" {
		t.Error("the finding carries no reason the applicability could not be evaluated")
	}
	// What tells this apart from an evaluated-and-false finding is not that a
	// third value is present — that arm carries three too — but the caption the
	// rule puts on it and the advice this occurrence carries, so both are pinned
	// rather than inferred from the count. This drives the unevaluable arm alone,
	// so it proves what that arm reports and not the discrimination itself; the
	// comparison of one arm's pair against the other's is taken where both arms
	// are driven from one table.
	discriminatorsOf(t, objections[0],
		[]string{"", "declared by Feature", "which could not be evaluated:"},
		"Correct the appliesTo selector on the Feature's catalog entity.")
	violations := analyzer.ErrorsFromObjections(objections)
	if len(violations) != 1 || violations[0].Field != ".spec.components.web.features.bad" {
		t.Errorf("violation = %+v, want one positioned on the offending attachment", violations)
	}
	if !errors.Is(violations[0].Sentinel, analyzer.ErrSelectorViolation()) {
		t.Errorf("violation carries %v, want the rule's identity", violations[0].Sentinel)
	}
}

// TestTheFeatureApplicabilityRuleRequiresBothDeclaredSurfaces covers the
// semantics the rule itself supplies rather than the label matcher: a Feature
// declares applicability on two surfaces, the single constraint and the
// alternatives, and both must hold.
//
// Every leg is driven by rewriting one Feature entity in a copy of the fixture
// catalog and compiling for real, so the declaration under test is one the
// loader accepted. Only that entity is rewritten, so the assertions below are
// narrowed to the one attachment naming it.
func TestTheFeatureApplicabilityRuleRequiresBothDeclaredSurfaces(t *testing.T) {
	clean := bytesOf(t, applicabilityAggregatePath)

	// The component the attachments hang off carries type=server and
	// zone=frontend, so a constraint naming either holds and one naming the data
	// zone does not.
	for _, leg := range []struct {
		name        string
		declaration string
		want        int
	}{
		{
			"both surfaces hold",
			"  appliesTo: \"component.aggrega.ai/type=server\"\n" +
				"  appliesToAnyOf:\n    - \"tier.aggrega.ai/zone=frontend\"\n",
			0,
		},
		{
			"the single constraint fails while an alternative holds",
			"  appliesTo: \"tier.aggrega.ai/zone=data\"\n" +
				"  appliesToAnyOf:\n    - \"component.aggrega.ai/type=server\"\n",
			1,
		},
		{
			"the single constraint holds while no alternative does",
			"  appliesTo: \"component.aggrega.ai/type=server\"\n" +
				"  appliesToAnyOf:\n    - \"tier.aggrega.ai/zone=data\"\n" +
				"    - \"component.aggrega.ai/type=worker\"\n",
			1,
		},
		{
			"one alternative among several holds",
			"  appliesToAnyOf:\n    - \"tier.aggrega.ai/zone=data\"\n" +
				"    - \"component.aggrega.ai/type=server\"\n",
			0,
		},
		{
			"an alternative the matcher cannot parse is reported",
			"  appliesToAnyOf:\n    - \"tier.aggrega.ai/zone in (frontend\"\n",
			1,
		},
		{
			"a Feature declaring neither surface applies anywhere",
			"",
			0,
		},
	} {
		t.Run(leg.name, func(t *testing.T) {
			out, _ := compileWorkspace(t,
				applicabilityWorkspace(catalogWithFeatureApplicability(t, leg.declaration)), clean)
			found := findingsAboutTheRewrittenAttachment(out.Analysis.Errors)
			if len(found) != leg.want {
				t.Errorf("findings about the rewritten attachment = %d, want %d (all findings: %v)",
					len(found), leg.want, out.Analysis.Errors)
			}
		})
	}
}
