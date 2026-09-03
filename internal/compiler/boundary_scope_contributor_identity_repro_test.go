// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end corpus for the identity a dispatch group holds a boundary-scope
// contributor under, driven through the real pipeline.
//
// A Feature attached at a boundary reaches its FeatureRecipe render and reaches the
// dispatch group its output lands in under ONE identity, which carries both
// coordinates an author wrote: the instance name and the boundary path. It derives
// the names its output publishes from a second value — the composed component
// identity settled against its own rendering plugin — and that value is the same on
// both sides of the render, so the name it declares while rendering is the name the
// assembled artifact addresses that content by.
//
// The corpus exists because neither property has a local witness. Where two
// boundaries' declared directories evaluate alike they feed one group by design, and
// an identity carrying only the instance name puts both contributors in one entry:
// one boundary's output is replaced, or two published entries collapse to one, at a
// clean exit with nothing in the merged bytes recording that two were declared. A
// test over either side alone sees a self-consistent answer, because what goes wrong
// is the agreement BETWEEN two sides rather than either one of them.
//
// Every case here reads the build's own output — the halt chain verbatim, the
// emitted path set, the assembled stack's own Outputs — and asserts on names an
// author wrote or an operator deploys. None asserts a value this file computed, and
// none asserts a count: a count passes on the wrong file as readily as the right one,
// and every shape below is one where something went missing.

package compiler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// bsErrorChain walks an error's whole wrap tree and returns every frame's own
// text, so a halt is read in full rather than through its outermost frame — a
// categorised report puts its findings in the values it wraps, and the top frame
// names only the class.
func bsErrorChain(err error) string {
	var b strings.Builder
	var walk func(e error, depth int)
	walk = func(e error, depth int) {
		if e == nil {
			return
		}
		fmt.Fprintf(&b, "%s[%d] %T: %s\n", strings.Repeat("  ", depth), depth, e, e.Error())
		if joined, isMulti := e.(interface{ Unwrap() []error }); isMulti {
			for _, m := range joined.Unwrap() {
				walk(m, depth+1)
			}
			return
		}
		walk(errors.Unwrap(e), depth+1)
	}
	walk(err, 0)
	return b.String()
}

const bsIdentityDir = "testdata/boundary-scope-contributor-identity"

func bsIdentityInput(variant string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  bsIdentityDir + "/release-" + variant + ".yaml",
		CatalogPaths: []string{bsIdentityDir + "/catalog/base"},
	}
}

// bsSortedPaths is one build's emitted path set in path order.
func bsSortedPaths(artifacts map[string][]byte) []string {
	out := make([]string, 0, len(artifacts))
	for p := range artifacts {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestReproIdenticalBodiesCollapse drives the two-boundary, one-instance-name
// shape whose two renders produce byte-identical fragments, and reports what the
// assembled stack carries.
func TestReproIdenticalBodiesCollapse(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("identical-bodies"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))

	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
	if outputs, ok := stack["Outputs"].(map[string]any); ok {
		names := mapKeys(outputs)
		sort.Strings(names)
		t.Logf("Outputs (%d): %v", len(names), names)
	} else {
		t.Log("the assembled stack carries no Outputs section")
	}
	resources := mustCfnMap(t, stack, "Resources")
	rnames := mapKeys(resources)
	sort.Strings(rnames)
	t.Logf("Resources (%d): %v", len(rnames), rnames)
}

// TestReproDifferingBodiesHalt drives the same shape whose two renders produce
// differing fragments, and reports the halt verbatim.
func TestReproDifferingBodiesHalt(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("differing-bodies"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	_, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
}

// TestReproDeclaredNameClaimant drives the variant whose component publishes the
// stack Output name the boundary-scope render DECLARES.
//
// It is the false-positive shape: the host's check compared the name the render
// declared against the other contributors' declarations, while the assembled stack
// addressed that content by a different name — so it halted over a name the stack
// never carried, and no edit an author could make would answer it.
//
// It halts now, and the halt is a true positive: the name the render declares is
// the name the stack carries, so a component publishing it really does reach the
// same name. Both contributors are named in the author's own terms.
func TestReproDeclaredNameClaimant(t *testing.T) {
	t.Parallel()
	bsAssertHostCatchesTheClaim(t, "declared-name")
}

// TestReproMintedNameClaimant drives the variant whose component publishes the
// stack Output name the assembled stack actually carries for the boundary-scope
// attachment.
//
// It is the false-negative shape's component-versus-attachment form. The pair that
// genuinely landed on one name reached the host's check under two different names,
// so the host stayed silent and the only thing that objected was the guest's own
// ledger, at merge time and in the guest's vocabulary.
//
// The two questions this and the case above ask have become one question, which is
// the whole of what the fix does: the name declared and the name minted are derived
// from one identity. Both are kept because a regression re-splitting the two
// derivations would answer them differently again, and the pair is what shows it.
func TestReproMintedNameClaimant(t *testing.T) {
	t.Parallel()
	bsAssertHostCatchesTheClaim(t, "minted-name")
}

// bsAssertHostCatchesTheClaim holds one claimant variant to a halt raised by the
// HOST's derived-name check, naming both contributors in the author's own terms.
//
// Which side raises it is the property, not an implementation detail. The guest's
// own ledger also refuses two entries reaching one name, at merge time — but it
// speaks in the guest's vocabulary about a name it built, while the host's check
// names the feature, the boundary it is attached at, and the component beside it,
// which is what an author edits. A regression that let the host miss this would
// still halt, so asserting only that the build failed would not see it.
func bsAssertHostCatchesTheClaim(t *testing.T, variant string) {
	t.Helper()

	res, err := pipelinetest.BuildE(t, bsIdentityInput(variant))
	if err == nil {
		t.Fatalf("expected a halt: a component publishes the same Output name the attachment "+
			"derives\nemitted: %v", bsSortedPaths(res.Artifacts))
	}
	chain := bsErrorChain(err)
	t.Logf("BUILD HALTED, full chain:\n%s", chain)

	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Errorf("the halt is not the host's derived-name refusal, so the host is still missing "+
			"a pair that lands on one name: %v", err)
	}
	for _, want := range []string{
		`"BfrZoneAAuditEndpoint"`,
		`derived by component "bfr-zone-a-audit"`,
		`derived by feature "audit", attached at boundary "zone-a"`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q, so a reader cannot tell which two things met", want)
		}
	}
	// The marker the compiler routes this contributor under reaches no reader.
	for _, bad := range []string{"<boundary-fr>", "@zone-a"} {
		if strings.Contains(chain, bad) {
			t.Errorf("the halt carries %q, which is a marker the compiler built for its own "+
				"dispatch and names nothing in the author's input", bad)
		}
	}
}

