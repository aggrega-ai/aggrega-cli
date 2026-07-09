// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// TestVerifyPatchedBody_PassesCorrectPatch is the no-churn guarantee at the unit
// level: a byte-surgical edit that realizes the ops trips nothing, so the codec
// keeps the surgical (comment- and style-preserving) bytes.
func TestVerifyPatchedBody_PassesCorrectPatch(t *testing.T) {
	t.Parallel()
	original := []byte("a: 1\nb: 2\nc: 3\n")
	ops := []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}}
	correct := []byte("a: 9\nb: 2\nc: 3\n")
	if !verifyPatchedBody(original, correct, ops) {
		t.Fatalf("a correct surgical patch must verify (no trip, no churn)")
	}
}

// TestVerifyPatchedBody_CatchesSiblingAbsorption proves the guard is
// content-agnostic and catches a span-class corruption: an edit whose range
// over-reached and swallowed a sibling member. The guard never enumerates YAML
// construct kinds — it decodes the result and compares it to the intended tree.
func TestVerifyPatchedBody_CatchesSiblingAbsorption(t *testing.T) {
	t.Parallel()
	original := []byte("a: 1\nb: 2\nc: 3\n")
	ops := []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}}
	// A span bug absorbed sibling b into a's replacement span: b vanished.
	corrupt := []byte("a: 9\nc: 3\n")
	if verifyPatchedBody(original, corrupt, ops) {
		t.Fatalf("sibling absorption must trip the guard")
	}
}

// TestVerifyPatchedBody_CatchesUnparseableResult proves an edit that orphaned a
// fragment into structurally-invalid YAML trips the guard rather than shipping.
func TestVerifyPatchedBody_CatchesUnparseableResult(t *testing.T) {
	t.Parallel()
	original := []byte("a: 1\nb: 2\n")
	ops := []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}}
	// A scalar cannot own an indented child mapping: the result does not parse.
	broken := []byte("a: 9\n  b: 2\n")
	if verifyPatchedBody(original, broken, ops) {
		t.Fatalf("an unparseable result must trip the guard")
	}
}

// TestVerifyPatchedBody_CatchesWrongValue proves the guard asserts the patched
// path holds the intended value, not merely that the document parses.
func TestVerifyPatchedBody_CatchesWrongValue(t *testing.T) {
	t.Parallel()
	original := []byte("a: 1\nb: 2\n")
	ops := []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}}
	wrong := []byte("a: 8\nb: 2\n")
	if verifyPatchedBody(original, wrong, ops) {
		t.Fatalf("a patched path landing the wrong value must trip the guard")
	}
}

// TestRemarshalPatchedBody_DeterministicAndCorrect proves the self-heal fallback
// is both correct (decodes to the intended tree) and deterministic (byte-equal
// across runs), so a triggered heal never introduces run-to-run diff churn.
func TestRemarshalPatchedBody_DeterministicAndCorrect(t *testing.T) {
	t.Parallel()
	original := []byte("b: 2\na: 1\n")
	ops := []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}}

	first, err := remarshalPatchedBody(original, ops, 2)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	second, err := remarshalPatchedBody(original, ops, 2)
	if err != nil {
		t.Fatalf("remarshal (second run): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("re-marshal fallback is not deterministic:\n first=%q\nsecond=%q", first, second)
	}

	got, err := YAMLBodyToJSON(first)
	if err != nil {
		t.Fatalf("healed body does not parse: %v\n%s", err, first)
	}
	want, err := intendedTree(original, ops)
	if err != nil {
		t.Fatalf("intended tree: %v", err)
	}
	if !jsonValueEqual(want, got) {
		t.Fatalf("healed body does not decode to the intended tree\n want=%s\n  got=%s", want, got)
	}
}

// TestReassemble_UnverifiablePatchSelfHealsWithWarning is the end-to-end proof:
// a patch whose byte-surgical result decodes differently from its RFC-6902
// intent trips the guard, is re-rendered in full, and surfaces an advisory
// warning — without ever failing the build, and with a correct final result.
//
// The scenario uses an anchor and an alias. Patching the anchored value edits
// only the anchor's bytes; on re-parse the alias re-resolves to the new value,
// so the surgical result decodes with BOTH sites changed. The op's intent,
// however, is to change only the addressed path. The guard catches the
// divergence and self-heals toward the intended result.
func TestReassemble_UnverifiablePatchSelfHealsWithWarning(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	const name = "values.yaml"
	const body = "base: &b hello\ncopy: *b\n"

	ops := []frp.JSONPatchOp{{Key: name, Op: "replace", Path: "/base", Value: "world"}}
	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	u := units[0]
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	out, warnings, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	// The guard NEVER fails the build.
	if err != nil {
		t.Fatalf("guard must never produce a hard error; got %v", err)
	}
	// Exactly one advisory warning, naming the unit.
	if len(warnings) != 1 {
		t.Fatalf("expected one advisory warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0] != PatchVerificationWarning(u.Token) {
		t.Fatalf("unexpected warning text:\n%s", warnings[0])
	}
	// The healed output is correct: it decodes to the intended tree.
	got, err := YAMLBodyToJSON(out)
	if err != nil {
		t.Fatalf("healed output does not parse: %v\n%s", err, out)
	}
	want, err := intendedTree([]byte(body), ops)
	if err != nil {
		t.Fatalf("intended tree: %v", err)
	}
	if !jsonValueEqual(want, got) {
		t.Fatalf("healed output does not decode to the intended tree\n want=%s\n  got=%s\n out=%s", want, got, out)
	}
}

// TestReassemble_CorrectPatchIsGuardNoOp proves the guard is a no-op on a
// correct patch: the output is byte-identical to the un-guarded surgical result,
// and no warning is raised — the zero-churn contract GitOps diffs depend on.
func TestReassemble_CorrectPatchIsGuardNoOp(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	const name = "values.yaml"
	const body = "# top comment\nname: web        # inline\nreplicas: 1\n"

	ops := []frp.JSONPatchOp{{Key: name, Op: "replace", Path: "/replicas", Value: float64(3)}}
	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	u := units[0]
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, warnings, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a correct patch must raise no warning; got %v", warnings)
	}
	const wantOut = "# top comment\nname: web        # inline\nreplicas: 3\n"
	if string(out) != wantOut {
		t.Fatalf("correct patch not byte-surgical (comments/spacing lost)\n want=%q\n  got=%q", wantOut, string(out))
	}
}

// TestPatchVerificationWarning_NamesUnit pins the advisory string's shape: it
// names the affected unit and states that the document was re-rendered.
func TestPatchVerificationWarning_NamesUnit(t *testing.T) {
	t.Parallel()
	w := PatchVerificationWarning("templates/deploy.yaml")
	if !strings.Contains(w, "templates/deploy.yaml") {
		t.Errorf("warning must name the unit: %q", w)
	}
	if !strings.Contains(w, "re-rendered") {
		t.Errorf("warning must state the re-render fallback: %q", w)
	}
}
