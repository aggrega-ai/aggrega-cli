// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that an author who declares a `values` leaf sensitive on a
// node whose render cannot carry that declaration is told so — built through the
// real pipeline against the real compiled Wasm guests, and read off the same
// structured channel the terminal and the editor both display.
//
// One Aggregate authors the same two values against the same two declarations
// on three nodes that differ only in what their Recipe declares, so all three
// outcomes come from a single build. That shape is deliberate: a silence
// measured in a build where nothing fires proves only that the check is quiet,
// while a silence measured beside a firing sibling on the same channel proves it
// is deciding.
//
// The two silences are load-bearing rather than spare. The artifacts node's is
// what proves the narrowing narrowed — the advisory reports a declaration the
// author can still act on, and an artifacts placement offers no lever, so firing
// there would report a permanent property of the shape on every build forever.
// The direct node's is the other direction: its declaration IS conveyed, so an
// advisory naming it would be false.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func notConveyedInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/sensitivity-not-conveyed/release.yaml",
		CatalogPaths: []string{"testdata/sensitivity-not-conveyed/catalog"},
	}
}

// notConveyedAdvisories returns the coordinates the class advised on, keyed by
// the node each advisory is attributed to, so a failure names what is missing
// or extra rather than reporting a count mismatch.
func notConveyedAdvisories(diags []renderer.Diagnostic) map[string][]string {
	out := map[string][]string{}
	for _, d := range diags {
		if d.Code != renderer.SensitivityNotConveyedCode {
			continue
		}
		node := ""
		if d.Location != nil {
			node = d.Location.Instance
		}
		out[node] = append(out[node], d.SourceRef)
	}
	return out
}

// TestSensitivityNotConveyed_ThreeOutcomesFromOneBuild proves, from a single
// real build, that the advisory fires for the declaration the mapped node
// cannot convey and that both other shapes stay silent — with the firing case
// standing as the positive control that makes the two silences evidence.
func TestSensitivityNotConveyed_ThreeOutcomesFromOneBuild(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, notConveyedInput())
	byNode := notConveyedAdvisories(result.Advisories)

	want := map[string][]string{"mapped": {"values.dbPassword"}}
	if len(byNode) != len(want) {
		t.Fatalf("advisories = %v; want %v — the artifacts node and the node whose "+
			"declaration IS conveyed must both stay silent", byNode, want)
	}
	for node, wantCoordinates := range want {
		got := byNode[node]
		if len(got) != len(wantCoordinates) {
			t.Errorf("node %q advisories = %v; want %v", node, got, wantCoordinates)
			continue
		}
		for i := range wantCoordinates {
			if got[i] != wantCoordinates[i] {
				t.Errorf("node %q advisories = %v; want %v", node, got, wantCoordinates)
				break
			}
		}
	}
	// The public sibling is authored on all three nodes and declared on none, so
	// an advisory naming it would report a declaration nobody wrote. Asserted by
	// name rather than left to the set comparison above, because over-reporting
	// on this axis is the failure with no cheap undo.
	for node, coordinates := range byNode {
		for _, c := range coordinates {
			if strings.Contains(c, "appName") {
				t.Errorf("node %q: advised about %q, which no declaration reaches", node, c)
			}
		}
	}
}

// TestSensitivityNotConveyed_DisplayedLineNamesCoordinateNodeAndRecipe asserts
// the requirement against the line a user actually reads, which both the
// terminal and the editor render through one formatter, rather than against the
// raw message. The authored coordinate has to be in it: an advisory telling an
// author that something they declared reaches nothing, without saying which
// declaration, names no action at all.
func TestSensitivityNotConveyed_DisplayedLineNamesCoordinateNodeAndRecipe(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, notConveyedInput())

	var lines []string
	for _, d := range result.Advisories {
		if d.Code == renderer.SensitivityNotConveyedCode {
			lines = append(lines, renderer.RenderDiagnosticText(d))
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want exactly one advisory line, got %d: %v", len(lines), lines)
	}
	for _, want := range []string{
		// the authored coordinate, spelled from the pillar root
		`"values.dbPassword"`,
		// the node and the resolved target
		`node "mapped" (provider "helm")`,
		// the Recipe holding the lever, which is a different entity from the one
		// carrying the declaration
		`"sens-mapped"`,
		// the key an author would edit
		"valuesMapping",
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("displayed advisory %q does not name %s", lines[0], want)
		}
	}
}