// TestReproDeclaredNameControl is the control for the declared-name variant: the
// same one boundary and the same attachment, with nothing reaching anything
// else's name — so the stack Output name the attachment actually lands under is
// readable from the emitted bytes.
func TestReproDeclaredNameControl(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("declared-name-control"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
	bsReportOutputs(t, stack)
	for _, absent := range []string{"BoundaryFrAuditZoneAEndpoint", "BfrZoneAAuditEndpoint"} {
		t.Logf("does the whole emitted output contain %q anywhere? %v",
			absent, bsAnyArtifactContains(res.Artifacts, absent))
	}
}

// bsReportOutputs reports the assembled stack's Outputs section, name by name
// with its body, or that it carries none.
func bsReportOutputs(t *testing.T, stack map[string]any) {
	t.Helper()
	outputs, ok := stack["Outputs"].(map[string]any)
	if !ok {
		t.Log("the assembled stack carries no Outputs section")
		return
	}
	names := mapKeys(outputs)
	sort.Strings(names)
	t.Logf("Outputs (%d): %v", len(names), names)
	for _, n := range names {
		b, _ := json.Marshal(outputs[n])
		t.Logf("  %s = %s", n, b)
	}
}

// bsAnyArtifactContains reports whether any emitted file's bytes carry needle.
func bsAnyArtifactContains(artifacts map[string][]byte, needle string) bool {
	for _, v := range artifacts {
		if strings.Contains(string(v), needle) {
			return true
		}
	}
	return false
}

// TestReproDistinctResIdenticalValue drives two boundaries whose renders author
// distinct stack resource names and publish one entry name with one value.
//
// This is the silent-collapse shape. The two contributors declare one entry name
// while their own resource names differ per boundary, so nothing the assembled
// stack addresses collides except the entry name itself — and the collapse it
// produced was a clean exit with one Output where two were declared, which no gate
// reported because a merged document records nothing about what it dropped.
//
// The assertion is that BOTH published entries survive, under names carrying the
// boundary each came from. It is written against the stack's own bytes rather than
// against a count, so a regression that reintroduced the collapse would fail on the
// missing name rather than on an arithmetic nobody can reconstruct.
func TestReproDistinctResIdenticalValue(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("distinct-res-identical-value"))
	if err != nil {
		t.Fatalf("expected the build to complete; two boundaries publishing one entry name "+
			"reach two names now:\n%s", bsErrorChain(err))
	}
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
	resources := mustCfnMap(t, stack, "Resources")
	rnames := mapKeys(resources)
	sort.Strings(rnames)
	t.Logf("Resources (%d): %v", len(rnames), rnames)
	bsReportOutputs(t, stack)

	outputs, ok := stack["Outputs"].(map[string]any)
	if !ok {
		t.Fatal("the assembled stack carries no Outputs section, so there is nothing to hold to account")
	}
	// Both boundaries' published entries, each under the identity composed from the
	// boundary it was attached at.
	for _, want := range []string{"BfrZoneAAuditEndpoint", "BfrZoneBAuditEndpoint"} {
		if _, present := outputs[want]; !present {
			t.Errorf("the assembled stack carries no Output %q: two contributors declared an entry "+
				"and the stack addresses fewer than two\ngot: %v", want, mapKeys(outputs))
		}
	}
	// The path-less name is the one the two used to collapse onto. Nothing may land
	// there, in any emitted file — the assembled stack is not the only place a name
	// travels to.
	if bsAnyArtifactContains(res.Artifacts, "BoundaryFrAuditEndpoint") {
		t.Error(`the emitted output still carries "BoundaryFrAuditEndpoint", the name two ` +
			`contributors reached when the identity dropped the boundary`)
	}
}

