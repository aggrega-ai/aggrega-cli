// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// reassembleOne runs Segment + Reassemble for one patched unit, returning the
// rebuilt file bytes. It mirrors the renderer's fold-back: segment, apply ops
// to the addressed unit's tree via the stock applier, then reassemble.
func reassembleOne(t *testing.T, codec stockYAMLCodec, name string, body []byte, ops []frp.JSONPatchOp) []byte {
	t.Helper()
	units, err := codec.Segment(name, body)
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	u := units[0]
	if !u.Structured() {
		t.Fatalf("unit is opaque; expected a structured tree")
	}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, err := codec.Reassemble(name, body, units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	return out
}

// TestO4_AnchorsAndCommentsSurviveSingleDocPatch is the O4 proof: a PATCH on a
// single-document values.yaml that adds and replaces fields leaves anchors,
// aliases, merge keys, inline comments, and scalar styles in the SAME document
// byte-identical — only the addressed byte-spans change. The doc-granular
// predecessor (a whole-body JSON round-trip) could not pass this.
func TestO4_AnchorsAndCommentsSurviveSingleDocPatch(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)

	const body = `# top-of-file comment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web          # the workload name
  annotations:
    existing: keep-me # do not touch
defaults: &defaults
  cpu: "100m"
  mem: '256Mi'
resources:
  <<: *defaults
  disk: "10Gi"
replicas: 1
`

	ops := []frp.JSONPatchOp{
		// Replace a scalar value (single-quoted style must survive elsewhere).
		{Scope: frp.DocumentScopeOwn, Key: "values.yaml", Op: "replace", Path: "/replicas", Value: float64(3)},
		// Add a new top-level member.
		{Scope: frp.DocumentScopeOwn, Key: "values.yaml", Op: "add", Path: "/team", Value: "payments"},
		// Add a new member into an existing block mapping (annotations).
		{Scope: frp.DocumentScopeOwn, Key: "values.yaml", Op: "add", Path: "/metadata/annotations/added", Value: "on-call"},
	}

	out := string(reassembleOne(t, codec, "values.yaml", []byte(body), ops))

	// Anchors, aliases, and merge keys survive verbatim.
	for _, frag := range []string{
		"# top-of-file comment\n",
		"defaults: &defaults\n",
		`  cpu: "100m"`,
		`  mem: '256Mi'`,    // single-quoted scalar style preserved
		"  <<: *defaults\n", // merge key + alias preserved
		`  disk: "10Gi"`,
		"name: web          # the workload name", // inline comment + spacing preserved
		"    existing: keep-me # do not touch",   // sibling comment preserved
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("O4 fidelity lost: fragment %q missing from output:\n%s", frag, out)
		}
	}

	// The replaced scalar changed.
	if !strings.Contains(out, "replicas: 3\n") {
		t.Errorf("replace did not land: replicas not 3:\n%s", out)
	}
	if strings.Contains(out, "replicas: 1\n") {
		t.Errorf("old replicas value still present:\n%s", out)
	}
	// The added top-level member landed.
	if !strings.Contains(out, "team: payments\n") {
		t.Errorf("top-level add did not land:\n%s", out)
	}
	// The added annotation landed at the annotations child indent.
	if !strings.Contains(out, "    added: on-call\n") {
		t.Errorf("nested add did not land at child indent:\n%s", out)
	}

	// The output must still parse as exactly one document.
	docs, derr := decodeAllDocs(out)
	if derr != nil {
		t.Fatalf("patched output does not re-parse: %v\n%s", derr, out)
	}
	if len(docs) != 1 {
		t.Fatalf("patched output has %d documents, want 1", len(docs))
	}
}

// TestO4_QuotedScalarStylePreservedOnReplace proves a replace of a quoted
// scalar value keeps the quoting style: a single-quoted target stays single
// quoted, a double-quoted target stays double quoted.
func TestO4_QuotedScalarStylePreservedOnReplace(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	const body = "a: 'single'\nb: \"double\"\nc: plain\n"

	out := string(reassembleOne(t, codec, "v.yaml", []byte(body), []frp.JSONPatchOp{
		{Key: "v.yaml", Op: "replace", Path: "/a", Value: "one"},
		{Key: "v.yaml", Op: "replace", Path: "/b", Value: "two"},
		{Key: "v.yaml", Op: "replace", Path: "/c", Value: "three"},
	}))

	for _, want := range []string{"a: 'one'\n", "b: \"two\"\n", "c: three\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("scalar style not preserved on replace: want %q in:\n%s", want, out)
		}
	}
}

