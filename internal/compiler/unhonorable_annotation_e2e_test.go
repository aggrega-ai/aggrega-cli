// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that an authored platform annotation the resolved target
// cannot act on reaches the author as an advisory instead of being dropped in
// silence — built through the real pipeline against the real compiled Wasm
// guests, and read off the same structured channel the terminal and the editor
// both display.
//
// One Release authors three annotations against one target whose support claim
// covers exactly one of them, so all three outcomes are proved from a single
// build. That shape is deliberate: a silence measured in a build where nothing
// fires proves only that the check is quiet, while a silence measured beside a
// firing sibling on the same channel proves it is deciding.
//
// The second half of this file carries the same requirement across the crossings
// a render can arrive through. An annotation is dropped by whichever crossing
// renders the documents, and each crossing resolves its own target and declares
// its own input kinds — a component's Recipe, a component attachment's
// FeatureRecipe, a boundary attachment's FeatureRecipe. The author cannot tell
// which crossing produced which file, so an advisory raised on one and withheld
// on another reports the same fact as an outcome on one path and as nothing at
// all on the next.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func advisoryInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/fullstack-app/releases/annotation-support-advisory.yaml",
		CatalogPaths: []string{
			"testdata/fullstack-app/catalog/base",
			"testdata/fullstack-app/catalog/recipes/helm-chart-sourced",
		},
	}
}

// annotationAdvisories returns the class advisories a build raised about an
// authored annotation, keyed by node and key so a failure names what is missing
// rather than reporting a count mismatch.
func annotationAdvisories(diags []renderer.Diagnostic) map[string][]string {
	out := map[string][]string{}
	for _, d := range diags {
		if d.Code != renderer.UnsupportedFieldCode || !strings.Contains(d.Message, "annotation") {
			continue
		}
		node := ""
		if d.Location != nil {
			node = d.Location.URI
		}
		out[node] = append(out[node], d.SourceRef)
	}
	return out
}

// TestUnhonorableAnnotation_ThreeOutcomesFromOneBuild proves, from a single real
// build, that the advisory fires for the platform key the target's claim does not
// cover, stays silent for the platform key it does cover, and stays silent for
// the authored sub-prefixed key — with the firing case standing as the positive
// control that makes the two silences evidence.
func TestUnhonorableAnnotation_ThreeOutcomesFromOneBuild(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, advisoryInput())
	byNode := annotationAdvisories(result.Advisories)

	// Every node under both annotated boundaries is advised about the uncovered
	// key, and about that key ALONE.
	for _, node := range []string{"frontend", "backend", "data.main-db"} {
		keys := byNode[node]
		if len(keys) != 1 || keys[0] != "aggrega.ai/namespace" {
			t.Errorf("node %q advisories = %v; want exactly [aggrega.ai/namespace] — "+
				"the covered key and the authored sub-prefixed key must not be advised", node, keys)
		}
	}
	if len(byNode) == 0 {
		t.Fatal("no advisory raised at all; the two silences below would prove nothing")
	}
	// The claimed key and the authored vocabulary appear in no advisory anywhere,
	// on any node.
	for node, keys := range byNode {
		for _, k := range keys {
			if k == "aggrega.ai/chart-name" {
				t.Errorf("node %q: advised about aggrega.ai/chart-name, which the target's claim covers at boundary scope", node)
			}
			if strings.Contains(k, ".aggrega.ai/") {
				t.Errorf("node %q: advised about %q, which is authored vocabulary the compiler passes through", node, k)
			}
		}
	}
}

// TestUnhonorableAnnotation_DisplayedLineNamesKeyTargetAndInputKind asserts the
// requirement against the line a user actually reads, which both the terminal and
// the editor render through one formatter, rather than against the raw message.
func TestUnhonorableAnnotation_DisplayedLineNamesKeyTargetAndInputKind(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, advisoryInput())

	var lines []string
	for _, d := range result.Advisories {
		if d.Code == renderer.UnsupportedFieldCode && d.SourceRef == "aggrega.ai/namespace" {
			lines = append(lines, renderer.RenderDiagnosticText(d))
		}
	}
	if len(lines) == 0 {
		t.Fatal("no advisory for the uncovered key")
	}
	for _, line := range lines {
		for _, want := range []string{"aggrega.ai/namespace", "helm-chart", "sourced"} {
			if !strings.Contains(line, want) {
				t.Errorf("displayed advisory %q does not name %s", line, want)
			}
		}
	}
}

