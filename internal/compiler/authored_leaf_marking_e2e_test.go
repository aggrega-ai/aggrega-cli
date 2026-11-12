// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that the per-authored-leaf
// sensitivity marking reaches every surface of the graph that carries authored
// pillar data, and reaches nothing else.
//
// Three surfaces carry such data and each is asserted here against the same
// build: the node's own values and infra, an attachment's payloads, and a
// boundary attachment's infra. Every positive assertion is paired with a
// negative one on a sibling datum in the SAME payload, because a marking that
// named everything would satisfy every presence assertion while being the one
// failure in this axis with no cheap undo — it declares public data secret, and
// a consumer then refuses or hides a build that should have passed.
package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const (
	markingNode       = "app"
	markingAttachment = "credentials"
	markingBoundary   = "cluster"
	markingBoundaryFR = "guard"
)

func authoredLeafMarkingBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/authored-leaf-marking/release.yaml",
		CatalogPaths: []string{"testdata/authored-leaf-marking/catalog/base"},
	})
}

// assertMarkedExactly compares a marking against the coordinates it must name,
// as a two-way set difference that NAMES what is missing and what is extra.
// Comparing lengths would let a marking that lost one coordinate and gained
// another agree with the expected count while naming a different set, and the
// gained one is precisely the over-marking this axis must not do.
func assertMarkedExactly(t *testing.T, surface string, got, want []string) {
	t.Helper()
	have := map[string]bool{}
	for _, c := range got {
		have[c] = true
	}
	need := map[string]bool{}
	for _, c := range want {
		need[c] = true
	}
	for _, c := range want {
		if !have[c] {
			t.Errorf("%s: coordinate %q is NOT marked; the whole marking is %v", surface, c, got)
		}
	}
	for _, c := range got {
		if !need[c] {
			t.Errorf("%s: coordinate %q IS marked and no declaration reaches it — over-marking declares "+
				"public data secret, which is the failure in this axis with no cheap undo; the whole marking is %v",
				surface, c, got)
		}
	}
}

// TestAuthoredLeafMarking_NodeOwnPillars is the surface this work exists for:
// a value a developer wrote directly into a component's own values or infra,
// against a declaration on that component's own schema. It carried no marking
// at all before, so a consumer reading the graph saw the datum with nothing
// said about it.
//
// The expected set covers the exact case, the covering case at one and at two
// levels of nesting, and the infra pillar; the coordinates deliberately absent
// from it are the whole point of the two-way difference above.
func TestAuthoredLeafMarking_NodeOwnPillars(t *testing.T) {
	t.Parallel()
	result := authoredLeafMarkingBuild(t)

	node, ok := result.IRGraph.Spec.Nodes.Get(markingNode)
	if !ok {
		t.Fatalf("graph carries no node %q", markingNode)
	}

	assertMarkedExactly(t, "node "+markingNode, node.SensitivePayloadLeaves, []string{
		// Declared at the leaf.
		"values.dbPassword",
		// Declared on the containing object; both children are named by that
		// one declaration, which an exact-match join would name neither of.
		"values.credentials.username",
		"values.credentials.password",
		// Two levels beneath the declaration, so the walk from leaf to
		// declaration climbs more than one segment.
		"values.outer.inner.secret",
		// The infra pillar, covered by an object declaration of its own. A
		// derivation reading only values would look complete without it.
		"infra.backup.passphrase",
	})
}

// TestAuthoredLeafMarking_NodeNegativeDirection states the absences the two-way
// difference above already enforces, individually and by name, because each one
// is a different way the rule can go wrong and a bare set comparison does not
// say which. The segment-wise case is the sharpest: `dbPasswordExtra` carries
// `dbPassword` as a textual prefix, so a covering rule that trimmed characters
// rather than whole path segments would mark it.
func TestAuthoredLeafMarking_NodeNegativeDirection(t *testing.T) {
	t.Parallel()
	result := authoredLeafMarkingBuild(t)

	node, _ := result.IRGraph.Spec.Nodes.Get(markingNode)
	marked := map[string]bool{}
	for _, c := range node.SensitivePayloadLeaves {
		marked[c] = true
	}

	for _, row := range []struct {
		coordinate string
		why        string
	}{
		{"values.appName", "a public top-level leaf nothing declares"},
		{"values.tuning.poolSize", "a leaf beneath an object nothing declares, so the covering walk must reach nothing"},
		{"values.dbPasswordExtra", "a sibling whose name has a declared leaf's name as a TEXTUAL prefix; " +
			"marking it means the covering walk trims characters instead of whole path segments"},
		{"values.outer.public", "a public sibling of the declared object, at the same depth"},
		{"infra.replicas", "a public infra leaf beside a declared infra object"},
		{"values.credentials", "the declared object itself; it names no authored datum, only its leaves do"},
		{"values.tuning", "an undeclared object"},
	} {
		if marked[row.coordinate] {
			t.Errorf("node %s: %q is marked and must not be — %s; the whole marking is %v",
				markingNode, row.coordinate, row.why, node.SensitivePayloadLeaves)
		}
	}
}