// TestO4_RemoveMemberDeletesOnlyItsSpan proves a remove deletes exactly the
// targeted member's line and leaves its siblings and their comments verbatim.
func TestO4_RemoveMemberDeletesOnlyItsSpan(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	const body = "keep1: a   # first\ndrop: b\nkeep2: c   # last\n"

	out := string(reassembleOne(t, codec, "v.yaml", []byte(body), []frp.JSONPatchOp{
		{Key: "v.yaml", Op: "remove", Path: "/drop"},
	}))

	if strings.Contains(out, "drop:") {
		t.Errorf("removed member still present:\n%s", out)
	}
	for _, want := range []string{"keep1: a   # first\n", "keep2: c   # last\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("sibling lost on remove: want %q in:\n%s", want, out)
		}
	}
}

// TestStockCodec_RoundTripNoPatch proves the codec's paired contract: an
// unpatched reassembly reproduces the body byte-for-byte (the round-trip
// invariant), reusing the segmenter's adversarial corpus.
func TestStockCodec_RoundTripNoPatch(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	for _, tc := range segmenterCorpus() {
		if tc.parseErr {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			units, err := codec.Segment("f.yaml", body)
			if err != nil {
				t.Fatalf("Segment: %v", err)
			}
			out, err := codec.Reassemble("f.yaml", body, units, nil)
			if err != nil {
				t.Fatalf("Reassemble: %v", err)
			}
			if string(out) != tc.body {
				t.Fatalf("round-trip mismatch\n orig=%q\n  got=%q", tc.body, string(out))
			}
		})
	}
}

// TestStockCodec_OpaqueUnitHasNoTree proves a body the codec cannot tree is
// carved into an opaque unit (MediaType + Bytes, no Tree) rather than dropped:
// the guest still sees it, tagged.
func TestStockCodec_OpaqueUnitHasNoTree(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	// A YAML scalar document (not a mapping) has no object tree-view for
	// RFC-6902-over-object; the codec yields an opaque unit.
	units, err := codec.Segment("note.txt", []byte("just a bare scalar line\n"))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	if units[0].Structured() {
		t.Errorf("expected an opaque unit (no tree) for a non-object body; got tree=%s", units[0].Tree)
	}
	if units[0].MediaType == "" || len(units[0].Bytes) == 0 {
		t.Errorf("opaque unit not tagged with media type + bytes: %+v", units[0])
	}
}

// TestStockCodec_NonSurgicalOpSurfaces proves the codec surfaces a hard error
// for an op class it cannot realise as a byte-span edit rather than falling
// back to a whole-document re-serialise — the escalate-if guard.
func TestStockCodec_NonSurgicalOpSurfaces(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)
	const body = "a:\n  b: 1\n"
	// Replacing a mapping value (not a scalar) is not surgically supported.
	units, _ := codec.Segment("v.yaml", []byte(body))
	newTree, err := frp.StockApplier{}.Apply(units[0].Tree, []frp.JSONPatchOp{
		{Key: "v.yaml", Op: "replace", Path: "/a", Value: map[string]any{"b": 2}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	_, rerr := codec.Reassemble("v.yaml", []byte(body), units, []frp.PatchedUnit{
		{Token: "v.yaml", Tree: newTree, Ops: []frp.JSONPatchOp{{Key: "v.yaml", Op: "replace", Path: "/a", Value: map[string]any{"b": 2}}}},
	})
	if rerr == nil {
		t.Fatalf("expected a hard error for a non-surgical op; got nil (a silent whole-document fallback would be wrong)")
	}
	var nse *nonSurgicalOpError
	if !errors.As(rerr, &nse) {
		t.Errorf("expected a nonSurgicalOpError; got %v", rerr)
	}
}
