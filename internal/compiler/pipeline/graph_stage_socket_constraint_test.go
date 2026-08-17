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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
)

// The proofs in this file cover the label constraint a dependency socket
// declares over what may satisfy it, evaluated over the finalised graph. They
// sit in this package rather than beside the declaration because the
// presentation and the enumeration the rule needs are both carried on an
// unexported output, and reaching them is the point: a proof over an
// enumeration a test assembles proves the test's account of the graph rather
// than the compiler's.
//
// The expectations are written as literals rather than compared against a
// second reading of the same document. That is deliberate and it is the
// stronger of the two available shapes here: a comparison between two readings
// agrees vacuously whenever the subject set is narrower than the graph — both
// readings are asked about fewer things, both stay silent, and the agreement
// says nothing. So coverage of the subject set is asserted separately, against
// the graph's own edge set, by
// TestTheSocketConstraintRuleIsInvokedOncePerConnectionTheGraphHolds below.

// misdirectTwoSockets points two of the fixture's connections at targets whose
// labels fail the constraints their sockets declare: the api server's database
// socket at the web front end, and the front end's api socket at the database.
//
// Two rather than one, because a proof that a rule reports every offending
// connection cannot be taken on a document with one offence — a reading that
// returned after the first would pass it. The splice is in-memory: the fixture
// on disk is shared with other proofs and must keep producing exactly what they
// expect.
func misdirectTwoSockets(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const (
		databaseAnchor      = "        databases:\n          - data.main-db\n"
		databaseMisdirected = "        databases:\n          - frontend\n"
		apiAnchor           = "            target: backend\n"
		apiMisdirected      = "            target: data.main-db\n"
	)
	for _, anchor := range []string{databaseAnchor, apiAnchor} {
		if !bytes.Contains(aggregate, []byte(anchor)) {
			t.Fatalf("fixture no longer carries the wiring %q; the splice would be silently inert", anchor)
		}
	}
	spliced := bytes.Replace(aggregate, []byte(databaseAnchor), []byte(databaseMisdirected), 1)
	return bytes.Replace(spliced, []byte(apiAnchor), []byte(apiMisdirected), 1)
}

// countingSocketConstraintRule is the declared rule with a counter around its
// predicate, so a proof can assert how many times it was invoked and for which
// coordinates. It delegates rather than reimplementing, so what is counted is
// the rule itself.
func countingSocketConstraintRule(
	visited *[]string,
) constraint.Check[constraint.GraphView, constraint.EdgeSubject] {
	rule := analyzer.EdgeTargetSatisfiesSocketSelector
	return constraint.Check[constraint.GraphView, constraint.EdgeSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
			*visited = append(*visited, fmt.Sprintf("%s|%s|%s", s.NodeID(), s.SocketName(), s.AliasName()))
			return rule.Run(v, s)
		},
	}
}

// connectionsTheGraphHolds is the coordinate triple of every connection in the
// compiled graph, read off the graph itself. It is the yardstick the invocation
// count is measured against, so it cannot be a list the proof writes down.
func connectionsTheGraphHolds(out *stepsOneToFourOutput) []string {
	coordinates := make([]string, 0, len(out.IRGraph.Spec.Edges))
	for _, edge := range out.IRGraph.Spec.Edges {
		coordinates = append(coordinates, fmt.Sprintf("%s|%s|%s", edge.Source, edge.Socket, edge.Alias))
	}
	return coordinates
}

