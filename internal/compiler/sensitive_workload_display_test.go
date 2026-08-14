// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — proof of the three properties that make a substituted
// re-render an honest display of a rendered workload rather than a second,
// unrelated document.
//
//  1. The re-render cannot reach a rendered artifact. Two builds differing only in
//     whether capture is on must emit byte-identical files.
//  2. The re-render keeps the emitted document's shape. Same key set at every
//     level, and at each differing position the replacement text stands exactly
//     where the datum stood, with the characters around it unchanged.
//  3. A template that branches on a value's CONTENT diverges, which is why the
//     display announces itself. That divergence is asserted, not fixed.
//
// Every assertion here DECODES the projection rather than searching its text. A
// text search over a structured body answers a question about the encoding before
// it answers one about the content, and the same value can be absent from a body's
// characters while fully present in its data.
package compiler_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

const (
	// The declared-sensitive plaintext each node of the display fixture carries.
	// Distinct from every other fixture's so no assertion can pass on the wrong one.
	shapeSecret  = "shape-secret-plaintext"
	branchSecret = "branch-secret-plaintext"
)

// displayCatalog is the one catalog every fixture in this directory resolves
// against.
var displayCatalog = []string{"testdata/sensitive-redaction/catalog/base"}

func displayBuild(t *testing.T, unredacted bool) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:     "testdata/sensitive-redaction/display-release.yaml",
		CatalogPaths:    displayCatalog,
		IncludeTrace:    true,
		TraceUnredacted: unredacted,
	})
}

// traceEntry returns one trace entry by its full relative path, failing when it is
// absent — an assertion against a missing slot would be vacuous, which is the one
// failure mode this whole family of tests exists to avoid.
func traceEntry(t *testing.T, result *pipeline.Result, path string) string {
	t.Helper()
	body, ok := result.Trace[path]
	if !ok {
		present := make([]string, 0, len(result.Trace))
		for p := range result.Trace {
			present = append(present, p)
		}
		sort.Strings(present)
		t.Fatalf("trace slot %q absent; the fixture's trace layout drifted. present: %v", path, present)
	}
	return string(body)
}

// workloadLeaves decodes one trace slot and flattens its .workload projection into
// path → scalar text.
//
// Decoding is the point. The projection is YAML, and a plaintext search over YAML
// is a claim about the serializer: a value can be folded across lines, quoted,
// re-encoded, or rendered as a list of integers, and any of those makes a text
// search report absence for data that is fully present. Flattening to leaf paths
// also makes an assertion say WHICH field it is about, where a whole-body search
// cannot tell the workload's copy of a value from the delivery pipe's.
//
// It fails when the slot carries no workload document, so a fixture that stops
// projecting one fails as a missing document rather than passing on an empty map.
func workloadLeaves(t *testing.T, body string) map[string]string {
	t.Helper()
	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode trace slot: %v\n--- slot ---\n%s", err, body)
	}
	doc, present := decoded["workload"]
	if !present {
		t.Fatalf("the slot carries no .workload projection; every assertion below would be vacuous\n--- slot ---\n%s", body)
	}
	if text, isStatement := doc.(string); isStatement {
		t.Fatalf("the slot carries a statement rather than a document: %q", text)
	}
	leaves := map[string]string{}
	flattenLeaves("", doc, leaves)
	if len(leaves) == 0 {
		t.Fatalf("the .workload projection decoded to no leaves\n--- slot ---\n%s", body)
	}
	return leaves
}