// TestAuthoredLeafMarking_AttachmentCoversBeneathADeclaredObject is the
// widening of the surface that was already marked. The attachment authors two
// values beneath an object its Feature declares sensitive and neither leaf path
// is itself declared, so a join matching an authored path against a declared
// path exactly names nothing here at all — the marking would be absent, and
// absence is indistinguishable from a declaration that matched nothing.
func TestAuthoredLeafMarking_AttachmentCoversBeneathADeclaredObject(t *testing.T) {
	t.Parallel()
	result := authoredLeafMarkingBuild(t)

	attachments, ok := result.IRGraph.Spec.FeatureValuesMapping.Get(markingNode)
	if !ok {
		t.Fatalf("node %q carries no attachment map", markingNode)
	}
	entry, ok := attachments.Get(markingAttachment)
	if !ok {
		t.Fatalf("node %q carries no attachment %q (keys: %v)", markingNode, markingAttachment, attachments.Keys())
	}

	assertMarkedExactly(t, "attachment "+markingAttachment, entry.SensitivePayloadLeaves, []string{
		"values.vault.token",
		"values.vault.path",
	})

	// The paired control on the same attachment: a published locator the
	// Feature deliberately leaves undeclared. It is what separates "the
	// marking covers a declared subtree" from "the marking names whatever the
	// attachment authored".
	for _, c := range entry.SensitivePayloadLeaves {
		if c == "values.secretName" {
			t.Errorf("attachment %s: values.secretName is marked; its Feature declares it public, and "+
				"marking a locator states something false about it", markingAttachment)
		}
	}
}

// TestAuthoredLeafMarking_BoundaryAttachmentInfra covers the third surface. A
// boundary attachment authors only infra — the Release schema bans a values
// block at boundary level — so the marking here is infra-only by construction
// rather than by a narrowing anyone chose.
func TestAuthoredLeafMarking_BoundaryAttachmentInfra(t *testing.T) {
	t.Parallel()
	result := authoredLeafMarkingBuild(t)

	perBoundary, ok := result.IRGraph.Spec.BoundaryFeatures.Get(markingBoundary)
	if !ok {
		t.Fatalf("graph carries no boundary attachments at %q", markingBoundary)
	}
	bf, ok := perBoundary.Get(markingBoundaryFR)
	if !ok {
		t.Fatalf("boundary %q carries no attachment %q (keys: %v)", markingBoundary, markingBoundaryFR, perBoundary.Keys())
	}

	assertMarkedExactly(t, "boundary attachment "+markingBoundaryFR, bf.SensitivePayloadLeaves, []string{
		"infra.apiKey",
		"infra.store.token",
	})

	for _, c := range bf.SensitivePayloadLeaves {
		if strings.HasPrefix(c, "values.") {
			t.Errorf("boundary attachment %s: %q is a values coordinate, and a boundary attachment authors "+
				"no values pillar at all", markingBoundaryFR, c)
		}
	}
}

// TestAuthoredLeafMarking_OrderIsAuthorOrderValuesBeforeInfra pins the ordering
// contract the wire depends on: the values pillar's leaves before the infra
// pillar's, and within each pillar the order the payload holds them. Nothing
// sorts, so the sequence a consumer reads is the sequence the author wrote —
// which is what makes the graph byte-identical across runs of identical input.
//
// The fixture is deliberately non-alphabetical on both axes: `dbPassword` is
// authored before `credentials`, and the infra coordinate sorts before every
// values one, so a sorting derivation reverses both.
func TestAuthoredLeafMarking_OrderIsAuthorOrderValuesBeforeInfra(t *testing.T) {
	t.Parallel()
	result := authoredLeafMarkingBuild(t)

	node, _ := result.IRGraph.Spec.Nodes.Get(markingNode)
	want := []string{
		"values.dbPassword",
		"values.credentials.username",
		"values.credentials.password",
		"values.outer.inner.secret",
		"infra.backup.passphrase",
	}
	if len(node.SensitivePayloadLeaves) != len(want) {
		t.Fatalf("node %s marking: got %v, want the sequence %v", markingNode, node.SensitivePayloadLeaves, want)
	}
	for i := range want {
		if node.SensitivePayloadLeaves[i] != want[i] {
			t.Fatalf("node %s marking order: got %v, want %v — author order, values pillar first, no sort",
				markingNode, node.SensitivePayloadLeaves, want)
		}
	}
}

// This fixture's emitted graph is additionally validated against the IR schema
// by the emitted-IR conformance table, which it is enrolled in rather than
// re-validated here: the definitions are closed, so a Go field carrying a
// property the schema does not declare fails there, and this is the one fixture
// authoring a marking at all three units, so a property no Go field fills would
// go unremarked on every other row of that table.