// TestUnhonorableAnnotation_NeverHalts pins that the advisory is a degradation. A
// boundary annotation legitimately cascades across components rendering through
// heterogeneous targets, so a build carrying advisories on every node still
// succeeds and still emits its artifacts.
func TestUnhonorableAnnotation_NeverHalts(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, advisoryInput())
	if err != nil {
		t.Fatalf("an authored annotation the target cannot act on halted the build: %v", err)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("build emitted no artifacts; the advisory is not out-of-band")
	}
	if len(annotationAdvisories(result.Advisories)) == 0 {
		t.Fatal("build raised no advisory, so it does not exercise the non-halting path")
	}
}

// TestUnhonorableAnnotation_OutputIsByteIdentical pins the out-of-band contract
// empirically rather than by citing it: the same workspace built twice emits
// byte-identical artifacts while the advisories are being raised, so raising them
// reaches no emitted byte.
func TestUnhonorableAnnotation_OutputIsByteIdentical(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, advisoryInput())
}

// multiKindInput is the build whose three nodes differ only in which input
// kinds their Recipe declares — one declaring both of the target's node-scoped
// paths, one declaring each alone — under one boundary annotating three keys.
func multiKindInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/fullstack-app/releases/annotation-support-multi-kind.yaml",
		CatalogPaths: []string{
			"testdata/fullstack-app/catalog/base",
			"testdata/fullstack-app/catalog/recipes/helm-sourced-with-workload",
			"testdata/fullstack-app/catalog/recipes/helm-sourced",
			"testdata/fullstack-app/catalog/recipes/helm-synthesized",
		},
	}
}

// TestUnhonorableAnnotation_MultiKindNodeIsJudgedOnEveryDeclaredKind is the
// end-to-end acceptance for a node declaring more than one input kind, through the
// real pipeline against the real compiled guests.
//
// The Release authors one key the target reads only where it builds the pod
// itself. The node declaring both paths therefore DOES have it applied, through its
// synthesized half, so no advisory may name it — while the node declaring the
// vendored path alone must still be advised, and the node declaring the synthesized
// path alone must still stay silent. Asserting all three from one build is what
// makes each verdict evidence: a silence measured beside a firing sibling on the
// same channel proves the check is deciding rather than merely quiet.
func TestUnhonorableAnnotation_MultiKindNodeIsJudgedOnEveryDeclaredKind(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, multiKindInput())
	byNode := annotationAdvisories(result.Advisories)

	for _, tc := range []struct {
		node     string
		declares string
		want     []string
	}{
		// Both node-scoped paths: the pod-scoped key is applied through the
		// synthesized half, so only the key no path reads is advised.
		{"backend", "a vendored chart and a synthesized Workload", []string{"aggrega.ai/cluster-domain"}},
		// The vendored path alone: the pod-scoped key has nowhere to land.
		{"frontend", "a vendored chart alone", []string{"aggrega.ai/service-account", "aggrega.ai/cluster-domain"}},
		// The synthesized path alone: the pod-scoped key is applied.
		{"data.main-db", "a synthesized Workload alone", []string{"aggrega.ai/cluster-domain"}},
	} {
		got := byNode[tc.node]
		if len(got) != len(tc.want) {
			t.Errorf("node %q (%s) advisories = %v; want %v", tc.node, tc.declares, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("node %q (%s) advisories = %v; want %v (author order is part of the contract)",
					tc.node, tc.declares, got, tc.want)
				break
			}
		}
	}

	// The key read on both paths is advised on no node at all. It is the control
	// that catches over-suppression: a change silencing the class wholesale would
	// satisfy every absence above, and this key was already silent, so it adds
	// nothing on its own — it is asserted alongside the firing key above, which is
	// what makes the pair discriminating.
	for node, keys := range byNode {
		for _, k := range keys {
			if k == "aggrega.ai/namespace" {
				t.Errorf("node %q: advised about aggrega.ai/namespace, which the target reads on both node-scoped paths", node)
			}
		}
	}
}