func flattenLeaves(prefix string, node any, out map[string]string) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			flattenLeaves(joinPath(prefix, k), v, out)
		}
	case []any:
		for i, v := range n {
			flattenLeaves(fmt.Sprintf("%s[%d]", prefix, i), v, out)
		}
	default:
		out[prefix] = fmt.Sprint(node)
	}
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func sortedPaths(leaves map[string]string) []string {
	out := make([]string, 0, len(leaves))
	for p := range leaves {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestWorkloadDisplay_CaptureMovesNoRenderedByte is the load-bearing safety
// property of the whole substitution, and the reason it is asserted rather than
// argued: the re-render runs INSIDE the render pipeline, on the same context the
// artifacts render from, so a placeholder reaching a deployable would be a far
// worse outcome than the leak the substitution closes.
//
// Two builds of the same input differing only in whether capture is on must emit
// the same files with the same bytes. The withheld data reaches those files in the
// clear in both, which is the other half of the boundary.
func TestWorkloadDisplay_CaptureMovesNoRenderedByte(t *testing.T) {
	t.Parallel()
	withCapture := displayBuild(t, false)
	withoutCapture := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/sensitive-redaction/display-release.yaml",
		CatalogPaths: displayCatalog,
	})

	if len(withoutCapture.Artifacts) == 0 {
		t.Fatal("the build with capture off rendered nothing; the byte-identity claim would be vacuous")
	}
	if len(withCapture.Trace) == 0 {
		t.Fatal("the build with capture on produced no trace; the two builds do not actually differ")
	}
	if len(withCapture.Artifacts) != len(withoutCapture.Artifacts) {
		t.Fatalf("capture changed the rendered file count: %d with it on, %d with it off",
			len(withCapture.Artifacts), len(withoutCapture.Artifacts))
	}
	for path, want := range withoutCapture.Artifacts {
		got, present := withCapture.Artifacts[path]
		if !present {
			t.Errorf("capture dropped the rendered artifact %q", path)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("capture moved rendered bytes at %q; the display re-render must never reach a deployable\n--- capture off ---\n%s\n--- capture on ---\n%s", path, want, got)
		}
		if strings.Contains(string(got), redact.Placeholder()) {
			t.Errorf("the rendered artifact %q carries the replacement text\n--- artifact ---\n%s", path, got)
		}
		if strings.Contains(string(got), redact.SubstitutedProjection()) || strings.Contains(string(got), redact.WithheldProjection()) {
			t.Errorf("the rendered artifact %q carries a display announcement\n--- artifact ---\n%s", path, got)
		}
	}
	// The other half: what the deployable is supposed to carry, it carries. The
	// shape node interpolates its declared leaf, so the plaintext itself is in the
	// artifact; the branch node only tests its leaf, so what the artifact proves is
	// that the emitted document took the branch the AUTHORED value selects — the
	// branch the display cannot take.
	for _, want := range []string{shapeSecret, "MATCHED_AUTHORED_VALUE"} {
		found := false
		for _, body := range withCapture.Artifacts {
			if strings.Contains(string(body), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the rendered artifacts must carry %q; a placeholder or the display branch there would be a wrong manifest", want)
		}
	}
	// And the branch node's own leaf reaches no artifact at all, because its
	// template never interpolates it. Asserting that keeps the check above honest
	// about which node proves which half.
	for path, body := range withCapture.Artifacts {
		if strings.Contains(string(body), branchSecret) {
			t.Errorf("%q carries a value no template interpolates; the fixture drifted\n--- artifact ---\n%s", path, body)
		}
	}
}

// TestWorkloadDisplay_ReRenderKeepsTheEmittedDocumentsShape is the structural
// proof, and it is what distinguishes substituting a placeholder from re-rendering
// against a context with the keys removed. Removing them changes which branches a
// template takes, so the two documents would diverge in shape; substituting keeps a
// string where a string was.
//
// It asserts three things, in increasing strength. The two documents carry the SAME
// leaf paths. Every path except the ones reading the declared leaf is byte-equal.
// And at each differing path, the displayed text is the emitted text with the
// secret's characters — and only those — replaced, which is the assertion a test
// that merely looked for the placeholder somewhere cannot make.
func TestWorkloadDisplay_ReRenderKeepsTheEmittedDocumentsShape(t *testing.T) {
	t.Parallel()
	shown := displayBuild(t, false)
	emitted := displayBuild(t, true)

	for _, slot := range []string{
		"boundaries/display/shape/09b-feature-rendering-boundary.yaml",
		"boundaries/display/shape/10-engine-synthesis/shape.compose-fragment.yaml",
	} {
		shownLeaves := workloadLeaves(t, traceEntry(t, shown, slot))
		emittedLeaves := workloadLeaves(t, traceEntry(t, emitted, slot))

		if got, want := sortedPaths(shownLeaves), sortedPaths(emittedLeaves); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: the re-render must carry the emitted document's leaf paths\n--- shown ---\n%v\n--- emitted ---\n%v", slot, got, want)
			continue
		}

		// The two positions that read the declared leaf, and what each must become.
		// Written as the emitted text with the secret substituted, so the assertion
		// pins the surrounding characters as well as the replacement.
		wantSubstituted := map[string]string{
			"containers.main.variables.APP_URL":    "postgres://svc:" + redact.Placeholder() + "@db.internal:5432/appdb",
			"containers.main.variables.APP_SECRET": redact.Placeholder(),
		}
		for path, emittedText := range emittedLeaves {
			want, substituted := wantSubstituted[path]
			switch {
			case substituted:
				if shownLeaves[path] != want {
					t.Errorf("%s: %s must show the replacement text in the datum's own position\n  want: %q\n  got:  %q\n  emitted: %q", slot, path, want, shownLeaves[path], emittedText)
				}
				if strings.Contains(shownLeaves[path], shapeSecret) {
					t.Errorf("%s: %s leaked %q", slot, path, shapeSecret)
				}
			default:
				if shownLeaves[path] != emittedText {
					t.Errorf("%s: %s reads nothing declared and must be byte-equal\n  emitted: %q\n  shown:   %q", slot, path, emittedText, shownLeaves[path])
				}
			}
		}
		// The control is a positive one: the undeclared sibling in the same document
		// survives with its own authored value, so the substitution discriminated
		// rather than blanking whatever it could reach.
		if got := shownLeaves["containers.main.variables.APP_NAME"]; got != "shapedemo" {
			t.Errorf("%s: the undeclared sibling must survive verbatim; got %q", slot, got)
		}
	}
}