// TestTheSocketConstraintRuleIsInvokedOncePerConnectionTheGraphHolds asserts
// the subject set the rule is asked about is the graph's whole connection set,
// by count and by coordinate.
//
// What it excludes, and what nothing else here can: an enumeration that yields
// fewer connections than the graph holds. Such an enumeration leaves the rule
// silently uninvoked over the connections it omits, and a comparison against a
// second reading, a clean-input control, and any content assertion about a
// connection the omission did not reach all still pass — a rule asked about
// nothing reports exactly what a rule asked and satisfied reports, and two
// readings asked about fewer things agree. So the count here is compared against
// the graph's own edge set rather than against a figure written down, the
// missing coordinates are named rather than counted, and the graph is required
// to hold more than one connection so the comparison can fail at all.
func TestTheSocketConstraintRuleIsInvokedOncePerConnectionTheGraphHolds(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	coordinates := coordinatesOf(t, out, fixtureCatalog(t))

	held := connectionsTheGraphHolds(out)
	if len(held) < 2 {
		t.Fatalf("the fixture graph holds %d connection(s); a per-connection count cannot fail on fewer than two", len(held))
	}
	if enumerated := len(coordinates.Edges()); enumerated != len(held) {
		t.Errorf("the enumeration yields %d connection(s) where the graph holds %d", enumerated, len(held))
	}

	var visited []string
	if _, err := constraint.RunOnEdges(
		context.Background().Err,
		coordinates,
		out.GraphView,
		countingSocketConstraintRule(&visited),
	); err != nil {
		t.Fatalf("traversal: %v", err)
	}

	if len(visited) != len(held) {
		t.Errorf("the rule was invoked %d time(s) for %d connection(s)", len(visited), len(held))
	}
	for _, connection := range held {
		if !slices.Contains(visited, connection) {
			t.Errorf("the rule was never invoked for the connection %q the graph holds", connection)
		}
	}
	for _, connection := range visited {
		if !slices.Contains(held, connection) {
			t.Errorf("the rule was invoked for %q, which the graph does not hold", connection)
		}
	}
}

// TestTheSocketConstraintRuleHaltsThroughTheViolationChannel asserts the
// findings reach the slice the build gates on rather than the advisory stream,
// and that they carry what a caller matches and positions on.
//
// The expectations are the ones the reading this replaced produced on the same
// document, with two measured differences carried deliberately: the locator now
// names the offending authoring key rather than being empty, and the message is
// the rule's own registered wording with the fact values it read, rather than a
// sentence composed inside the reading.
func TestTheSocketConstraintRuleHaltsThroughTheViolationChannel(t *testing.T) {
	clean := readAggregate(t)

	t.Run("both offending connections reach the violation slice", func(t *testing.T) {
		out := compileFixture(t, misdirectTwoSockets(t, clean))

		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("a halting rule reported on the advisory stream: %v", out.Analysis.Warnings)
		}
		if len(out.Analysis.Errors) != 2 {
			t.Fatalf("violations = %d, want 2 (one per offending connection): %v", len(out.Analysis.Errors), out.Analysis.Errors)
		}

		wantLocators := []string{
			".spec.components.backend.uses.databases",
			".spec.components.frontend.uses.apis.my-api",
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
			if violation.OriginFile != invocationAggregatePath {
				t.Errorf("violation %d document = %q, want %q", i, violation.OriginFile, invocationAggregatePath)
			}
			// The edge fields stay empty on this class, so a consumer reading
			// Socket to classify a finding would misread it. That is why the
			// kind above is the classifier.
			if violation.Source != "" || violation.Target != "" || violation.Socket != "" {
				t.Errorf("violation %d filled the edge fields: %+v", i, violation)
			}
		}

		// The offending values the compiled graph and the catalog supplied.
		first := out.Analysis.Errors[0].Message
		if !strings.Contains(first, "component.aggrega.ai/type=stateful-server") || !strings.Contains(first, "frontend") {
			t.Errorf("first violation names neither the declared constraint nor the resolved target: %q", first)
		}
		second := out.Analysis.Errors[1].Message
		if !strings.Contains(second, "component.aggrega.ai/type=server") || !strings.Contains(second, "data.main-db") {
			t.Errorf("second violation names neither the declared constraint nor the resolved target: %q", second)
		}
	})

	t.Run("a violation converts to a developer fault pointing at its document", func(t *testing.T) {
		out := compileFixture(t, misdirectTwoSockets(t, clean))
		if len(out.Analysis.Errors) == 0 {
			t.Fatal("no violation to convert")
		}
		converted := out.Analysis.Errors[0].ToCompilerError()
		if converted.Category != cerrors.DeveloperError {
			t.Errorf("category = %v, want %v", converted.Category, cerrors.DeveloperError)
		}
		if converted.Source.File != invocationAggregatePath {
			t.Errorf("source pointer = %q, want the document the finding is about %q", converted.Source.File, invocationAggregatePath)
		}
		if !errors.Is(converted, analyzer.ErrSelectorViolation()) {
			t.Error("the converted fault does not carry the rule's identity")
		}
	})

	t.Run("a document satisfying every socket constraint reports nothing", func(t *testing.T) {
		out := compileFixture(t, clean)
		if len(out.Analysis.Errors) != 0 {
			t.Errorf("violations on a satisfying document: %v", out.Analysis.Errors)
		}
		if len(out.Analysis.Warnings) != 0 {
			t.Errorf("advisories on a satisfying document: %v", out.Analysis.Warnings)
		}
	})
}