// TestUnhonorableAnnotation_MultiKindAdvisoryNamesEveryDeclaredKind asserts the
// requirement against the line a user actually reads. A node declaring two input
// kinds must be described by both of them: naming one would assert something false
// about a node that renders two, and this warning says the value was ignored, so a
// wrong kind here reads as an invitation to delete a working annotation.
func TestUnhonorableAnnotation_MultiKindAdvisoryNamesEveryDeclaredKind(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, multiKindInput())

	var multi, single []string
	for _, d := range result.Advisories {
		if d.Code != renderer.UnsupportedFieldCode || d.Location == nil {
			continue
		}
		switch d.Location.URI {
		case "backend":
			multi = append(multi, renderer.RenderDiagnosticText(d))
		case "frontend":
			single = append(single, renderer.RenderDiagnosticText(d))
		}
	}
	if len(multi) == 0 || len(single) == 0 {
		t.Fatalf("need an advisory on the multi-kind node and on the single-kind node; got %d and %d",
			len(multi), len(single))
	}
	for _, line := range multi {
		for _, want := range []string{"sourced", "synthesized"} {
			if !strings.Contains(line, want) {
				t.Errorf("the multi-kind node's advisory %q does not name the %s kind", line, want)
			}
		}
	}
	// The single-kind node's line is the control on the other side: naming both
	// kinds everywhere would satisfy the assertion above while describing every
	// node wrongly.
	for _, line := range single {
		if strings.Contains(line, "synthesized") {
			t.Errorf("the single-kind node's advisory %q names a kind that node does not declare", line)
		}
		if !strings.Contains(line, "sourced") {
			t.Errorf("the single-kind node's advisory %q does not name the sourced kind", line)
		}
	}
}

// TestUnhonorableAnnotation_MultiKindOutputIsByteIdentical carries the out-of-band
// contract onto the multi-kind shape. The node whose advisory this change removed
// still renders both halves, so its emitted bytes are the thing the removed
// advisory was false about — and they must be stable.
func TestUnhonorableAnnotation_MultiKindOutputIsByteIdentical(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, multiKindInput())
}

// crossingInput names one of the builds that differ only in which crossing
// carries the artifacts. They share a catalog — one component, one target, one
// authored annotation — so what a build's advisories differ by is the crossing
// and nothing else.
//
// The target claims the authored key at the two kinds where it builds the
// manifest itself and claims the standalone-artifacts kind as one it renders. So
// a crossing whose whole output is imported artifact documents is one the key
// reaches nothing through, while the target still renders it perfectly well —
// which is the case an author has no way to distinguish from a working
// annotation.
func crossingInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/ignored-annotation/" + release,
		CatalogPaths: []string{"testdata/ignored-annotation/catalog"},
	}
}

// TestUnhonorableAnnotation_AdvisedWhicheverCrossingRendersTheOutput is the
// acceptance for the requirement that the advisory is a property of the render,
// not of the path the render was reached through.
//
// Each row is a real build. The three firing rows are the crossings that resolve
// their own target and declare their own input kinds; the fourth authors the same
// annotation with every crossing rendering at a kind the claim covers, and must
// raise nothing. Asserting all four from one table is what makes each verdict
// evidence: the firing rows are the positive control that the channel works, so
// the fourth row's zero is an absence rather than a broken harness — and the
// fourth row is the control on the other side, since a check firing whenever the
// key is merely present would satisfy every firing row and be caught only there.
func TestUnhonorableAnnotation_AdvisedWhicheverCrossingRendersTheOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		crossing string
		release  string
		// want maps the identity each advisory is attributed to onto the keys
		// advised there. Empty means the build must raise none at all.
		want map[string][]string
	}{
		{
			crossing: "the node's own Recipe declares the artifacts",
			release:  "release-component-artifacts.yaml",
			want:     map[string][]string{"app": {"aggrega.ai/namespace"}},
		},
		{
			crossing: "a component attachment's FeatureRecipe declares them",
			release:  "release-feature-artifacts.yaml",
			want:     map[string][]string{"app": {"aggrega.ai/namespace"}},
		},
		{
			crossing: "a boundary attachment's FeatureRecipe declares them",
			release:  "release-boundary-artifacts.yaml",
			want:     map[string][]string{"<boundary-fr>:bart@app-ns": {"aggrega.ai/namespace"}},
		},
		{
			crossing: "every crossing renders at a kind the claim covers",
			release:  "release-all-honoured.yaml",
			want:     map[string][]string{},
		},
	} {
		t.Run(tc.crossing, func(t *testing.T) {
			t.Parallel()
			result := pipelinetest.Build(t, crossingInput(tc.release))
			got := annotationAdvisories(result.Advisories)

			if len(got) != len(tc.want) {
				t.Fatalf("%s: advisories = %v; want %v", tc.crossing, got, tc.want)
			}
			for id, wantKeys := range tc.want {
				gotKeys := got[id]
				if len(gotKeys) != len(wantKeys) {
					t.Errorf("%s: advisories for %q = %v; want %v", tc.crossing, id, gotKeys, wantKeys)
					continue
				}
				for i := range wantKeys {
					if gotKeys[i] != wantKeys[i] {
						t.Errorf("%s: advisories for %q = %v; want %v (author order is part of the contract)",
							tc.crossing, id, gotKeys, wantKeys)
						break
					}
				}
			}
		})
	}
}