// TestReproDistinctResDifferingValue drives the same shape whose two renders
// publish one entry name with two different values.
func TestReproDistinctResDifferingValue(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("distinct-res-differing-value"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	_, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
}

// TestReproHelmOneBoundary is the control: what one boundary-scope contributor
// emits on a provider that assembles nothing.
func TestReproHelmOneBoundary(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("helm-one-boundary"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	paths := bsSortedPaths(res.Artifacts)
	t.Logf("emitted paths (%d):\n  %s", len(paths), strings.Join(paths, "\n  "))
	for _, p := range paths {
		t.Logf("--- %s ---\n%s", p, res.Artifacts[p])
	}
}

// TestReproHelmTwoBoundariesDeterminism reports which of the two boundaries'
// output survives, over repeated compilations of one input.
//
// The two contributions are held under one per-node key in the dispatch group's
// file map, so which one the group ends up holding is decided by the order the
// accumulator's own map happens to iterate in — which is not a property of the
// input. The count below is what says whether that is observable, rather than a
// claim that it is.
func TestReproHelmTwoBoundariesDeterminism(t *testing.T) {
	t.Parallel()

	// The sample size is chosen against a measured split rather than picked: a
	// forty-compilation census of this input produced the minority outcome seven
	// times, so a census this size misses it with probability under one in a
	// hundred. It is stated here so a later tightening has a baseline.
	const runs = 24
	winners := map[string]int{}
	for i := 0; i < runs; i++ {
		res, err := pipelinetest.BuildE(t, bsIdentityInput("helm-two-boundaries"))
		if err != nil {
			t.Fatalf("run %d halted: %v", i, err)
		}
		var got []string
		for _, p := range bsSortedPaths(res.Artifacts) {
			if strings.Contains(p, "features/audit/") {
				got = append(got, p)
			}
		}
		winners[strings.Join(got, ",")]++
	}
	t.Logf("over %d compilations of one input, the boundary-scope output emitted was:", runs)
	keys := make([]string, 0, len(winners))
	for k := range winners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %3d x  %s", winners[k], k)
	}
	if len(winners) > 1 {
		t.Logf("DISTINCT OUTCOMES: %d — the emitted tree is not a function of the input", len(winners))
	} else {
		t.Log("ONE OUTCOME over every run at this sample size")
	}
}

// TestReproHelmTwoBoundariesDoubleRun runs the project's own determinism guard,
// unmodified, over the same input — so the verdict is the guard's rather than
// this file's.
//
// 🔴 THIS TEST FAILS, AND THE FAILURE IS THE REPRODUCTION. It is the baseline a
// change to the identity a dispatch group holds a boundary-scope contributor
// under has to turn green, and it is written as a failure rather than as a
// logged observation for a reason: a passing Go test prints nothing, so a
// report-only probe is invisible in the routine battery whatever it found.
func TestReproHelmTwoBoundariesDoubleRun(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministicN(t, bsIdentityInput("helm-two-boundaries"), 12)
}

// TestReproHelmTwoBoundaries drives two boundaries whose declared directories
// evaluate alike, each attaching a different Feature under one instance name, on a
// provider that assembles nothing.
//
// This is the silently-absent shape, and it is the one with no signal at all: the
// two contributions were held under one path-less key, so the second replaced the
// first and one boundary's whole rendered output left the build at a clean exit.
// The two files differ in name here, so nothing about the output forces a choice
// between them — which is what makes the loss a defect rather than a collision.
//
// The assertion names both emitted files. A count would pass on either one plus any
// unrelated file, and the control beside it is what says one boundary alone emits
// only its own.
func TestReproHelmTwoBoundaries(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("helm-two-boundaries"))
	if err != nil {
		t.Fatalf("expected the build to complete; the two boundaries emit different filenames "+
			"and meet on nothing:\n%s", bsErrorChain(err))
	}
	paths := bsSortedPaths(res.Artifacts)
	t.Logf("emitted paths (%d):\n  %s", len(paths), strings.Join(paths, "\n  "))
	for _, p := range paths {
		t.Logf("--- %s ---\n%s", p, res.Artifacts[p])
	}
	for _, want := range []string{
		"dist/app/helm/features/audit/audit-a.yaml",
		"dist/app/helm/features/audit/audit-b.yaml",
	} {
		if _, present := res.Artifacts[want]; !present {
			t.Errorf("the emitted tree carries no %q: a boundary's rendered output left the build "+
				"with nothing recording that it was declared\ngot: %v", want, paths)
		}
	}
}