// TestWorkloadDisplay_ContentBranchingTemplateDiverges is the reason the display
// announces itself, asserted rather than assumed.
//
// A template that branches on a value's CONTENT takes a different branch against
// the replacement text, so the displayed document is genuinely not the one the
// build emitted — here the difference is a change in the document's KEY SET, which
// is the strongest form the divergence takes. This is the honest cost of showing a
// document at all, and it is not to be "fixed": the alternative is showing nothing.
//
// The control is an equality test on the UNDECLARED sibling in the same template,
// which must take the same branch in both documents. Without it a display that
// mangled every branch would satisfy the positive assertion.
func TestWorkloadDisplay_ContentBranchingTemplateDiverges(t *testing.T) {
	t.Parallel()
	shown := displayBuild(t, false)
	emitted := displayBuild(t, true)

	const slot = "boundaries/display/branch/09b-feature-rendering-boundary.yaml"
	shownLeaves := workloadLeaves(t, traceEntry(t, shown, slot))
	emittedLeaves := workloadLeaves(t, traceEntry(t, emitted, slot))

	// The equality test on the declared leaf: the emitted document matched the
	// authored value, the displayed one fell through.
	if _, matched := emittedLeaves["containers.main.variables.MATCHED_AUTHORED_VALUE"]; !matched {
		t.Fatalf("the emitted document must have matched the authored value; the divergence below would be vacuous\n--- emitted ---\n%v", sortedPaths(emittedLeaves))
	}
	if _, matched := shownLeaves["containers.main.variables.MATCHED_AUTHORED_VALUE"]; matched {
		t.Errorf("a display re-render cannot match the authored value; the branch would have to have read it\n--- shown ---\n%v", sortedPaths(shownLeaves))
	}
	if _, fell := shownLeaves["containers.main.variables.FELL_THROUGH"]; !fell {
		t.Errorf("the display must have taken the other branch, changing the document's key set\n--- shown ---\n%v", sortedPaths(shownLeaves))
	}

	// The length test: the display reports the replacement text's length, not the
	// datum's. Both are asserted, because "different" is weaker than "the length of
	// what the display actually substituted".
	if got, want := emittedLeaves["containers.main.variables.AUTHORED_VALUE_LENGTH"], fmt.Sprint(len(branchSecret)); got != want {
		t.Errorf("the emitted document must report the authored value's length; want %q, got %q", want, got)
	}
	if got, want := shownLeaves["containers.main.variables.AUTHORED_VALUE_LENGTH"], fmt.Sprint(len(redact.Placeholder())); got != want {
		t.Errorf("the display must report the replacement text's length; want %q, got %q", want, got)
	}

	// The control: the branch on the undeclared sibling resolves identically in both.
	for _, path := range []string{"containers.main.variables.MATCHED_UNDECLARED_VALUE"} {
		if _, matched := emittedLeaves[path]; !matched {
			t.Fatalf("the control branch must have matched in the emitted document; got %v", sortedPaths(emittedLeaves))
		}
		if _, matched := shownLeaves[path]; !matched {
			t.Errorf("a branch on an undeclared value must resolve identically in the display; got %v", sortedPaths(shownLeaves))
		}
	}

	// And the divergence never costs the announcement: a reader of this slot is told
	// what they are looking at.
	if body := traceEntry(t, shown, slot); !strings.Contains(body, redact.SubstitutedProjection()) {
		t.Errorf("%s must announce that its document is a re-render\n--- slot ---\n%s", slot, body)
	}
	// The plaintext is nowhere in the displayed slot, decoded or otherwise.
	for path, text := range shownLeaves {
		if strings.Contains(text, branchSecret) {
			t.Errorf("%s: %s leaked %q", slot, path, branchSecret)
		}
	}
}