// TestUnhonorableAnnotation_AttachmentAdvisoryNamesTheKindTheAttachmentDeclares
// asserts against the line a user reads, for a render reached through an
// attachment. The kind named has to be the ATTACHMENT's own: on both of these
// builds the host component synthesizes its Workload, which the target's
// claim covers, so naming the host's kind would describe a crossing that had the
// annotation applied and would invite deleting one that is in fact working.
func TestUnhonorableAnnotation_AttachmentAdvisoryNamesTheKindTheAttachmentDeclares(t *testing.T) {
	t.Parallel()
	for _, release := range []string{
		"release-feature-artifacts.yaml",
		"release-boundary-artifacts.yaml",
	} {
		t.Run(release, func(t *testing.T) {
			t.Parallel()
			result := pipelinetest.Build(t, crossingInput(release))

			var lines []string
			for _, d := range result.Advisories {
				if d.Code == renderer.UnsupportedFieldCode && d.SourceRef == "aggrega.ai/namespace" {
					lines = append(lines, renderer.RenderDiagnosticText(d))
				}
			}
			if len(lines) != 1 {
				t.Fatalf("want exactly one advisory line, got %d: %v", len(lines), lines)
			}
			for _, want := range []string{"aggrega.ai/namespace", "helm", "artifacts-only"} {
				if !strings.Contains(lines[0], want) {
					t.Errorf("displayed advisory %q does not name %s", lines[0], want)
				}
			}
			// The kind the HOST renders at must not be what the line says: the
			// host had the annotation applied, and this advisory is not about it.
			if strings.Contains(lines[0], "synthesized") {
				t.Errorf("displayed advisory %q names the kind the host renders at, not the attachment's", lines[0])
			}
		})
	}
}

// TestUnhonorableAnnotation_HonouredCrossingsEmitStableBytes pins the
// out-of-band contract on the build where nothing is ignored: it raises no
// advisory, builds, and emits the same bytes on every run.
func TestUnhonorableAnnotation_HonouredCrossingsEmitStableBytes(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, crossingInput("release-all-honoured.yaml"))
}

// TestUnhonorableAnnotation_AttachmentCrossingOutputIsByteIdentical carries the
// same contract onto the crossing this change made noisy. The attachment's
// documents are the bytes the advisory is ABOUT, so raising it must reach none of
// them.
func TestUnhonorableAnnotation_AttachmentCrossingOutputIsByteIdentical(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, crossingInput("release-feature-artifacts.yaml"))
}

// TestUnhonorableAnnotation_AttachmentCrossingNeverHalts pins that the advisory
// stays a degradation on the attachment crossing too. A boundary annotation
// cascades to every attachment beneath it, so a build whose attachments
// cannot act on one still succeeds and still emits their documents — including
// the very file the annotation was dropped from.
func TestUnhonorableAnnotation_AttachmentCrossingNeverHalts(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, crossingInput("release-feature-artifacts.yaml"))
	if err != nil {
		t.Fatalf("an annotation an attachment's target cannot act on halted the build: %v", err)
	}
	if len(annotationAdvisories(result.Advisories)) == 0 {
		t.Fatal("build raised no advisory, so it does not exercise the non-halting path")
	}
	const emitted = "dist/app/helm/app--art--aux-config.yaml"
	if _, ok := result.Artifacts[emitted]; !ok {
		t.Errorf("the advised attachment emitted no %s; files: %v", emitted, sortedKeys(result.Artifacts))
	}
}
