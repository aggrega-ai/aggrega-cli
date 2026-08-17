// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The fixture this proof compiles. It is named here rather than shared with the
// package's other proofs because those are in the external test package, which
// this one cannot be: the presentation under proof is carried on an unexported
// output, and reaching it is the whole point.
const (
	invocationAggregatePath = "../../../internal/compiler/testdata/fullstack-app/aggregate.yaml"
	invocationReleasePath   = "../../../internal/compiler/testdata/fullstack-app/release.yaml"
	invocationManifestPath  = "../../../internal/compiler/testdata/fullstack-app/manifest.yaml"
	invocationCatalogPath   = "../../../internal/compiler/testdata/fullstack-app/catalog"
	invocationVersion       = "0.1.0"
)

// coordinatesOf is the enumeration the production path itself builds, taken for
// a compilation that actually happened.
//
// The proofs below take it rather than assembling an axis of their own: an
// enumeration a test writes is the test's account of what the graph holds, and
// a rule asked about that account proves the account rather than the compiler.
// The deployment's file is taken from the same place the production path takes
// it, so a proof over this enumeration is a proof over the one the build uses.
func coordinatesOf(t *testing.T, out *stepsOneToFourOutput, catalogLookup resolver.CatalogLookup) *ir.GraphStageCoordinates {
	t.Helper()
	coordinates := ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, catalogLookup, releaseFileOf(out.ResolveResult),
		out.AttachmentSites,
	)
	if coordinates == nil {
		t.Fatal("the compilation produced no enumeration of the finalised graph; no rule over it can be invoked")
	}
	return coordinates
}

// fixtureCatalog loads the catalog the fixture below compiles against, which the
// enumeration needs for the one axis the graph does not carry.
func fixtureCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	cat, err := catalog.LoadAll("", invocationCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	return cat.CatalogLookup()
}

// compileFixture runs the real pre-render pipeline over the fullstack fixture
// and hands the caller the outputs it produced, the spliced aggregate bytes
// included. It fails the test when the compilation cannot reach the outputs at
// all; an analyzer violation is not such a failure, because those ride on the
// analysis result by design and this proof compiles a document that has one.
func compileFixture(t *testing.T, aggregate []byte) *stepsOneToFourOutput {
	t.Helper()
	out, _ := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   invocationReleasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, aggregate)
	return out
}

// readAggregate returns the fixture's aggregate bytes.
func readAggregate(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	return b
}

// misdirectTheDatabaseSocket points the api server's database socket at the web
// front end instead of the database the module contributes.
//
// The socket declares that what it wires must carry the stateful-server type
// label; the web front end carries the plain server one. So the splice makes the
// rule's subject a real edge whose target really does fail the constraint its
// socket really declares — the finding is a property of the compiled document,
// not of the assertion. The splice is in-memory: the fixture on disk is shared
// with other proofs and must keep producing exactly what they expect.
func misdirectTheDatabaseSocket(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const anchor = "        databases:\n          - data.main-db\n"
	const misdirected = "        databases:\n          - frontend\n"
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the %q wiring; the splice would be silently inert", "databases -> data.main-db")
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(misdirected), 1)
}

// TestGraphPresentationAnswersARuleFromTheCompiledGraph is the invocation proof
// for the graph-stage presentation.
//
// It compiles a real workspace, takes the presentation the pipeline itself
// built, and asks a declared rule of it. The rule reads facts from three stages
// at once — what the graph resolved an edge to and the label set its target
// ends up with, what the flattening decided a node's component type is, and what
// the catalog declares the socket constrains — so a presentation that answered
// only its own stage, or answered emptily, could not produce the finding below.
//
// The subject is an edge whose target is a component a module contributed. That
// is deliberate: the earlier stage's fact over the application's own instance
// list cannot see such a component, so the finding is evidence that the
// delegation reaches the right producer rather than the nearest one.
func TestGraphPresentationAnswersARuleFromTheCompiledGraph(t *testing.T) {
	clean := readAggregate(t)

	t.Run("the presentation is built on the production path", func(t *testing.T) {
		out := compileFixture(t, clean)
		if out.GraphView == nil {
			t.Fatal("the pipeline carried no graph presentation; nothing downstream can be invoked through it")
		}
		// The module-contributed node is the one an earlier stage's fact cannot
		// see. Reading it through the presentation proves the embedding delegates
		// to the producer that can.
		if _, present := out.GraphView.FlattenedNodeComponentType("data.main-db"); !present {
			t.Error("the presentation does not see the component the module contributed; the earlier stage is not being delegated to")
		}
		labels := out.GraphView.NodeLabels("data.main-db")
		if labels.Len() == 0 {
			t.Error("the presentation answered an empty label set for a node the graph carries; it is not reading the compiled graph")
		}
	})

	t.Run("a rule invoked through it raises on the compiled document", func(t *testing.T) {
		out := compileFixture(t, misdirectTheDatabaseSocket(t, clean))

		objections, err := constraint.RunOnEdges(
			context.Background().Err,
			coordinatesOf(t, out, fixtureCatalog(t)),
			out.GraphView,
			analyzer.EdgeTargetSatisfiesSocketSelector,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) == 0 {
			t.Fatal("the rule raised nothing on a document whose socket constraint the compiled graph violates")
		}

		// The finding is read off the real product: the values below are the
		// selector the catalog declares and the target the graph resolved, both
		// reaching the rule only through the presentation.
		violations := analyzer.ErrorsFromObjections(objections)
		if len(violations) != len(objections) {
			t.Fatalf("conversion dropped findings: %d raised, %d converted", len(objections), len(violations))
		}
		first := violations[0]
		if !strings.Contains(first.Message, "component.aggrega.ai/type=stateful-server") {
			t.Errorf("finding does not carry the selector the socket declares: %q", first.Message)
		}
		if !strings.Contains(first.Message, "frontend") {
			t.Errorf("finding does not name the target the graph resolved: %q", first.Message)
		}
		if first.Field == "" {
			t.Error("finding carries no locator; it cannot be positioned on the authoring key")
		}
		if first.Sentinel == nil {
			t.Error("finding carries no sentinel; a caller cannot match the rule by identity")
		}
		if first.Kind != analyzer.AnalysisErrorLocated {
			t.Errorf("finding kind = %q, want %q", first.Kind, analyzer.AnalysisErrorLocated)
		}
		// Error() must read as the composed diagnostic rather than as an
		// edge-shaped one assembled from fields this conversion leaves empty.
		if first.Error() != first.Message {
			t.Errorf("Error() = %q, want the message verbatim %q", first.Error(), first.Message)
		}
	})

	t.Run("the same rule stays silent on the unspliced document", func(t *testing.T) {
		out := compileFixture(t, clean)

		objections, err := constraint.RunOnEdges(
			context.Background().Err,
			coordinatesOf(t, out, fixtureCatalog(t)),
			out.GraphView,
			analyzer.EdgeTargetSatisfiesSocketSelector,
		)
		if err != nil {
			t.Fatalf("traversal: %v", err)
		}
		if len(objections) != 0 {
			t.Fatalf("the rule raised on a document that satisfies every socket constraint: %d finding(s)", len(objections))
		}
		if converted := analyzer.ErrorsFromObjections(objections); converted != nil {
			t.Errorf("conversion invented %d violation(s) from nothing raised", len(converted))
		}
	})
}
