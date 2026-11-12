// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// TestReproDeclaredNameClaimant drives the variant whose component is named so
// that its own published endpoint reads almost the same as the identity the
// boundary-scope render DECLARES, and holds the two apart.
//
// This case witnesses separation. The stack Output name is derived by writing an
// identity and an entry name into the alphabet an Output name admits, escaping
// what that alphabet refuses rather than dropping it, so the derivation is
// injective in the two together: one name is reached by one such pair and by no
// other. A component reaching the attachment's name would therefore have to be
// named exactly as the attachment's composed identity is written,
// "bfr--zone-a--audit" — and an authored component identity opening on a reserved
// scope is refused, so no author can write it. There is no input that puts the two
// on one name, and so no halt available to assert here.
//
// The host's refusal of two contributors reaching one derived name is exercised
// end to end next door, by TestCloudFormationAttachmentOutputsOverlappingEntry_Halts:
// a component-scope attachment derives its Output names from the component it is
// attached to, so that component and its attachment meet on an entry name an
// author wrote, which is a collision an author can still reach.
func TestReproDeclaredNameClaimant(t *testing.T) {
	t.Parallel()
	bsAssertTheTwoIdentitiesStayApart(t, "declared-name")
}

// TestReproMintedNameClaimant drives the variant whose component is named so that
// its own published endpoint reads almost the same as the stack Output name the
// assembled stack actually carries for the boundary-scope attachment, and holds
// the two apart.
//
// The name a render declares and the name the assembled stack carries are derived
// from one identity, so this variant and the one above ask one question. The pair
// is kept because a regression splitting those two derivations would answer them
// differently, and only the pair shows it.
//
// This case witnesses separation, for the same reason its sibling does: the
// derivation is injective in the identity and the entry name together, so reaching
// the attachment's name would need a component named exactly
// "bfr--zone-a--audit", and an authored component identity opening on a reserved
// scope is refused. The collision is unconstructible, so there is no halt to
// assert here.
//
// The host's refusal of two contributors reaching one derived name is exercised
// end to end next door, by TestCloudFormationAttachmentOutputsOverlappingEntry_Halts,
// where a component-scope attachment and its host component meet on an entry name
// an author wrote.
func TestReproMintedNameClaimant(t *testing.T) {
	t.Parallel()
	bsAssertTheTwoIdentitiesStayApart(t, "minted-name")
}

// bsAssertTheTwoIdentitiesStayApart holds one claimant variant to the separation
// the stack Output derivation guarantees, asserted on the assembled stack's own
// names.
//
// The variant puts an authored component beside a boundary-scope attachment whose
// composed identity reads almost the same, and both publish one entry name. Each
// identity is written into the alphabet an Output name admits by escaping the
// separators that alphabet refuses rather than dropping them, so the two arrive as
// two names and the stack addresses both.
//
// Both names are asserted present, and asserted to be two names rather than one. A
// case asserting only that the build completed would pass just as well if the
// attachment published nothing at all.
func bsAssertTheTwoIdentitiesStayApart(t *testing.T, variant string) {
	t.Helper()

	// The name the component's own identity reaches, and the name the attachment's
	// composed identity reaches. Both are written out rather than derived here,
	// because a name computed through the derivation would agree with the
	// derivation whatever it did.
	const (
		componentOutput  = "bfrXzhzoneXzhaXzhauditXXendpoint"
		attachmentOutput = "bfrXzhXzhzoneXzhaXzhXzhauditXXendpoint"
	)
	if componentOutput == attachmentOutput {
		t.Fatal("the two names this case holds apart are one string, so every assertion below " +
			"reads one name twice and the separation is not under test")
	}

	res, err := pipelinetest.BuildE(t, bsIdentityInput(variant))
	if err != nil {
		t.Fatalf("expected the build to complete: a component and a boundary-scope attachment "+
			"whose identities read alike reach two Output names\n%s", bsErrorChain(err))
	}
	t.Logf("emitted paths:\n  %s", strings.Join(bsSortedPaths(res.Artifacts), "\n  "))
	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack:\n%s", raw)
	bsReportOutputs(t, stack)

	outputs, ok := stack["Outputs"].(map[string]any)
	if !ok {
		t.Fatal("the assembled stack carries no Outputs section, so neither name is there to hold apart")
	}
	for _, want := range []string{componentOutput, attachmentOutput} {
		if _, present := outputs[want]; !present {
			t.Errorf("the assembled stack carries no Output %q: a component and a boundary-scope "+
				"attachment each published an entry and the stack addresses fewer than two\ngot: %v",
				want, mapKeys(outputs))
		}
	}
	// The marker the compiler routes this contributor under reaches no reader. The
	// readable surface here is the emitted output rather than a halt, and both
	// spellings are ones the compiler really builds: the synthetic node id is the
	// marker followed by the instance and the boundary it was attached at.
	for _, bad := range []string{"<boundary-fr>", "@zone-a"} {
		if bsAnyArtifactContains(res.Artifacts, bad) {
			t.Errorf("the emitted output carries %q, which is a marker the compiler built for its "+
				"own dispatch and names nothing in the author's input", bad)
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
	// The path-less name is the one the attachment reaches when its identity drops
	// the boundary, leaving the scope and the instance alone. It is written out
	// under the same derivation the reported names are, so it stays a name the
	// emitted output could actually carry: a probe naming a string no derivation
	// can produce could only ever report the same answer. The whole emitted output
	// is read rather than the stack alone, because a name travels further than the
	// document it is declared in.
	const pathLess = "bfrXzhXzhauditXXendpoint"
	t.Logf("does the whole emitted output contain %q, the name the attachment reaches "+
		"when its identity drops the boundary? %v",
		pathLess, bsAnyArtifactContains(res.Artifacts, pathLess))
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
	// boundary it was attached at. The composed identity is the boundary-feature
	// scope, the boundary and the instance; the provider writes it and the authored
	// entry name into the alphabet an Output name admits, so each hyphen of the
	// composition arrives as an escape rather than being dropped.
	for _, want := range []string{
		"bfrXzhXzhzoneXzhaXzhXzhauditXXendpoint",
		"bfrXzhXzhzoneXzhbXzhXzhauditXXendpoint",
	} {
		if _, present := outputs[want]; !present {
			t.Errorf("the assembled stack carries no Output %q: two contributors declared an entry "+
				"and the stack addresses fewer than two\ngot: %v", want, mapKeys(outputs))
		}
	}
	// The path-less name is the one two contributors reach when the identity drops
	// the boundary, leaving the scope and the instance alone. Nothing may land
	// there, in any emitted file — the assembled stack is not the only place a name
	// travels to. It is written out under the same derivation the two names above
	// are, so it remains a name the emitted output could actually carry: a control
	// spelling a name no derivation can produce could never fire.
	if bsAnyArtifactContains(res.Artifacts, "bfrXzhXzhauditXXendpoint") {
		t.Error(`the emitted output still carries "bfrXzhXzhauditXXendpoint", the name two ` +
			`contributors reach when the identity drops the boundary`)
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
		"dist/app/helm/audit--audit-a.yaml",
		"dist/app/helm/audit--audit-b.yaml",
	} {
		if _, present := res.Artifacts[want]; !present {
			t.Errorf("the emitted tree carries no %q: a boundary's rendered output left the build "+
				"with nothing recording that it was declared\ngot: %v", want, paths)
		}
	}
}
