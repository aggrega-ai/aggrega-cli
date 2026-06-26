// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/codecconformance"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// TestStockYAMLCodecConformance holds the core's stock YAML codec to the shared
// codec contract — determinism, round-trip, patch-isolation, and op-apply — over
// three corpus groups:
//
//   - the adversarial segmenter corpus (segmenterCorpus): block scalars
//     (literal | and folded >), CRLF line endings, BOM prefix, %TAG/%YAML
//     directives, empty and null documents, leading/inter-document/trailing
//     comments, no-trailing-newline, bare separators, inline flow documents.
//     Inputs the YAML parser rejects are fed as halt cases (Segment must error).
//   - the RFC-6902 op-apply cases (codecconformance.RFC6902Cases): replace of a
//     scalar value, add of a mapping member, remove of a mapping member, and an
//     absent-path halt.
//   - the absent-token guardrail (CheckAbsentTokenHalts): a Reassemble naming a
//     token the segmentation never produced must be rejected.
func TestStockYAMLCodecConformance(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)

	corpus := stockYAMLCorpus()
	corpus = append(corpus, codecconformance.RFC6902Cases()...)
	codecconformance.Run(t, codec, corpus)

	if err := codecconformance.CheckAbsentTokenHalts(codec); err != nil {
		t.Error(err)
	}
}

// stockYAMLCorpus maps the adversarial segmenter corpus onto conformance cases.
// A parse-rejecting input becomes a halt case (Segment must error); every other
// case asserts round-trip identity. The empty-body case (zero segments) is
// dropped — there is no unit to address and round-trip identity over empty
// bytes is trivial.
func stockYAMLCorpus() []codecconformance.Case {
	const name = "manifest.yaml"
	var out []codecconformance.Case
	for _, tc := range segmenterCorpus() {
		if tc.body == "" {
			continue
		}
		out = append(out, codecconformance.Case{
			Name:     tc.name,
			Filename: name,
			Input:    []byte(tc.body),
			WantHalt: tc.parseErr,
		})
	}
	// A two-document case carrying ops on the first document — exercises
	// patch-isolation (the sibling document survives verbatim) through the
	// shared harness against the stock codec.
	out = append(out, codecconformance.Case{
		Name:      "patch-isolation-two-docs",
		Filename:  name,
		Input:     []byte("a: 1\n---\nb: 2\n"),
		TargetSel: firstStructured,
		Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}},
		WantBytes: []byte("a: 9\n---\nb: 2\n"),
	})
	return out
}

// firstStructured selects the first structured unit — the explicit form of the
// harness default, used where a case names a TargetSel for clarity.
func firstStructured(units []frp.Unit) (int, bool) {
	for i, u := range units {
		if u.Structured() {
			return i, true
		}
	}
	return 0, false
}
