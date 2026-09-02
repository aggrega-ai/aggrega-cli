// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Reproduction corpus for what a dispatch group does with the identity it holds a
// boundary-scope contributor under.
//
// A Feature attached at a boundary reaches its FeatureRecipe render under an
// identity that carries the boundary it was attached to, and reaches the dispatch
// group its output lands in under one that does not. Where two boundaries'
// declared directories evaluate alike they feed one group, so one instance name
// written at two boundaries arrives at that group as one contributor — and the
// name the assembled document ends up carrying is derived from the identity the
// group held rather than from the one the render declared.
//
// Every test here reads the build's own output: the halt text verbatim, the
// emitted path set, and the assembled stack's own Outputs. None of them asserts a
// value this file computed.

package compiler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
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
// stack Output name the boundary-scope render DECLARED, and reports the outcome.
func TestReproDeclaredNameClaimant(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("declared-name"))
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
	}
}

// TestReproMintedNameClaimant drives the variant whose component publishes the
// stack Output name the assembled stack actually carries for the boundary-scope
// attachment, and reports the outcome.
func TestReproMintedNameClaimant(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("minted-name"))
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
		for _, n := range names {
			b, _ := json.Marshal(outputs[n])
			t.Logf("  %s = %s", n, b)
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
func TestReproDistinctResIdenticalValue(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("distinct-res-identical-value"))
	if err != nil {
		t.Logf("BUILD HALTED, full chain:\n%s", bsErrorChain(err))
		return
	}
	t.Log("BUILD RETURNED NO ERROR")
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
	resources := mustCfnMap(t, stack, "Resources")
	rnames := mapKeys(resources)
	sort.Strings(rnames)
	t.Logf("Resources (%d): %v", len(rnames), rnames)
	bsReportOutputs(t, stack)
	for _, n := range []string{"BoundaryFrAuditZoneAEndpoint", "BoundaryFrAuditZoneBEndpoint", "BoundaryFrAuditEndpoint"} {
		t.Logf("emitted output carries %q anywhere? %v", n, bsAnyArtifactContains(res.Artifacts, n))
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
// evaluate alike, each attaching a different Feature under one instance name, and
// reports the whole emitted path set.
func TestReproHelmTwoBoundaries(t *testing.T) {
	t.Parallel()

	res, err := pipelinetest.BuildE(t, bsIdentityInput("helm-two-boundaries"))
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