// TestTheSocketConstraintRuleReportsAConstraintItCannotEvaluate covers the
// second thing the rule reports: a declared constraint the label matcher cannot
// parse at all, as against one it parses and finds unsatisfied.
//
// It compiles against a copy of the fixture catalog with one socket's declared
// constraint rewritten to a string the matcher rejects, so the catalog is
// loaded for real and the offending declaration is a property of an entity the
// loader accepted.
func TestTheSocketConstraintRuleReportsAConstraintItCannotEvaluate(t *testing.T) {
	catalogPath := catalogWithAnUnparsableSocketConstraint(t)

	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   catalogPath,
	}, readAggregate(t))

	objections, err := constraint.RunOnEdges(
		context.Background().Err,
		coordinatesOf(t, out, catalogLookup),
		out.GraphView,
		analyzer.EdgeTargetSatisfiesSocketSelector,
	)
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	if len(objections) != 1 {
		t.Fatalf("objections = %d, want 1 for the one socket whose constraint cannot be evaluated", len(objections))
	}
	detail := objections[0].Detail()
	if len(detail) != 3 {
		t.Fatalf("detail = %v, want the constraint, the target and why it could not be evaluated", detail)
	}
	// The reason the constraint could not be evaluated is the last element, and
	// its presence is what tells this apart from an evaluated-and-false finding.
	if detail[len(detail)-1] == "" {
		t.Error("the finding carries no reason the constraint could not be evaluated")
	}
	violations := analyzer.ErrorsFromObjections(objections)
	if len(violations) != 1 || violations[0].Field != ".spec.components.backend.uses.databases" {
		t.Errorf("violation = %+v, want one positioned on the offending socket", violations)
	}
}

// TestTheSocketConstraintRuleEvaluatesASetMembershipConstraint covers a
// constraint written as set membership over a label rather than as equality,
// which is the shape a socket declaring a per-category capability takes.
//
// Both legs are asserted from one declaration, because the passing leg is what
// stops the failing one being satisfied by a rule that reports on everything.
func TestTheSocketConstraintRuleEvaluatesASetMembershipConstraint(t *testing.T) {
	catalogPath := catalogWithSocketConstraint(t,
		`selector: "component.aggrega.ai/type in (stateful-server, cache)"`)
	clean := readAggregate(t)

	for _, leg := range []struct {
		name      string
		aggregate []byte
		want      int
	}{
		{"a target whose label value is in the set is admitted", clean, 0},
		{"a target whose label value is outside the set is reported", misdirectTwoSockets(t, clean), 1},
	} {
		t.Run(leg.name, func(t *testing.T) {
			out, catalogLookup := compileWorkspace(t, workspaceFixture{
				aggregatePath: invocationAggregatePath,
				releasePath:   invocationReleasePath,
				manifestPath:  invocationManifestPath,
				catalogPath:   catalogPath,
			}, leg.aggregate)

			objections, err := constraint.RunOnEdges(
				context.Background().Err,
				coordinatesOf(t, out, catalogLookup),
				out.GraphView,
				analyzer.EdgeTargetSatisfiesSocketSelector,
			)
			if err != nil {
				t.Fatalf("traversal: %v", err)
			}
			// Only the database socket carries the rewritten constraint, so the
			// second misdirected connection is not this leg's subject.
			held := 0
			for _, objection := range objections {
				if strings.Contains(objection.Locator(), "uses.databases") {
					held++
				}
			}
			if held != leg.want {
				t.Errorf("findings on the database socket = %d, want %d (all findings: %d)", held, leg.want, len(objections))
			}
		})
	}
}

// catalogWithAnUnparsableSocketConstraint is the catalog with the api server's
// database-socket constraint rewritten to a string the label matcher rejects.
func catalogWithAnUnparsableSocketConstraint(t *testing.T) string {
	t.Helper()
	return catalogWithSocketConstraint(t, `selector: "!"`)
}