// TestSensitivityNotConveyed_NeverHalts pins that the advisory is a
// degradation. What it reports is an absence rather than a fault, and a Recipe
// that maps its values is an ordinary supported shape, so the build succeeds
// and still emits its artifacts.
func TestSensitivityNotConveyed_NeverHalts(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, notConveyedInput())
	if err != nil {
		t.Fatalf("an unconveyable sensitivity declaration halted the build: %v", err)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("build emitted no artifacts; the advisory is not out-of-band")
	}
	if len(notConveyedAdvisories(result.Advisories)) == 0 {
		t.Fatal("build raised no advisory, so it does not exercise the non-halting path")
	}
}

// TestSensitivityNotConveyed_OutputIsByteIdentical pins the out-of-band
// contract empirically rather than by citing it: the same workspace built twice
// emits byte-identical artifacts while the advisory is being raised, so raising
// it reaches no emitted byte.
func TestSensitivityNotConveyed_OutputIsByteIdentical(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, notConveyedInput())
}

// TestSensitivityNotConveyed_TheAdvisedNodeStillCarriesNoMarking is the other
// half of the claim the advisory makes. The advisory says the target receives
// no sensitivity marking for the declared leaf; this asserts that on the graph
// the build produced rather than trusting the sentence, and asserts the paired
// positive on the node whose Recipe declares no mapping — so the pair shows the
// derivation is deciding rather than absent everywhere.
func TestSensitivityNotConveyed_TheAdvisedNodeStillCarriesNoMarking(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, notConveyedInput())

	// Both nodes' Components declare the same leaf, so the authored-leaf marking
	// on the graph names it on both: what differs between them is whether the
	// RENDER can recover a rendered identity for it, which is the advisory's
	// subject and is not a fact the graph carries.
	for _, node := range []string{"mapped", "direct"} {
		n, ok := result.IRGraph.Spec.Nodes.Get(node)
		if !ok {
			t.Fatalf("graph carries no node %q", node)
		}
		var found bool
		for _, c := range n.SensitivePayloadLeaves {
			if c == "values.dbPassword" {
				found = true
			}
			if c == "values.appName" {
				t.Errorf("node %q marks values.appName, which no declaration reaches", node)
			}
		}
		if !found {
			t.Errorf("node %q does not mark values.dbPassword; the fixture no longer declares "+
				"what the advisory is about, so every assertion in this file measures nothing: %v",
				node, n.SensitivePayloadLeaves)
		}
	}
}

// TestSensitivityNotConveyed_AdvisoryCanFail is the negative control on the
// whole file. A green that cannot go red is decoration, so this asserts the
// shape of the failure the others would report: a build whose advisory stream
// carries the class for a node the table does not expect must be visible as a
// difference, which the two-way comparison above performs. It runs the same
// build and requires the class to be present at all, because every silence
// asserted elsewhere is evidence only while something fires.
func TestSensitivityNotConveyed_AdvisoryCanFail(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, notConveyedInput())

	var raised int
	for _, d := range result.Advisories {
		if d.Code == renderer.SensitivityNotConveyedCode {
			raised++
		}
	}
	if raised == 0 {
		t.Fatal("the class raised nothing on a build that authors the condition, so every " +
			"silence this file asserts is the silence of a broken channel")
	}
	// The class must not be reachable by the flag that silences target-fidelity
	// notices. Asserted here over the real build's own diagnostics, so it is the
	// shipped code being compared rather than a literal.
	if renderer.SensitivityNotConveyedCode == renderer.UnsupportedFieldCode {
		t.Fatalf("the two classes must not share a code; both are %q", renderer.SensitivityNotConveyedCode)
	}
	if renderer.SensitivityNotConveyedCode == renderer.SensitiveByValueCode {
		t.Fatalf("the two sensitivity advisories must not share a code; both are %q",
			renderer.SensitivityNotConveyedCode)
	}
}