// catalogWithSocketConstraint copies the fixture catalog into a temporary tree
// and rewrites the api server's database-socket constraint to the supplied
// declaration. The copy is what keeps the committed fixture producing exactly
// what every other proof over it expects.
func catalogWithSocketConstraint(t *testing.T, declaration string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(root, os.DirFS(invocationCatalogPath)); err != nil {
		t.Fatalf("copy catalog: %v", err)
	}
	entity := filepath.Join(root, "base", "components", "api-server.yaml")
	source, err := os.ReadFile(entity)
	if err != nil {
		t.Fatalf("read component entity: %v", err)
	}
	const anchor = `selector: "component.aggrega.ai/type=stateful-server"`
	if !bytes.Contains(source, []byte(anchor)) {
		t.Fatalf("the entity no longer declares %q; the rewrite would be silently inert", anchor)
	}
	rewritten := bytes.Replace(source, []byte(anchor), []byte(declaration), 1)
	if err := os.WriteFile(entity, rewritten, 0o600); err != nil {
		t.Fatalf("write component entity: %v", err)
	}
	return root
}

// TestTheSocketConstraintRuleStopsPartwayOnACancelledWalk asserts the property
// a cancellation signal exists for, which is not the same as asserting an error
// came back.
//
// A walk that ran to completion and reported the cancellation afterwards
// returns exactly the same error as one that stopped, so the error alone
// distinguishes nothing. What distinguishes them is how many connections the
// rule was invoked for, so that is what is asserted, against a graph holding
// more than one so the two readings differ.
func TestTheSocketConstraintRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	coordinates := coordinatesOf(t, out, fixtureCatalog(t))

	held := connectionsTheGraphHolds(out)
	if len(held) < 2 {
		t.Fatalf("the fixture graph holds %d connection(s); stopping partway is indistinguishable from running to the end", len(held))
	}

	t.Run("the walk stops on the iteration after the signal turns", func(t *testing.T) {
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
		objections, err := constraint.RunOnEdges(
			constraint.Cancellation(signal),
			coordinates,
			out.GraphView,
			countingSocketConstraintRule(&visited),
		)
		if !errors.Is(err, stop) {
			t.Fatalf("traversal error = %v, want the caller's own reason", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every connection and reported at the
		// end, which is the reading this rules out.
		if len(visited) != 1 {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly 1", len(visited))
		}
		if len(objections) != 0 {
			t.Errorf("a cancelled walk handed back %d finding(s); a truncated set is not a result", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every connection", func(t *testing.T) {
		var visited []string
		if _, err := constraint.RunOnEdges(
			nil,
			coordinates,
			out.GraphView,
			countingSocketConstraintRule(&visited),
		); err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(visited) != len(held) {
			t.Errorf("the rule was invoked %d time(s) for %d connection(s)", len(visited), len(held))
		}
	})
}

// TestTheGraphStageRulesReadTheCallersCancellationSignal asserts the signal the
// traversals read is the caller's own, threaded through the function the build
// invokes them from — not one supplied at a proof's convenience.
func TestTheGraphStageRulesReadTheCallersCancellationSignal(t *testing.T) {
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, misdirectTwoSockets(t, readAggregate(t)))

	before := len(out.Analysis.Errors)
	if before == 0 {
		t.Fatal("the spliced document produced no violation, so nothing distinguishes a stopped walk from a silent one")
	}

	withdrawn, withdraw := context.WithCancel(context.Background())
	withdraw()
	if err := runGraphStageRules(withdrawn, out, catalogLookup); !errors.Is(err, context.Canceled) {
		t.Fatalf("the graph-stage rules returned %v for a caller that withdrew, want the withdrawal", err)
	}
	if after := len(out.Analysis.Errors); after != before {
		t.Errorf("a stopped walk still appended %d violation(s)", after-before)
	}
}

// TestTheGraphStageEnumerationRefusesAnAbsentPresentation pins the refusal that
// keeps a rule from being invoked over nothing: an enumeration built without a
// presentation would answer every rule an empty subject set, which reports
// exactly what a satisfied rule reports.
func TestTheGraphStageEnumerationRefusesAnAbsentPresentation(t *testing.T) {
	out := compileFixture(t, readAggregate(t))
	if ir.NewGraphStageCoordinates(out.IRGraph, nil, fixtureCatalog(t), "", out.AttachmentSites) != nil {
		t.Error("an enumeration was built without a presentation to read facts from")
	}
}
